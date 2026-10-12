package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/Radiergummi/tsrelay/internal/api"
	"github.com/Radiergummi/tsrelay/internal/client"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/problem"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
	"tailscale.com/client/local"
)

func httpCmd(ctx context.Context, args []string, stdout, stderr io.Writer, d deps) int {
	fs := flag.NewFlagSet("tsrelay http", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: tsrelay http <port|url> [flags]")
		fs.PrintDefaults()
	}
	relayFlag := fs.String("relay", "", "relay URL or host (default: https://tsrelay.<your tailnet>)")
	name := fs.String("name", "", "hook path below your ~user prefix (default: remembered, else three random words)")
	channel := fs.String("channel", "", `hook channel (default: "default")`)
	persistent := fs.Bool("persistent", false, "keep the hook after this command exits")
	insecure := fs.Bool("insecure", false, "skip TLS certificate verification for an https:// target")
	// Accept flags before and after the target, as people type both.
	if err := fs.Parse(args); err != nil {
		return 2
	}
	rest := fs.Args()
	if len(rest) > 0 {
		first := rest[0]
		if err := fs.Parse(rest[1:]); err != nil {
			return 2
		}
		rest = append([]string{first}, fs.Args()...)
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

	fail := func(err error) int {
		var p *problem.Problem
		if errors.As(err, &p) {
			fmt.Fprintf(stderr, "tsrelay: the relay refused: %s\n", p.Detail)
		} else {
			fmt.Fprintln(stderr, "tsrelay:", err)
		}
		return 1
	}

	status := d.Status
	if status == nil {
		status = (&local.Client{}).StatusWithoutPeers
	}
	relayURL, err := client.RelayURL(ctx, *relayFlag, status)
	if err != nil {
		return fail(err)
	}
	hc := d.HTTP
	if hc == nil {
		hc = tunnel.NewHTTPClient(nil)
	}
	relayAPI := &client.API{BaseURL: relayURL, HTTP: hc}

	state, err := openState(d)
	if err != nil {
		return fail(err)
	}
	cwd, _ := os.Getwd()
	key := client.StateKey(relayURL, cwd, target)

	req := hooks.CreateRequest{Channel: *channel, Ephemeral: !*persistent}
	hook, err := ensureHook(ctx, relayAPI, *name, state.Path(key), req)
	if err != nil {
		return fail(err)
	}
	if err := state.Remember(key, hook.Path, hook.URL); err != nil {
		fmt.Fprintf(stderr, "tsrelay: warning: could not remember the hook path: %v\n", err)
	}

	fmt.Fprintln(stdout, hook.URL)
	logCall := func(c client.Call) {
		fmt.Fprintf(stderr, "%s %s %s → %d  %s  %s\n", time.Now().Format("15:04:05"), c.Method, c.Path, c.Status,
			c.Duration.Round(time.Millisecond), c.Sender)
	}
	connectedOnce := false
	hookURL := hook.URL // stays the same when the hook is recreated
	var tc *tunnel.Client
	tc = &tunnel.Client{
		Hooks: []string{hook.ID},
		Dial: func(ctx context.Context, ids []string, a tunnel.Attach) (net.Conn, error) {
			conn, err := tunnel.Dial(ctx, hc, relayAPI.TunnelURL(a, ids...))
			if !isNotFound(err) {
				return conn, err
			}
			// The relay reaped the hook while this CLI was disconnected for
			// longer than the ephemeral grace period: recreate it at the
			// same path, so its URL keeps working.
			h, err := ensureHook(ctx, relayAPI, hook.Path, "", req)
			if err != nil {
				return nil, err
			}
			tc.Replace(hook.ID, h.ID)
			hook = h
			fmt.Fprintln(stderr, "tsrelay: the hook was removed while disconnected; recreated at the same URL.")
			return tunnel.Dial(ctx, hc, relayAPI.TunnelURL(a, h.ID))
		},
		OnControl: func(c tunnel.Control) {
			if msg, ok := dropMessage(c.Type, hookURL); ok {
				fmt.Fprintf(stderr, "tsrelay: %s; stopping.\n", msg)
			}
		},
		Handler: client.LogCalls(client.NewForwarder(target, client.Options{
			Insecure:        *insecure,
			OnUpstreamError: func(err error) { fmt.Fprintln(stderr, "tsrelay:", err) },
		}), logCall),
		OnState: func(connected bool, err error) {
			switch {
			case connected && !connectedOnce:
				connectedOnce = true
				fmt.Fprintf(stderr, "Forwarding %s → %s\n", hook.URL, target)
			case connected:
				fmt.Fprintln(stderr, "Tunnel restored.")
			default:
				fmt.Fprintf(stderr, "Tunnel lost (%v); reconnecting…\n", err)
			}
		},
	}
	switch err := tc.Run(ctx); {
	case errors.Is(err, tunnel.ErrNoHooks):
		return 1 // OnControl said why
	case err != nil:
		return fail(err)
	}
	return 0
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

// dropMessage explains a control message that ends delivery of the hook at
// hookURL to this CLI. ok is false for other control messages.
func dropMessage(t tunnel.ControlType, hookURL string) (msg string, ok bool) {
	switch t {
	case tunnel.ControlReplaced:
		return hookURL + " is now served by another tsrelay process", true
	case tunnel.ControlRevoked:
		return "your grants no longer allow serving " + hookURL, true
	case tunnel.ControlHookDeleted:
		return hookURL + " was deleted", true
	}
	return "", false
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
