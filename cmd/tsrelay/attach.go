package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"slices"
	"sync"

	"github.com/Radiergummi/tsrelay/internal/api"
	"github.com/Radiergummi/tsrelay/internal/client"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
)

// attachCmd serves existing hooks, named by ID or path, from one local
// target over one tunnel. Unlike tsrelay http it never creates hooks.
func attachCmd(ctx context.Context, args []string, stdout, stderr io.Writer, d deps) int {
	fs, relay := commandFlags("attach", "attach <id|path>... --forward-to <port|url> [flags]", stderr)
	forwardTo := fs.String("forward-to", "", "the local app to deliver calls to: a port or an http(s) URL (required)")
	insecure := fs.Bool("insecure", false, "skip TLS certificate verification for an https:// target")
	asJSON := fs.Bool("json", false, "print one JSON object per event on stdout")
	refs, err := parseArgs(fs, args)
	if err != nil {
		return 2
	}
	if len(refs) == 0 || *forwardTo == "" {
		fs.Usage()
		return 2
	}
	target, err := client.ParseTarget(*forwardTo)
	if err != nil {
		fmt.Fprintln(stderr, "tsrelay attach:", err)
		return 2
	}
	out := newOutput(stdout, stderr, *asJSON)

	relayAPI, err := newRelayAPI(ctx, *relay, d)
	if err != nil {
		return out.fail(err)
	}
	found, err := resolveHooks(ctx, relayAPI, refs)
	if err != nil {
		return out.fail(err)
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
		Hooks: ids,
		Dial: func(ctx context.Context, ids []string, a tunnel.Attach) (net.Conn, error) {
			return tunnel.Dial(ctx, relayAPI.HTTP, relayAPI.TunnelURL(a, ids...))
		},
		Handler: callHandler(target, *insecure, out),
		OnState: out.onState(urls, target.String(), hold),
		OnControl: func(c tunnel.Control) {
			mu.Lock() // control messages arrive concurrently
			defer mu.Unlock()
			if c.Type == tunnel.ControlAttached {
				// A new connection starts its own sequence, and attached
				// precedes any hook-updated on it; a sequence number
				// remembered from before a reconnect (e.g. a relay restart,
				// whose counter starts over at 0) must not suppress one
				// that is actually new.
				clear(seqs)
			}
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
	return out.finish(tc.Run(ctx))
}
