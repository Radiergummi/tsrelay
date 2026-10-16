package main

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Radiergummi/tsrelay/internal/api"
	"github.com/Radiergummi/tsrelay/internal/client"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func newHooksCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:                        "hooks",
		Short:                      "Manage your hooks",
		Args:                       subcommand,
		SuggestionsMinimumDistance: 2,
		RunE:                       c.helpAndFail,
	}
	cmd.AddCommand(newHooksListCmd(c), newHooksShowCmd(c), newHooksCreateCmd(c), newHooksUpdateCmd(c), newHooksDeleteCmd(c))
	return cmd
}

func newHooksListCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List your hooks",
		Args:    arity(0, 0, ""),
	}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		ctx := cmd.Context()
		relayAPI, err := c.newRelayAPI(ctx)
		if err != nil {
			return err
		}
		hs, err := relayAPI.ListHooks(ctx)
		if err != nil {
			return err
		}
		if hs == nil {
			hs = []api.Hook{}
		}
		out, errStream := c.streams()
		if c.json {
			return writeJSON(out, hs)
		}
		writeHookList(out, errStream, hs)
		return nil
	}
	return cmd
}

func newHooksShowCmd(c *cli) *cobra.Command {
	return &cobra.Command{
		Use:               "show <hook>",
		Short:             "Show one hook and its effective settings",
		Args:              arity(1, 1, "hook"),
		ValidArgsFunction: c.completeHooks(1),
		RunE: func(cmd *cobra.Command, refs []string) error {
			ctx := cmd.Context()
			c.hook = refs[0]
			relayAPI, err := c.newRelayAPI(ctx)
			if err != nil {
				return err
			}
			found, err := resolveHooks(ctx, relayAPI, refs)
			if err != nil {
				return err
			}
			out := c.stdoutStream()
			if c.json {
				return writeJSON(out, found[0])
			}
			writeHookShow(out, found[0])
			return nil
		},
	}
}

func newHooksCreateCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create [<path>]",
		Short: "Create a persistent hook",
		Args:  arity(0, 1, "path"),
	}
	fs := cmd.Flags()
	channel := fs.StringP("channel", "c", "", `hook channel (default: "default")`)
	description := fs.String("description", "", "free text, at most 280 characters")
	ephemeral := fs.Bool("ephemeral", false, "delete the hook once it had no tunnel for the relay's grace period")
	expiresIn := fs.Duration("expires-in", 0, "delete the hook after this long (0: never)")
	settings := registerHookSettingFlags(fs, false)
	cmd.RegisterFlagCompletionFunc("channel", c.completeChannels)
	completeSettingFlags(cmd)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		if *expiresIn < 0 {
			return usage("--expires-in can't be negative", "It says how long from now the hook is deleted.",
				strings.Join(append([]string{"tsrelay hooks create"}, args...), " ")+" --expires-in 24h")
		}
		s, err := settings()
		if err != nil {
			return err
		}
		req := hooks.CreateRequest{Channel: *channel, Description: *description, Ephemeral: *ephemeral, Settings: s}
		if len(args) == 1 {
			req.Path = args[0]
		}
		if *expiresIn != 0 {
			at := time.Now().Add(*expiresIn)
			req.ExpiresAt = &at
		}
		relayAPI, err := c.newRelayAPI(ctx)
		if err != nil {
			return err
		}
		h, err := relayAPI.CreateHook(ctx, req)
		if err != nil {
			return err
		}
		return c.printHook(h)
	}
	return cmd
}

func newHooksUpdateCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:               "update <hook>",
		Short:             "Change a hook",
		Args:              arity(1, 1, "hook"),
		ValidArgsFunction: c.completeHooks(1),
	}
	fs := cmd.Flags()
	path := fs.String("path", "", "move the hook to this path (changes its URL)")
	channel := fs.StringP("channel", "c", "", "move the hook to this channel")
	description := fs.String("description", "", "replace the description")
	ephemeral := fs.Bool("ephemeral", false, "make the hook ephemeral, or persistent with --ephemeral=false")
	expiresIn := fs.Duration("expires-in", 0, "expire this long from now (0: never)")
	settings := registerHookSettingFlags(fs, true)
	cmd.RegisterFlagCompletionFunc("channel", c.completeChannels)
	completeSettingFlags(cmd)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		c.hook = args[0]
		if *expiresIn < 0 {
			return usage("--expires-in can't be negative", "It says how long from now the hook is deleted; 0 removes the expiry.",
				"tsrelay hooks update "+args[0]+" --expires-in 24h", "tsrelay hooks update "+args[0]+" --expires-in 0")
		}
		s, err := settings()
		if err != nil {
			return err
		}
		req := hooks.UpdateRequest{Settings: settingsPatch(s)}
		changes := 0
		// cmd.LocalFlags().Visit would not do: LocalFlags() builds a fresh
		// FlagSet that shares the *Flag pointers but never calls Set on
		// itself, so its own Visit (which walks its private "actual" map,
		// not each flag's Changed field) never finds anything. Visiting
		// cmd.Flags() and excluding what InheritedFlags() knows about
		// achieves the same "local, changed" set correctly.
		// A stray TSRELAY_* value is not a change the caller asked for.
		visitTyped(cmd.Flags(), func(f *pflag.Flag) {
			if cmd.InheritedFlags().Lookup(f.Name) != nil {
				return
			}
			changes++
			switch f.Name {
			case "path":
				req.Path = optPtr(path)
			case "channel":
				req.Channel = optPtr(channel)
			case "description":
				req.Description = optPtr(description)
			case "ephemeral":
				req.Ephemeral = optPtr(ephemeral)
			case "expires-in":
				req.ExpiresAt = hooks.Opt[time.Time]{Set: true} // 0 removes the expiry
				if *expiresIn != 0 {
					req.ExpiresAt.Value = new(time.Now().Add(*expiresIn))
				}
			}
		})
		if changes == 0 {
			return usage("Nothing to change", "hooks update changes only what its flags name.",
				"tsrelay hooks update "+args[0]+" --description \"payments callbacks\"",
				"tsrelay hooks update "+args[0]+" --channel prod",
				"tsrelay hooks update --help")
		}
		relayAPI, err := c.newRelayAPI(ctx)
		if err != nil {
			return err
		}
		found, err := resolveHooks(ctx, relayAPI, args)
		if err != nil {
			return err
		}
		h, err := relayAPI.UpdateHook(ctx, found[0].ID, req)
		if err != nil {
			return err
		}
		return c.printHook(h)
	}
	return cmd
}

func newHooksDeleteCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:               "delete <hook>...",
		Aliases:           []string{"rm"},
		Short:             "Delete hooks",
		Args:              arity(1, -1, "hook"),
		ValidArgsFunction: c.completeHooks(-1),
	}
	cmd.RunE = func(cmd *cobra.Command, refs []string) error {
		ctx := cmd.Context()
		c.hook = refs[0]
		relayAPI, err := c.newRelayAPI(ctx)
		if err != nil {
			return err
		}
		found, err := resolveHooks(ctx, relayAPI, refs)
		if err != nil {
			return err
		}
		out, errStream := c.streams()
		ids := make([]string, 0, len(found))
		for _, h := range found {
			if err := relayAPI.DeleteHook(ctx, h.ID); err != nil {
				return err
			}
			ids = append(ids, h.ID)
			if !c.json {
				fmt.Fprintf(errStream.w, "deleted %s\n", h.URL)
			}
		}
		if c.json {
			return writeJSON(out, map[string][]string{"deleted": ids})
		}
		return nil
	}
	return cmd
}

// resolveHooks finds the caller's hooks named by ID or path, in order. It
// fails if any of them does not exist.
func resolveHooks(ctx context.Context, c *client.API, refs []string) ([]api.Hook, error) {
	all, err := c.ListHooks(ctx)
	if err != nil {
		return nil, err
	}
	found := make([]api.Hook, 0, len(refs))
	for _, ref := range refs {
		i := slices.IndexFunc(all, func(h api.Hook) bool { return h.ID == ref })
		if i < 0 {
			i = slices.IndexFunc(all, func(h api.Hook) bool { return h.Path == strings.Trim(ref, "/") })
		}
		if i < 0 {
			return nil, &cliError{Title: fmt.Sprintf("No hook %q", ref), Reason: "You have no hook with that ID or path.",
				Fixes: []string{"tsrelay hooks list"}, Code: "no_such_hook"}
		}
		found = append(found, all[i])
	}
	return found, nil
}

// optPtr is a PATCH field that sets *v, or leaves the field alone if v is nil.
func optPtr[T any](v *T) hooks.Opt[T] { return hooks.Opt[T]{Set: v != nil, Value: v} }

// settingsPatch turns the setting flags that were given into a PATCH.
func settingsPatch(s hooks.Settings) hooks.SettingsPatch {
	return hooks.SettingsPatch{
		AttachPolicy:    optPtr(s.AttachPolicy),
		HostHeader:      optPtr(s.HostHeader),
		HoldTimeout:     optPtr(s.HoldTimeout),
		ResponseTimeout: optPtr(s.ResponseTimeout),
		IdleTimeout:     optPtr(s.IdleTimeout),
		MaxBodyBytes:    optPtr(s.MaxBodyBytes),
		RateLimit:       optPtr(s.RateLimit),
		Capture:         optPtr(s.Capture),
	}
}

// printHook reports a created or updated hook: as JSON, or as its URL.
func (c *cli) printHook(h api.Hook) error {
	out := c.stdoutStream()
	if c.json {
		return writeJSON(out, h)
	}
	fmt.Fprintln(out.w, out.style(styleBold, h.URL))
	return nil
}
