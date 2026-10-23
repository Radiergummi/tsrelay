package main

import (
	"context"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/Radiergummi/tsrelay/internal/api"
	"github.com/Radiergummi/tsrelay/internal/authz"
	"github.com/Radiergummi/tsrelay/internal/client"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/spf13/cobra"
)

// completionTimeout bounds how long a completion waits for Tailscale and
// the relay. Completions are never cached, so they are never stale.
const completionTimeout = 2 * time.Second

// noCompletions is what any failure completes to: nothing, and no files.
const noCompletions = cobra.ShellCompDirectiveNoFileComp

// completing prepares a completion: cobra runs no PersistentPreRunE for
// the command completed (and the root's skips __complete itself), so it
// applies the TSRELAY_* defaults itself, ignoring bad ones, and it keeps
// the version warning off the terminal. It returns a context that ends
// after completionTimeout.
func (c *cli) completing(cmd *cobra.Command) (context.Context, context.CancelFunc) {
	_ = envDefaults(cmd.Flags())
	c.versionWarned.Do(func() {}) // completions print nothing but candidates
	return context.WithTimeout(cmd.Context(), completionTimeout)
}

// completeHooks completes the caller's hooks by path and by ID, with the
// description, or else the channel, as help. Hooks already named on the
// line are left out. max is how many hooks the command takes (< 0: any
// number).
func (c *cli) completeHooks(max int) cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if max >= 0 && len(args) >= max {
			return nil, noCompletions
		}
		ctx, cancel := c.completing(cmd)
		defer cancel()
		hs, err := c.listHooks(ctx)
		if err != nil {
			return nil, noCompletions
		}
		var out []cobra.Completion
		for _, h := range hs {
			if slices.ContainsFunc(args, func(a string) bool { return a == h.ID || strings.Trim(a, "/") == h.Path }) {
				continue
			}
			help := printable(h.Description)
			if help == "" {
				help = "channel " + h.Channel
			}
			for _, v := range []string{h.Path, h.ID} {
				if strings.HasPrefix(v, toComplete) {
					out = append(out, cobra.CompletionWithDesc(v, help))
				}
			}
		}
		return out, noCompletions
	}
}

func (c *cli) listHooks(ctx context.Context) ([]api.Hook, error) {
	relayAPI, err := c.newRelayAPI(ctx)
	if err != nil {
		return nil, err
	}
	return relayAPI.ListHooks(ctx)
}

// completeChannels completes --channel with the channels the caller may
// receive on by name (their receive patterns without a wildcard) and the
// channels of their hooks.
func (c *cli) completeChannels(cmd *cobra.Command, _ []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	ctx, cancel := c.completing(cmd)
	defer cancel()
	relayAPI, err := c.newRelayAPI(ctx)
	if err != nil {
		return nil, noCompletions
	}
	me, err := relayAPI.Me(ctx)
	if err != nil {
		return nil, noCompletions
	}
	hs, err := relayAPI.ListHooks(ctx)
	if err != nil {
		return nil, noCompletions
	}
	var channels []string
	for _, p := range me.Roles[authz.RoleReceiver] {
		if !strings.Contains(p, "*") {
			channels = append(channels, p)
		}
	}
	for _, h := range hs {
		channels = append(channels, h.Channel)
	}
	slices.Sort(channels)
	var out []cobra.Completion
	for _, ch := range slices.Compact(channels) {
		if strings.HasPrefix(ch, toComplete) {
			out = append(out, ch)
		}
	}
	return out, noCompletions
}

// completeTargets completes a target with those tsrelay http remembered
// for this relay and directory, with the hook URL as help.
func (c *cli) completeTargets(cmd *cobra.Command, _ []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	ctx, cancel := c.completing(cmd)
	defer cancel()
	base, err := relayBase(ctx, c.relay, c.d)
	if err != nil {
		return nil, noCompletions
	}
	state, err := openState(c.d)
	if err != nil {
		return nil, noCompletions
	}
	cwd, _ := os.Getwd()
	var out []cobra.Completion
	for _, t := range state.Targets(base, cwd) {
		u, err := url.Parse(t)
		if err != nil || !strings.HasPrefix(t, toComplete) {
			continue
		}
		out = append(out, cobra.CompletionWithDesc(t, state.URL(client.StateKey(base, cwd, u))))
	}
	return out, noCompletions
}

// completeTargetArg completes the <port|url> argument of http and url.
// After "--", where http's command goes, the shell completes files. (The
// words are searched for it: cobra parses them with a "--" appended
// first, which leaves cmd.ArgsLenAtDash set.)
func (c *cli) completeTargetArg(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	switch {
	case len(c.args) > 0 && slices.Contains(c.args[:len(c.args)-1], "--"):
		return nil, cobra.ShellCompDirectiveDefault
	case len(args) > 0:
		return nil, noCompletions
	}
	return c.completeTargets(cmd, args, toComplete)
}

// enum completes a flag with fixed values.
func enum(values ...string) cobra.CompletionFunc {
	return cobra.FixedCompletions(values, noCompletions)
}

// completeSettingFlags completes the enum flags registerHookSettingFlags
// registered on cmd.
func completeSettingFlags(cmd *cobra.Command) {
	cmd.RegisterFlagCompletionFunc("attach-policy", enum(string(hooks.AttachTakeover), string(hooks.AttachReject)))
	cmd.RegisterFlagCompletionFunc("host-header", enum(string(hooks.HostPreserve), string(hooks.HostRewrite)))
}
