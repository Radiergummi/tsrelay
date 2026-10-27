package main

import (
	"slices"
	"strings"

	"github.com/Radiergummi/tsrelay/internal/api"
	"github.com/Radiergummi/tsrelay/internal/client"
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
		found, err := relayAPI.ResolveHooks(ctx, refs)
		if err != nil {
			return exitWith(out.fail(err))
		}
		var list []api.Hook // found without duplicates
		for _, h := range found {
			if !slices.ContainsFunc(list, func(x api.Hook) bool { return x.ID == h.ID }) {
				list = append(list, h)
			}
		}
		hold := list[0].Effective.HoldTimeout.Std()
		out.headline(target.String())
		for _, h := range list {
			hold = min(hold, h.Effective.HoldTimeout.Std())
			out.url(h, false)
		}
		s := &client.Session{API: relayAPI, Hooks: list, Target: target, Insecure: *insecure}
		s.OnEvent = c.sessionEvents(s, out, hold, nil)
		return exitWith(out.finish(s.Run(ctx)))
	}
	return cmd
}
