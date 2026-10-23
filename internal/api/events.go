package api

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/Radiergummi/tsrelay/internal/access"
	"github.com/Radiergummi/tsrelay/internal/authz"
	"github.com/Radiergummi/tsrelay/internal/events"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/problem"
	"tailscale.com/tailcfg"
)

// eventPing is how often an idle event stream sends a comment, so that
// browsers and proxies keep the connection open.
const eventPing = 25 * time.Second

// eventMaxAge is how long an event stream lasts. Streams identify their
// viewer again every ReidentifyEvery; this is the backstop for when that is
// off, after which EventSource reconnects and the caller is authorized again.
var eventMaxAge = 5 * time.Minute

// streamIdentifyTimeout bounds identifying a stream's viewer again, well
// under eventPing: the stream sends nothing while it waits.
var streamIdentifyTimeout = 5 * time.Second

// defaultStreamsPerNode is how many event streams one node may hold open
// when StreamsPerNode is not set.
const defaultStreamsPerNode = 8

// hooksStream streams changes to the caller's hooks and their calls, as
// GET /hooks asked for as events. Admins may add ?all=true for every hook
// in their channels; summaries are metadata only, so that reveals nothing
// more.
func (a *API) hooksStream(w http.ResponseWriter, r *http.Request, id authz.Identity) {
	all, ok := queryBool(w, r, "all")
	if !ok {
		return
	}
	all = all && access.IsAdmin(id)
	a.stream(w, r, id, "", func(v authz.Identity, e events.Event) bool {
		lvl := access.View(v, access.Hook{Owner: e.Owner, Channel: e.Channel})
		if all {
			return lvl != access.None
		}
		return lvl == access.Owner
	})
}

// hookStream streams changes to one hook and its calls.
func (a *API) hookStream(w http.ResponseWriter, r *http.Request, id authz.Identity) {
	h, _, ok := a.visibleHook(r, id)
	if !ok {
		problem.New(http.StatusNotFound, problem.NotFound, "hook not found").Write(w)
		return
	}
	a.stream(w, r, id, h.ID, func(v authz.Identity, e events.Event) bool { return e.HookID == string(h.ID) && sees(v, e) })
}

// sees reports whether viewer v may see the hook e is about, as e describes
// it: the hook may have been in another channel when e was published.
func sees(v authz.Identity, e events.Event) bool {
	return access.View(v, access.Hook{Owner: e.Owner, Channel: e.Channel}) != access.None
}

// callsStream streams one hook's calls.
func (a *API) callsStream(w http.ResponseWriter, r *http.Request, id authz.Identity) {
	h, _, ok := a.visibleHook(r, id)
	if !ok {
		problem.New(http.StatusNotFound, problem.NotFound, "hook not found").Write(w)
		return
	}
	a.stream(w, r, id, h.ID, func(v authz.Identity, e events.Event) bool {
		return e.Type == events.TypeCall && e.HookID == string(h.ID) && sees(v, e)
	})
}

// stream sends the events match accepts for the viewer as server-sent
// events (DESIGN.md §10.1). "call" events carry a call summary, "hook"
// events the ID of a hook that changed. The stream ends when the client
// falls behind or the relay stops; EventSource then reconnects, and the
// client refetches. It also ends after eventMaxAge, and, if it streams one
// hook, once that hook is gone or no longer visible to the viewer.
//
// Every ReidentifyEvery the viewer is identified again, and match sees the
// new identity; the stream ends once the viewer holds no role. If
// identification fails, the last identity stays: never more than it allowed.
func (a *API) stream(w http.ResponseWriter, r *http.Request, id authz.Identity, hook hooks.ID, match func(authz.Identity, events.Event) bool) {
	if !access.MayStream(id) {
		problem.New(http.StatusForbidden, problem.Forbidden, "event streams need a relay role").Write(w)
		return
	}
	if !a.takeStream(id.NodeID) {
		problem.New(http.StatusTooManyRequests, problem.StreamLimit, "this node has too many event streams open").Write(w)
		return
	}
	defer a.releaseStream(id.NodeID)
	// The hub calls the subscription's match from Publish, so the viewer
	// is shared with it atomically.
	var viewer atomic.Pointer[authz.Identity]
	viewer.Store(&id)
	ownHook := func(e events.Event) bool {
		return hook != "" && e.Type == events.TypeHook && e.HookID == string(hook)
	}
	ch, cancel := a.Events.Subscribe(func(e events.Event) bool {
		return match(*viewer.Load(), e) || ownHook(e)
	})
	defer cancel()
	maxAge := time.NewTimer(eventMaxAge)
	defer maxAge.Stop()
	var reidentify <-chan time.Time
	if a.ReidentifyEvery > 0 {
		t := time.NewTicker(a.ReidentifyEvery)
		defer t.Stop()
		reidentify = t.C
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	io.WriteString(w, ": connected\n\n")
	if rc.Flush() != nil {
		return
	}
	ping := time.NewTicker(eventPing)
	defer ping.Stop()
	for {
		select {
		case e, ok := <-ch:
			if !ok {
				return // fell behind, or the relay is stopping
			}
			v := *viewer.Load()
			if ownHook(e) {
				if _, _, ok := a.VisibleHook(hook, v); !ok {
					return
				}
			}
			// The viewer may have changed since the event was queued.
			if !match(v, e) {
				continue
			}
			b, err := json.Marshal(e.Data)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.Type, b)
		case <-reidentify:
			ctx, cancel := context.WithTimeout(r.Context(), streamIdentifyTimeout)
			v, err := a.Identifier.Identify(ctx, r.RemoteAddr)
			cancel()
			if err != nil {
				continue
			}
			viewer.Store(&v)
			if !access.MayStream(v) {
				return
			}
			if hook != "" {
				if _, _, ok := a.VisibleHook(hook, v); !ok {
					return
				}
			}
			continue
		case <-ping.C:
			io.WriteString(w, ": ping\n\n")
		case <-maxAge.C:
			return
		case <-r.Context().Done():
			return
		}
		if rc.Flush() != nil {
			return
		}
	}
}

// takeStream counts one more event stream for node, unless it has as many
// open as it may.
func (a *API) takeStream(node tailcfg.StableNodeID) bool {
	a.streamsMu.Lock()
	defer a.streamsMu.Unlock()
	if a.streams[node] >= cmp.Or(a.StreamsPerNode, defaultStreamsPerNode) {
		return false
	}
	if a.streams == nil {
		a.streams = map[tailcfg.StableNodeID]int{}
	}
	a.streams[node]++
	return true
}

func (a *API) releaseStream(node tailcfg.StableNodeID) {
	a.streamsMu.Lock()
	defer a.streamsMu.Unlock()
	if a.streams[node]--; a.streams[node] <= 0 {
		delete(a.streams, node)
	}
}
