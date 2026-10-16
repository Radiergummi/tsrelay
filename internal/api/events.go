package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Radiergummi/tsrelay/internal/authz"
	"github.com/Radiergummi/tsrelay/internal/events"
	"github.com/Radiergummi/tsrelay/internal/problem"
)

// eventPing is how often an idle event stream sends a comment, so that
// browsers and proxies keep the connection open.
const eventPing = 25 * time.Second

// hooksStream streams changes to the caller's hooks and their calls, as
// GET /hooks asked for as events. Admins may add ?all=true for every hook;
// summaries are metadata only, so that reveals nothing more.
func (a *API) hooksStream(w http.ResponseWriter, r *http.Request, id authz.Identity) {
	all := r.URL.Query().Get("all") == "true" && id.Grants.Has(authz.RoleAdmin)
	if !all && !id.IsUser() {
		problem.New(http.StatusForbidden, problem.Forbidden, "only users own hooks, so tagged nodes have no events").Write(w)
		return
	}
	a.stream(w, r, func(e events.Event) bool { return all || e.Owner == id.UserID })
}

// hookStream streams changes to one hook and its calls.
func (a *API) hookStream(w http.ResponseWriter, r *http.Request, id authz.Identity) {
	h, ok := a.visibleHook(r, id)
	if !ok {
		problem.New(http.StatusNotFound, problem.NotFound, "hook not found").Write(w)
		return
	}
	a.stream(w, r, func(e events.Event) bool { return e.HookID == string(h.ID) })
}

// callsStream streams one hook's calls.
func (a *API) callsStream(w http.ResponseWriter, r *http.Request, id authz.Identity) {
	h, ok := a.visibleHook(r, id)
	if !ok {
		problem.New(http.StatusNotFound, problem.NotFound, "hook not found").Write(w)
		return
	}
	a.stream(w, r, func(e events.Event) bool { return e.Type == events.TypeCall && e.HookID == string(h.ID) })
}

// stream sends the events match accepts as server-sent events (DESIGN.md
// §10.1). "call" events carry a call summary, "hook" events the ID of a
// hook that changed. The stream ends when the client falls behind or the
// relay stops; EventSource then reconnects, and the client refetches.
func (a *API) stream(w http.ResponseWriter, r *http.Request, match func(events.Event) bool) {
	ch, cancel := a.Events.Subscribe(match)
	defer cancel()
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
			b, err := json.Marshal(e.Data)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.Type, b)
		case <-ping.C:
			io.WriteString(w, ": ping\n\n")
		case <-r.Context().Done():
			return
		}
		if rc.Flush() != nil {
			return
		}
	}
}
