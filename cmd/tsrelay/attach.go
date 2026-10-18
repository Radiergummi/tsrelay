package main

import (
	"context"
	"net"
	"slices"
	"strings"
	"sync"

	"github.com/Radiergummi/tsrelay/internal/api"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
	"github.com/spf13/cobra"
)

// newAttachCmd serves existing hooks, named by ID or path, from one local
// target over one tunnel. Unlike tsrelay http it never creates hooks.
func newAttachCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "attach <hook>... --to <port|url>",
		Short:   "Serve existing hooks from one local app",
		Example: "  tsrelay attach payments ship/v1 --to 8080",
		Args:    arity(1, -1, "hook"),
	}
	cmd.ValidArgsFunction = c.completeHooks(-1)
	to := cmd.Flags().StringP("to", "t", "", "the local app to deliver calls to: a port or an http(s) URL (required)")
	cmd.RegisterFlagCompletionFunc("to", c.completeTargets)
	insecure := cmd.Flags().BoolP("insecure", "k", false, "skip TLS certificate verification for an https:// target")
	background := cmd.Flags().Bool("background", false, "run in the background once attached; see tsrelay ps, logs and stop")
	cmd.RunE = func(cmd *cobra.Command, refs []string) error {
		ctx := cmd.Context()
		c.hook = refs[0]
		example := func(target string) string { return "tsrelay attach " + strings.Join(refs, " ") + " --to " + target }
		if *to == "" {
			return usage("No target to forward calls to", "attach needs to know where your local app listens.",
				example("3000"), example("http://localhost:8080"))
		}
		target, err := parseTarget(*to, example)
		if err != nil {
			return err
		}
		if *background && c.bg == nil {
			return c.startBackground(ctx)
		}
		out := newOutput(c)

		relayAPI, err := c.newRelayAPI(ctx)
		if err != nil {
			return exitWith(out.fail(err))
		}
		out.inspectBase = relayAPI.BaseURL
		found, err := resolveHooks(ctx, relayAPI, refs)
		if err != nil {
			return exitWith(out.fail(err))
		}
		var list []api.Hook // found without duplicates
		for _, h := range found {
			if !slices.ContainsFunc(list, func(x api.Hook) bool { return x.ID == h.ID }) {
				list = append(list, h)
			}
		}
		ids := make([]string, len(list))
		infoOf := make(map[string]tunnel.HookInfo, len(list)) // guarded by mu once the tunnel runs
		seqs := updateSeqs{}                                  // guarded by mu
		var mu sync.Mutex
		hold := list[0].Effective.HoldTimeout.Std()
		out.headline(target.String())
		for i, h := range list {
			ids[i] = h.ID
			infoOf[h.ID] = tunnel.HookInfo{ID: h.ID, URL: h.URL, Public: h.Public, PublicURL: h.PublicURL}
			hold = min(hold, h.Effective.HoldTimeout.Std())
			out.url(h, false)
		}

		tc := &tunnel.Client{
			Hooks:  ids,
			Target: target.Redacted(),
			Dial: func(ctx context.Context, ids []string, a tunnel.Attach) (net.Conn, error) {
				return tunnel.Dial(ctx, relayAPI.HTTP, relayAPI.TunnelURL(a, ids...))
			},
			Handler: callHandler(target, *insecure, out),
			OnState: c.whenAttached(out.onState(target.String(), hold), list, target.Redacted()),
			OnControl: func(ctl tunnel.Control) {
				mu.Lock() // control messages arrive concurrently
				defer mu.Unlock()
				seqs.observe(ctl)
				if ctl.Type == tunnel.ControlHookUpdated {
					for _, h := range ctl.Hooks {
						if cur, ok := infoOf[h.ID]; ok && seqs.fresh(h.ID, ctl.Seq) && cur != h {
							infoOf[h.ID] = h
							c.bg.hookChanged(h.ID, h.ID, h.URL)
							out.url(api.Hook{ID: h.ID, URL: h.URL, Public: h.Public, PublicURL: h.PublicURL}, false)
						}
					}
				}
				out.dropped(ctl, func(id string) string { return infoOf[id].URL })
			},
		}
		return exitWith(out.finish(tc.Run(ctx)))
	}
	return cmd
}
