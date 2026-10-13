package api

import (
	"net/http"

	"github.com/Radiergummi/tsrelay/internal/authz"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/problem"
)

// owns reports whether id owns h.
func owns(id authz.Identity, h hooks.Hook) bool { return id.IsUser() && h.Owner == id.UserID }

// listCalls returns the hook's call summaries, newest first. Summaries are
// metadata only, so owners and admins get the same list.
func (a *API) listCalls(w http.ResponseWriter, r *http.Request, id authz.Identity) {
	h, ok := a.visibleHook(r, id)
	if !ok {
		problem.New(http.StatusNotFound, problem.NotFound, "hook not found").Write(w)
		return
	}
	writeJSON(w, http.StatusOK, a.Inspect.List(h.ID))
}

// getCall returns one call with its attempts. Admins who do not own the
// hook get its metadata only (DESIGN.md §9.2).
func (a *API) getCall(w http.ResponseWriter, r *http.Request, id authz.Identity) {
	h, ok := a.visibleHook(r, id)
	if !ok {
		problem.New(http.StatusNotFound, problem.NotFound, "hook not found").Write(w)
		return
	}
	c, ok := a.Inspect.Get(r.PathValue("callID"))
	if !ok || c.HookID != h.ID {
		problem.New(http.StatusNotFound, problem.NotFound, "call not found").Write(w)
		return
	}
	if !owns(id, h) {
		c = c.Metadata()
	}
	writeJSON(w, http.StatusOK, c)
}
