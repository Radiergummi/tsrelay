package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Radiergummi/tsrelay/internal/api"
	"github.com/Radiergummi/tsrelay/internal/client"
	"github.com/Radiergummi/tsrelay/internal/hooks"
)

const hooksUsage = `Usage:
  tsrelay hooks ls [--json]               list your hooks
  tsrelay hooks create [<path>] [flags]   create a persistent hook
  tsrelay hooks update <id|path> [flags]  change a hook
  tsrelay hooks rm <id|path>...           delete hooks

Run "tsrelay hooks <command> --help" for the flags of a command.
`

func hooksCmd(ctx context.Context, args []string, stdout, stderr io.Writer, d deps) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, hooksUsage)
		return 2
	}
	switch args[0] {
	case "ls":
		return hooksLs(ctx, args[1:], stdout, stderr, d)
	case "create":
		return hooksCreate(ctx, args[1:], stdout, stderr, d)
	case "update":
		return hooksUpdate(ctx, args[1:], stdout, stderr, d)
	case "rm":
		return hooksRm(ctx, args[1:], stderr, d)
	default:
		fmt.Fprintf(stderr, "tsrelay hooks: unknown command %q\n\n%s", args[0], hooksUsage)
		return 2
	}
}

// commandFlags starts the flag set of a client command. Every one of them
// takes --relay.
func commandFlags(name, usage string, stderr io.Writer) (*flag.FlagSet, *string) {
	fs := flag.NewFlagSet("tsrelay "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: tsrelay "+usage)
		fs.PrintDefaults()
	}
	relay := fs.String("relay", "", "relay URL or host (default: $TSRELAY_RELAY, else https://tsrelay.<your tailnet>)")
	return fs, relay
}

// failed prints err for a client command and returns exit code 1.
func failed(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "tsrelay: %s\n", errText(err))
	return 1
}

func hooksLs(ctx context.Context, args []string, stdout, stderr io.Writer, d deps) int {
	fs, relay := commandFlags("hooks ls", "hooks ls [--json]", stderr)
	asJSON := fs.Bool("json", false, "print the hooks as a JSON array")
	rest, err := parseArgs(fs, args)
	if err != nil {
		return 2
	}
	if len(rest) > 0 {
		fs.Usage()
		return 2
	}
	c, err := newRelayAPI(ctx, *relay, d)
	if err != nil {
		return failed(stderr, err)
	}
	hs, err := c.ListHooks(ctx)
	if err != nil {
		return failed(stderr, err)
	}
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		enc.Encode(hs)
		return 0
	}
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tPATH\tCHANNEL\tKIND\tTUNNEL\tEXPIRES\tURL")
	for _, h := range hs {
		kind, tun, exp := "persistent", "-", "-"
		if h.Ephemeral {
			kind = "ephemeral"
		}
		if h.Tunnel != nil {
			tun = h.Tunnel.Node
		}
		if h.ExpiresAt != nil {
			exp = h.ExpiresAt.Local().Format(time.DateTime)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", h.ID, h.Path, h.Channel, kind, tun, exp, h.URL)
	}
	tw.Flush()
	return 0
}

func hooksCreate(ctx context.Context, args []string, stdout, stderr io.Writer, d deps) int {
	fs, relay := commandFlags("hooks create", "hooks create [<path>] [flags]", stderr)
	channel := fs.String("channel", "", `hook channel (default: "default")`)
	description := fs.String("description", "", "free text, at most 280 characters")
	ephemeral := fs.Bool("ephemeral", false, "delete the hook once it had no tunnel for the relay's grace period")
	expiresIn := fs.Duration("expires-in", 0, "delete the hook after this long (0: never)")
	settings := registerHookSettingFlags(fs)
	rest, err := parseArgs(fs, args)
	if err != nil {
		return 2
	}
	if len(rest) > 1 {
		fs.Usage()
		return 2
	}
	s, err := settings()
	if err != nil {
		fmt.Fprintln(stderr, "tsrelay hooks create:", err)
		return 2
	}
	req := hooks.CreateRequest{Channel: *channel, Description: *description, Ephemeral: *ephemeral, Settings: s}
	if len(rest) == 1 {
		req.Path = rest[0]
	}
	if *expiresIn != 0 {
		at := time.Now().Add(*expiresIn)
		req.ExpiresAt = &at
	}
	c, err := newRelayAPI(ctx, *relay, d)
	if err != nil {
		return failed(stderr, err)
	}
	h, err := c.CreateHook(ctx, req)
	if err != nil {
		return failed(stderr, err)
	}
	fmt.Fprintln(stdout, h.URL)
	return 0
}

func hooksUpdate(ctx context.Context, args []string, stdout, stderr io.Writer, d deps) int {
	fs, relay := commandFlags("hooks update", "hooks update <id|path> [flags]", stderr)
	path := fs.String("path", "", "move the hook to this path (changes its URL)")
	channel := fs.String("channel", "", "move the hook to this channel")
	description := fs.String("description", "", "replace the description")
	ephemeral := fs.Bool("ephemeral", false, "make the hook ephemeral, or persistent with --ephemeral=false")
	expiresIn := fs.Duration("expires-in", 0, "expire this long from now (0: never)")
	settings := registerHookSettingFlags(fs)
	rest, err := parseArgs(fs, args)
	if err != nil {
		return 2
	}
	if len(rest) != 1 {
		fs.Usage()
		return 2
	}
	s, err := settings()
	if err != nil {
		fmt.Fprintln(stderr, "tsrelay hooks update:", err)
		return 2
	}
	req := hooks.UpdateRequest{Settings: settingsPatch(s)}
	changes := 0
	fs.Visit(func(f *flag.Flag) {
		if f.Name != "relay" {
			changes++
		}
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
		fmt.Fprintln(stderr, "tsrelay hooks update: nothing to change")
		fs.Usage()
		return 2
	}
	c, err := newRelayAPI(ctx, *relay, d)
	if err != nil {
		return failed(stderr, err)
	}
	found, err := resolveHooks(ctx, c, rest)
	if err != nil {
		return failed(stderr, err)
	}
	h, err := c.UpdateHook(ctx, found[0].ID, req)
	if err != nil {
		return failed(stderr, err)
	}
	fmt.Fprintln(stdout, h.URL)
	return 0
}

func hooksRm(ctx context.Context, args []string, stderr io.Writer, d deps) int {
	fs, relay := commandFlags("hooks rm", "hooks rm <id|path>...", stderr)
	refs, err := parseArgs(fs, args)
	if err != nil {
		return 2
	}
	if len(refs) == 0 {
		fs.Usage()
		return 2
	}
	c, err := newRelayAPI(ctx, *relay, d)
	if err != nil {
		return failed(stderr, err)
	}
	found, err := resolveHooks(ctx, c, refs)
	if err != nil {
		return failed(stderr, err)
	}
	for _, h := range found {
		if err := c.DeleteHook(ctx, h.ID); err != nil {
			return failed(stderr, err)
		}
		fmt.Fprintf(stderr, "Deleted %s\n", h.URL)
	}
	return 0
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
			return nil, fmt.Errorf("you have no hook with ID or path %q (see tsrelay hooks ls)", ref)
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
	}
}

// urlCmd prints the URL tsrelay http would use here, from the state file.
// It never contacts the relay, so it creates nothing.
func urlCmd(ctx context.Context, args []string, stdout, stderr io.Writer, d deps) int {
	fs, relay := commandFlags("url", "url [<port|url>]", stderr)
	rest, err := parseArgs(fs, args)
	if err != nil {
		return 2
	}
	if len(rest) > 1 {
		fs.Usage()
		return 2
	}
	base, err := relayBase(ctx, *relay, d)
	if err != nil {
		return failed(stderr, err)
	}
	state, err := openState(d)
	if err != nil {
		return failed(stderr, err)
	}
	cwd, _ := os.Getwd()
	var target *url.URL
	if len(rest) == 1 {
		if target, err = client.ParseTarget(rest[0]); err != nil {
			fmt.Fprintln(stderr, "tsrelay url:", err)
			return 2
		}
	} else {
		switch targets := state.Targets(base, cwd); len(targets) {
		case 0:
		case 1:
			target, _ = url.Parse(targets[0])
		default:
			fmt.Fprintf(stderr, "tsrelay url: this directory has hooks for several targets (%s); name one\n", strings.Join(targets, ", "))
			return 2
		}
	}
	hookURL := ""
	if target != nil {
		hookURL = state.URL(client.StateKey(base, cwd, target))
	}
	if hookURL == "" {
		fmt.Fprintln(stderr, "tsrelay url: no hook is known for this directory and target yet; run tsrelay http first")
		return 1
	}
	fmt.Fprintln(stdout, hookURL)
	return 0
}
