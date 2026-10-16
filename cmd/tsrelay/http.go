package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Radiergummi/tsrelay/internal/api"
	"github.com/Radiergummi/tsrelay/internal/client"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/problem"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
	"github.com/spf13/cobra"
	"tailscale.com/client/local"
)

// childStopTimeout is how long a command started after "--" gets to exit
// after SIGTERM before it is killed.
const childStopTimeout = 5 * time.Second

func newHTTPCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "http <port|url> [flags] [-- command args...]",
		Short: "Forward a new or remembered hook to a local app",
		Long: "Forward a hook to a local app. The hook is remembered per directory and target, " +
			"so its URL stays the same across runs. A command given after -- is started with " +
			"TSRELAY_URL set and stopped when tsrelay stops.",
		Example: "  tsrelay http 8080\n  tsrelay http 8080 -n payments -- npm run dev",
		Args: func(cmd *cobra.Command, args []string) error {
			switch n := targetArgs(cmd, args); {
			case n == 0:
				return usage("No target to forward calls to", "http needs to know where your local app listens.",
					"tsrelay http 3000", "tsrelay http http://localhost:8080")
			case n > 1:
				return usage(fmt.Sprintf("Unexpected argument %q", args[1]), "http forwards to one local app; a command to start goes after --.",
					fmt.Sprintf("tsrelay http %s -- %s", args[0], strings.Join(args[1:n], " ")))
			}
			return nil
		},
		ValidArgsFunction: c.completeTargetArg,
	}
	fs := cmd.Flags()
	name := fs.StringP("name", "n", "", "hook path below your ~user prefix (default: remembered, else three random words)")
	channel := fs.StringP("channel", "c", "", `hook channel (default: "default")`)
	persistent := fs.BoolP("persistent", "p", false, "keep the hook after tsrelay exits")
	insecure := fs.BoolP("insecure", "k", false, "skip TLS certificate verification for an https:// target")
	public := fs.Bool("public", false, "make the hook reachable from the internet through the relay's Funnel")
	settings := registerHookSettingFlags(fs, false)
	cmd.RegisterFlagCompletionFunc("channel", c.completeChannels)
	completeSettingFlags(cmd)
	cmd.Long += " Settings flags apply only when the hook is created."
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		var command []string
		if d := cmd.ArgsLenAtDash(); d >= 0 {
			command = args[d:]
		}
		target, err := parseTarget(args[0], func(t string) string { return "tsrelay http " + t })
		if err != nil {
			return err
		}
		s, err := settings()
		if err != nil {
			return err
		}
		out := newOutput(c)

		relayAPI, err := c.newRelayAPI(ctx)
		if err != nil {
			return exitWith(out.fail(err))
		}
		out.inspectBase = relayAPI.BaseURL
		state, err := openState(c.d)
		if err != nil {
			return exitWith(out.fail(err))
		}
		cwd, _ := os.Getwd()
		key := client.StateKey(relayAPI.BaseURL, cwd, target)
		req := hooks.CreateRequest{Channel: *channel, Ephemeral: !*persistent, Settings: s, Public: *public}
		c.hook = cmp.Or(*name, state.Path(key))
		hook, created, err := ensureHook(ctx, relayAPI, *name, state.Path(key), req)
		if err != nil {
			return exitWith(out.fail(err))
		}
		c.hook = hook.Path
		if !created && fs.Changed("public") && hook.Public != *public {
			hook, err = relayAPI.UpdateHook(ctx, hook.ID, hooks.UpdateRequest{Public: optPtr(public)})
			if err != nil {
				return exitWith(out.fail(err))
			}
		}
		if !created && !c.json && anyHookSettingGivenOnCLI(fs) {
			out.status(styleWarn, fmt.Sprintf("settings flags apply only when the hook is created; use tsrelay hooks update to change %s", hook.Path))
		}
		if err := state.Remember(key, hook.Path, hook.URL); err != nil {
			out.status(styleWarn, "could not remember the hook path: "+err.Error())
		}
		out.headline(target.String())
		out.url(hook, false)

		startURL := hook.URL // what a command started after "--" gets
		// hook and seqs change when the hook is recreated or updated; mu guards
		// them, as control messages arrive concurrently.
		var mu sync.Mutex
		seqs := updateSeqs{}
		var tc *tunnel.Client
		tc = &tunnel.Client{
			Hooks:  []string{hook.ID},
			Target: target.Redacted(),
			Dial: func(ctx context.Context, ids []string, a tunnel.Attach) (net.Conn, error) {
				conn, err := tunnel.Dial(ctx, relayAPI.HTTP, relayAPI.TunnelURL(a, ids...))
				if !isNotFound(err) {
					return conn, err
				}
				// The relay reaped the hook while this CLI was disconnected for
				// longer than the ephemeral grace period: recreate it at the
				// same path, so its URL keeps working.
				mu.Lock()
				old := hook
				mu.Unlock()
				recreate := req
				recreate.Public = old.Public // as it was last, not as the flags first asked
				h, _, err := ensureHook(ctx, relayAPI, old.Path, "", recreate)
				if err != nil {
					return nil, err
				}
				tc.Replace(old.ID, h.ID)
				mu.Lock()
				hook = h
				mu.Unlock()
				out.url(h, true)
				return tunnel.Dial(ctx, relayAPI.HTTP, relayAPI.TunnelURL(a, h.ID))
			},
			Handler: callHandler(target, *insecure, out),
			OnState: out.onState(target.String(), hook.Effective.HoldTimeout.Std()),
			OnControl: func(ctl tunnel.Control) {
				mu.Lock()
				defer mu.Unlock()
				seqs.observe(ctl)
				if ctl.Type == tunnel.ControlHookUpdated {
					for _, h := range ctl.Hooks {
						if h.ID != hook.ID || !seqs.fresh(h.ID, ctl.Seq) || (h.URL == hook.URL && h.Public == hook.Public && h.PublicURL == hook.PublicURL) {
							continue
						}
						// Only the path can change the URL; the prefix before it stays.
						hook.Path = strings.TrimPrefix(h.URL, strings.TrimSuffix(hook.URL, hook.Path))
						hook.URL, hook.PublicURL, hook.Public = h.URL, h.PublicURL, h.Public
						if err := state.Remember(key, hook.Path, hook.URL); err != nil {
							out.status(styleWarn, "could not remember the hook path: "+err.Error())
						}
						out.url(hook, false)
					}
				}
				out.dropped(ctl, func(string) string { return hook.URL })
			},
		}
		return exitWith(runTunnel(ctx, tc, command, startURL, out))
	}
	return cmd
}

// targetArgs is how many positional arguments come before "--".
func targetArgs(cmd *cobra.Command, args []string) int {
	if d := cmd.ArgsLenAtDash(); d >= 0 {
		return d
	}
	return len(args)
}

// parseTarget is client.ParseTarget with its error explained; example is
// the command line that would work with a given target.
func parseTarget(s string, example func(target string) string) (*url.URL, error) {
	u, err := client.ParseTarget(s)
	if err == nil {
		return u, nil
	}
	e := usage(fmt.Sprintf("%q isn't a port or an http(s) URL", s), "tsrelay forwards calls to a port on this machine, or to an http(s) URL.",
		example("3000"), example("http://localhost:3000"))
	if _, perr := strconv.Atoi(strings.TrimPrefix(s, ":")); perr == nil {
		e.Title, e.Reason = fmt.Sprintf("Port %s is out of range", strings.TrimPrefix(s, ":")), "Ports go from 1 to 65535."
	}
	e.err = err
	return nil, e
}

// runTunnel runs tc until it stops and returns the exit code. With a
// command, it first starts the command with TSRELAY_URL set. Whichever ends
// first stops the other: the command gets SIGTERM, and is killed after
// childStopTimeout. If the command exits on its own, its exit code becomes
// tsrelay's.
func runTunnel(ctx context.Context, tc *tunnel.Client, command []string, hookURL string, out *output) int {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var childDone chan error
	if len(command) > 0 {
		cmd := exec.CommandContext(runCtx, command[0], command[1:]...)
		cmd.Env = append(os.Environ(), "TSRELAY_URL="+hookURL)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, out.stdout, out.stderr
		if out.json {
			cmd.Stdout = out.stderr // stdout carries only events
		}
		cmd.Cancel = func() error {
			if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
				return cmd.Process.Kill() // e.g. on Windows, which has no SIGTERM
			}
			return nil
		}
		cmd.WaitDelay = childStopTimeout
		if err := cmd.Start(); err != nil {
			return out.fail(&cliError{Title: "Couldn't start " + command[0], Reason: sentence(err.Error()), Code: "command_failed", err: err,
				Fixes: []string{"Check that " + command[0] + " is installed and on your PATH"}})
		}
		childDone = make(chan error, 1)
		go func() { childDone <- cmd.Wait() }()
	}
	tunnelDone := make(chan error, 1)
	go func() { tunnelDone <- tc.Run(runCtx) }()

	select {
	case err := <-tunnelDone:
		cancel()
		if childDone != nil {
			<-childDone
		}
		return out.finish(err)
	case err := <-childDone:
		cancel()
		runErr := <-tunnelDone
		if ctx.Err() != nil {
			return out.finish(runErr) // interrupted: the command only followed
		}
		return out.stopped("command-exited", nil, exitCode(err))
	}
}

// exitCode is the exit status of a finished command; 1 if a signal ended it.
func exitCode(err error) int {
	var ee *exec.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &ee) && ee.ExitCode() >= 0:
		return ee.ExitCode()
	default:
		return 1
	}
}

// relayBase finds the relay: --relay (envDefaults already applies
// $TSRELAY_RELAY), else https://tsrelay.<MagicDNS suffix> from the local
// Tailscale.
func relayBase(ctx context.Context, relayFlag string, d deps) (string, error) {
	status := d.Status
	if status == nil {
		status = (&local.Client{}).StatusWithoutPeers
	}
	base, err := client.RelayURL(ctx, relayFlag, status)
	switch {
	case errors.Is(err, client.ErrNoMagicDNS):
		return "", &cliError{Title: "Can't tell which relay to use", Code: "no_relay", err: err,
			Reason: "tsrelay finds the relay at https://tsrelay.<your tailnet>, but this tailnet has no MagicDNS name.",
			Fixes:  []string{"Name the relay with --relay https://<relay host>, or set TSRELAY_RELAY"}}
	case err != nil:
		return "", &cliError{Title: "Can't ask Tailscale for your tailnet", Code: "tailscale_unavailable", err: err,
			Reason: "tsrelay finds the relay by your tailnet's name, which the local Tailscale didn't give: " + sentence(errors.Unwrap(err).Error()),
			Fixes:  []string{"tailscale up", "Name the relay with --relay https://tsrelay.<your tailnet>.ts.net"}}
	}
	return base, nil
}

// newRelayAPI returns a client for the relay's API; see relayBase. It
// warns once when the relay reports a version other than this CLI's.
func (c *cli) newRelayAPI(ctx context.Context) (*client.API, error) {
	base, err := relayBase(ctx, c.relay, c.d)
	if err != nil {
		return nil, err
	}
	hc := c.d.HTTP
	if hc == nil {
		hc = tunnel.NewHTTPClient(nil)
	}
	next := hc.Transport
	if next == nil {
		next = http.DefaultTransport
	}
	checked := *hc
	checked.Transport = &versionCheck{next: next, seen: func(relay string) {
		c.versionWarned.Do(func() {
			c.version.Store(&versionSeen{base: base, relay: relay})
			c.warnVersion(base, relay)
		})
	}}
	c.base = base
	return &client.API{BaseURL: base, HTTP: &checked}, nil
}

// ensureHook reuses the caller's hook at the wanted path or creates one. An
// explicit name wins over the remembered path. A remembered path that can no
// longer be created (e.g. another hook now overlaps it) falls back to a new
// random path; an explicit name never does. created reports whether this
// call made a new hook rather than reusing one that already existed.
func ensureHook(ctx context.Context, c *client.API, explicit, remembered string, req hooks.CreateRequest) (h api.Hook, created bool, err error) {
	want := explicit
	if want == "" {
		want = remembered
	}
	if want != "" {
		existing, err := c.ListHooks(ctx)
		if err != nil {
			return api.Hook{}, false, err
		}
		for _, h := range existing {
			if h.Path != want {
				continue
			}
			switch {
			case req.Channel != "" && h.Channel != req.Channel:
				return api.Hook{}, false, &cliError{Title: fmt.Sprintf("Your hook %q is in channel %q, not %q", h.Path, h.Channel, req.Channel), Code: "hook_mismatch",
					Reason: "tsrelay http reuses the hook at this path, and won't move it to another channel on its own.",
					Fixes:  []string{fmt.Sprintf("tsrelay hooks update %s --channel %s", h.Path, req.Channel), "tsrelay hooks delete " + h.Path, "Pick another path with --name"}}
			case !req.Ephemeral && h.Ephemeral:
				return api.Hook{}, false, &cliError{Title: fmt.Sprintf("Your hook %q is ephemeral", h.Path), Code: "hook_mismatch",
					Reason: "--persistent asks for a hook that outlives tsrelay, and tsrelay http won't change the hook it reuses on its own.",
					Fixes:  []string{fmt.Sprintf("tsrelay hooks update %s --ephemeral=false", h.Path), "tsrelay hooks delete " + h.Path, "Pick another path with --name"}}
			}
			return h, false, nil
		}
	}
	req.Path = want
	h, err = c.CreateHook(ctx, req)
	var p *problem.Problem
	if err != nil && explicit == "" && remembered != "" && errors.As(err, &p) && p.Status == 422 {
		req.Path = ""
		h, err = c.CreateHook(ctx, req)
	}
	return h, err == nil, err
}

// isNotFound reports whether err is the relay's 404, which a tunnel dial
// gets for a hook that no longer exists.
func isNotFound(err error) bool {
	var p *problem.Problem
	return errors.As(err, &p) && p.Status == http.StatusNotFound
}

// callHandler delivers calls to target and reports them on out.
func callHandler(target *url.URL, insecure bool, out *output) http.Handler {
	return client.LogCalls(client.NewForwarder(target, client.Options{
		Insecure:        insecure,
		OnUpstreamError: out.upstreamError,
	}), out.call)
}

// openState loads the CLI's state: d.StatePath in tests, else
// client.DefaultStatePath, falling back to the pre-§15 location until the
// next write moves it.
func openState(d deps) (*client.State, error) {
	path, legacy := d.StatePath, ""
	if path == "" {
		var err error
		if path, err = client.DefaultStatePath(); err != nil {
			return nil, err
		}
		legacy, _ = client.LegacyStatePath()
	}
	s, err := client.LoadState(path, legacy)
	if err != nil {
		return nil, &cliError{Title: "Can't read tsrelay's remembered hooks", Code: "state_unreadable", err: err,
			Reason: fmt.Sprintf("tsrelay remembers hooks per directory in %s: %s", path, sentence(err.Error())),
			Fixes:  []string{"Repair the file, or move it aside to start over: mv " + path + " " + path + ".bak"}}
	}
	return s, nil
}
