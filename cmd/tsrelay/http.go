package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Radiergummi/tsrelay/internal/api"
	"github.com/Radiergummi/tsrelay/internal/client"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/problem"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
	"tailscale.com/client/local"
)

// childStopTimeout is how long a command started after "--" gets to exit
// after SIGTERM before it is killed.
const childStopTimeout = 5 * time.Second

func httpCmd(ctx context.Context, args []string, stdout, stderr io.Writer, d deps) int {
	fs := flag.NewFlagSet("tsrelay http", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: tsrelay http <port|url> [flags] [-- command args...]")
		fs.PrintDefaults()
	}
	relayFlag := fs.String("relay", "", "relay URL or host (default: https://tsrelay.<your tailnet>)")
	name := fs.String("name", "", "hook path below your ~user prefix (default: remembered, else three random words)")
	channel := fs.String("channel", "", `hook channel (default: "default")`)
	persistent := fs.Bool("persistent", false, "keep the hook after this command exits")
	insecure := fs.Bool("insecure", false, "skip TLS certificate verification for an https:// target")
	asJSON := fs.Bool("json", false, "print one JSON object per event on stdout")
	own, command := splitCommand(args)
	rest, err := parseArgs(fs, own)
	if err != nil {
		return 2
	}
	if err := applyEnv(fs, "TSRELAY_"); err != nil {
		fmt.Fprintln(stderr, "tsrelay http:", err)
		return 2
	}
	if len(rest) != 1 {
		fs.Usage()
		return 2
	}
	target, err := client.ParseTarget(rest[0])
	if err != nil {
		fmt.Fprintln(stderr, "tsrelay http:", err)
		return 2
	}
	out := newOutput(stdout, stderr, *asJSON)

	relayAPI, err := newRelayAPI(ctx, *relayFlag, d)
	if err != nil {
		return out.fail(err)
	}
	out.inspectBase = relayAPI.BaseURL
	state, err := openState(d)
	if err != nil {
		return out.fail(err)
	}
	cwd, _ := os.Getwd()
	key := client.StateKey(relayAPI.BaseURL, cwd, target)
	req := hooks.CreateRequest{Channel: *channel, Ephemeral: !*persistent}
	hook, err := ensureHook(ctx, relayAPI, *name, state.Path(key), req)
	if err != nil {
		return out.fail(err)
	}
	if err := state.Remember(key, hook.Path, hook.URL); err != nil {
		out.text(stderr, "tsrelay: warning: could not remember the hook path: %v", err)
	}
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
			h, err := ensureHook(ctx, relayAPI, old.Path, "", req)
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
		OnState: out.onState([]string{hook.URL}, target.String(), hook.Effective.HoldTimeout.Std()),
		OnControl: func(c tunnel.Control) {
			mu.Lock()
			defer mu.Unlock()
			seqs.observe(c)
			if c.Type == tunnel.ControlHookUpdated {
				for _, h := range c.Hooks {
					if h.ID != hook.ID || !seqs.fresh(h.ID, c.Seq) || h.URL == hook.URL {
						continue
					}
					// Only the path can change the URL; the prefix before it stays.
					hook.Path = strings.TrimPrefix(h.URL, strings.TrimSuffix(hook.URL, hook.Path))
					hook.URL = h.URL
					if err := state.Remember(key, hook.Path, hook.URL); err != nil {
						out.text(stderr, "tsrelay: warning: could not remember the hook path: %v", err)
					}
					out.url(hook, false)
				}
			}
			out.dropped(c, func(string) string { return hook.URL })
		},
	}
	return runTunnel(ctx, tc, command, startURL, out)
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
			return out.fail(fmt.Errorf("starting %s: %w", command[0], err))
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

// relayBase finds the relay: --relay, then $TSRELAY_RELAY, then
// https://tsrelay.<MagicDNS suffix> from the local Tailscale.
func relayBase(ctx context.Context, relayFlag string, d deps) (string, error) {
	if relayFlag == "" {
		relayFlag = os.Getenv("TSRELAY_RELAY")
	}
	status := d.Status
	if status == nil {
		status = (&local.Client{}).StatusWithoutPeers
	}
	return client.RelayURL(ctx, relayFlag, status)
}

// newRelayAPI returns a client for the relay's API; see relayBase.
func newRelayAPI(ctx context.Context, relayFlag string, d deps) (*client.API, error) {
	base, err := relayBase(ctx, relayFlag, d)
	if err != nil {
		return nil, err
	}
	hc := d.HTTP
	if hc == nil {
		hc = tunnel.NewHTTPClient(nil)
	}
	return &client.API{BaseURL: base, HTTP: hc}, nil
}

// ensureHook reuses the caller's hook at the wanted path or creates one. An
// explicit name wins over the remembered path. A remembered path that can no
// longer be created (e.g. another hook now overlaps it) falls back to a new
// random path; an explicit name never does.
func ensureHook(ctx context.Context, c *client.API, explicit, remembered string, req hooks.CreateRequest) (api.Hook, error) {
	want := explicit
	if want == "" {
		want = remembered
	}
	if want != "" {
		existing, err := c.ListHooks(ctx)
		if err != nil {
			return api.Hook{}, err
		}
		for _, h := range existing {
			if h.Path != want {
				continue
			}
			switch {
			case req.Channel != "" && h.Channel != req.Channel:
				return api.Hook{}, fmt.Errorf("your hook %q is in channel %q, not %q; delete it or pick another --name", h.Path, h.Channel, req.Channel)
			case !req.Ephemeral && h.Ephemeral:
				return api.Hook{}, fmt.Errorf("your hook %q is ephemeral, but --persistent was given; delete it or pick another --name", h.Path)
			}
			return h, nil
		}
	}
	req.Path = want
	h, err := c.CreateHook(ctx, req)
	var p *problem.Problem
	if err != nil && explicit == "" && remembered != "" && errors.As(err, &p) && p.Status == 422 {
		req.Path = ""
		return c.CreateHook(ctx, req)
	}
	return h, err
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
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return s, nil
}
