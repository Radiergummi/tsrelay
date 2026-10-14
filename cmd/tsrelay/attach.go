package main

import (
	"context"
	"net"
	"slices"
	"sync"

	"github.com/Radiergummi/tsrelay/internal/api"
	"github.com/Radiergummi/tsrelay/internal/client"
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
		Args:    usageArgs(cobra.MinimumNArgs(1)),
	}
	to := cmd.Flags().StringP("to", "t", "", "the local app to deliver calls to: a port or an http(s) URL (required)")
	insecure := cmd.Flags().BoolP("insecure", "k", false, "skip TLS certificate verification for an https:// target")
	cmd.RunE = func(cmd *cobra.Command, refs []string) error {
		ctx := cmd.Context()
		if *to == "" {
			return usagef("--to is required")
		}
		target, err := client.ParseTarget(*to)
		if err != nil {
			return &usageError{err.Error()}
		}
		out := newOutput(c.stdout, c.stderr, c.json)

		relayAPI, err := newRelayAPI(ctx, c.relay, c.d)
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
		ids, urls := make([]string, len(list)), make([]string, len(list))
		urlOf := make(map[string]string, len(list)) // guarded by mu once the tunnel runs
		seqs := updateSeqs{}                        // guarded by mu
		var mu sync.Mutex
		hold := list[0].Effective.HoldTimeout.Std()
		for i, h := range list {
			ids[i], urls[i], urlOf[h.ID] = h.ID, h.URL, h.URL
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
			OnState: out.onState(urls, target.String(), hold),
			OnControl: func(c tunnel.Control) {
				mu.Lock() // control messages arrive concurrently
				defer mu.Unlock()
				seqs.observe(c)
				if c.Type == tunnel.ControlHookUpdated {
					for _, h := range c.Hooks {
						if u, ok := urlOf[h.ID]; ok && seqs.fresh(h.ID, c.Seq) && u != h.URL {
							urlOf[h.ID] = h.URL
							out.url(api.Hook{ID: h.ID, URL: h.URL}, false)
						}
					}
				}
				out.dropped(c, func(id string) string { return urlOf[id] })
			},
		}
		return exitWith(out.finish(tc.Run(ctx)))
	}
	return cmd
}
