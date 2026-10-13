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

// eventStream streams the caller's call and hook changes as server-sent
// events (DESIGN.md §10.1). "call" events carry a call summary, "hook"
// events the ID of a hook that changed. Admins may add ?all=true for every
// hook; summaries are metadata only, so that reveals nothing more. The
// stream ends when the client falls behind or the relay stops; EventSource
// then reconnects, and the client refetches.
func (a *API) eventStream(w http.ResponseWriter, r *http.Request, id authz.Identity) {
	all := r.URL.Query().Get("all") == "true" && id.Grants.Has(authz.RoleAdmin)
	if !all && !id.IsUser() {
		problem.New(http.StatusForbidden, problem.Forbidden, "only users own hooks, so tagged nodes have no events").Write(w)
		return
	}
	ch, cancel := a.Events.Subscribe(func(e events.Event) bool { return all || e.Owner == id.UserID })
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
