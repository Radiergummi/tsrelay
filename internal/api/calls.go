package api

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/Radiergummi/tsrelay/internal/authz"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/inspect"
	"github.com/Radiergummi/tsrelay/internal/problem"
	"github.com/Radiergummi/tsrelay/internal/relay"
	"golang.org/x/net/http/httpguts"
)

// Owns reports whether id owns h.
func Owns(id authz.Identity, h hooks.Hook) bool { return id.IsUser() && h.Owner == id.UserID }

// Visible reports whether id may see h: its owner always, admins as metadata.
func Visible(id authz.Identity, h hooks.Hook) bool {
	return Owns(id, h) || id.Grants.Has(authz.RoleAdmin)
}

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
// hook get its metadata only (DESIGN.md §9.2), and only owners learn
// whether it can be replayed.
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
	out := CallJSON{Call: c}
	if Owns(id, h) {
		out.Replay = &Replay{Allowed: true}
		if p := ReplayRefusal(c); p != nil {
			out.Replay = &Replay{Reason: p.Detail}
		}
	} else {
		out.Call = c.Metadata()
	}
	writeJSON(w, http.StatusOK, out)
}

// CallJSON is a call as GET /api/v1/hooks/{id}/calls/{callID} shows it.
// Replay is set for the hook's owner only: nobody else may replay a call.
type CallJSON struct {
	inspect.Call
	Replay *Replay `json:"replay,omitempty"`
}

// Replay says whether the owner can replay a call, and if not, why
// (ReplayRefusal's detail).
type Replay struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason"`
}

// TestRequest is the body of POST /api/v1/hooks/{id}/test.
type TestRequest struct {
	Method  string      `json:"method"` // default POST
	Path    string      `json:"path"`   // below the hook's URL, query allowed; default "/"
	Headers http.Header `json:"headers"`
	Body    string      `json:"body"`
}

// ownedHook returns the hook named in the path if id owns it. It answers
// 404 for hooks id cannot see, and 403 when an admin names another user's
// hook: admins see metadata only and cannot act on calls (DESIGN.md §5.2).
func (a *API) ownedHook(w http.ResponseWriter, r *http.Request, id authz.Identity) (hooks.Hook, bool) {
	h, ok := a.visibleHook(r, id)
	switch {
	case !ok:
		problem.New(http.StatusNotFound, problem.NotFound, "hook not found").Write(w)
	case !Owns(id, h):
		problem.New(http.StatusForbidden, problem.Forbidden, "only the hook's owner can replay calls or send test requests").Write(w)
	default:
		return h, true
	}
	return hooks.Hook{}, false
}

// ReplayRefusal says why c cannot be replayed (DESIGN.md §9.3), or returns
// nil if it can.
func ReplayRefusal(c inspect.Call) *problem.Problem {
	switch {
	case c.Outcome == inspect.OutcomePending:
		return problem.New(http.StatusConflict, problem.Conflict, "this call is still being delivered, so it cannot be replayed yet")
	case c.Outcome == problem.HookNotFound:
		return problem.New(http.StatusConflict, problem.Conflict, "this call was refused by channel and never delivered, so it cannot be replayed")
	case c.QueryTruncated:
		return problem.New(http.StatusConflict, problem.Conflict, "this call's query string was not captured in full, so it cannot be replayed")
	case !c.RequestBody.Complete():
		return problem.New(http.StatusConflict, problem.Conflict, "this call's request body was not captured in full, so it cannot be replayed")
	}
	return nil
}

// replayCall re-sends a captured call to the hook's current tunnel
// (DESIGN.md §9.3). Redacted headers were never captured, so they are left
// out. The local app is told about the original sender. A call refused by
// channel was never delivered, so it cannot be replayed either.
func (a *API) replayCall(w http.ResponseWriter, r *http.Request, id authz.Identity) {
	h, ok := a.ownedHook(w, r, id)
	if !ok {
		return
	}
	orig, ok := a.Inspect.Get(r.PathValue("callID"))
	if !ok || orig.HookID != h.ID {
		problem.New(http.StatusNotFound, problem.NotFound, "call not found").Write(w)
		return
	}
	if p := ReplayRefusal(orig); p != nil {
		p.Write(w)
		return
	}
	a.inject(w, r, relay.Injection{
		Hook: h, Kind: inspect.KindReplay, ReplayOf: orig.ID,
		Sender: authz.Identity{NodeName: orig.Sender.Node, Tags: orig.Sender.Tags, Login: orig.Sender.Login},
		Method: orig.Method, Path: orig.Path, Query: orig.Query, Header: orig.RequestHeaders, Body: orig.RequestBody.Body,
		From: r,
	})
}

// testCall sends a request the owner wrote through the normal pipeline,
// as a call of kind test (DESIGN.md §9.3).
func (a *API) testCall(w http.ResponseWriter, r *http.Request, id authz.Identity) {
	h, ok := a.ownedHook(w, r, id)
	if !ok {
		return
	}
	var req TestRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		problem.New(http.StatusBadRequest, problem.BadRequest, "invalid JSON: "+err.Error()).Write(w)
		return
	}
	var errs hooks.ValidationError
	method := cmp.Or(req.Method, http.MethodPost)
	if !httpguts.ValidHeaderFieldName(method) { // a method is a token, like a header name
		errs = append(errs, hooks.FieldError{Field: "method", Message: fmt.Sprintf("%q is not a valid HTTP method", method)})
	}
	u, err := url.Parse(cmp.Or(req.Path, "/"))
	if err != nil || u.Scheme != "" || u.Host != "" || !strings.HasPrefix(u.Path, "/") {
		errs = append(errs, hooks.FieldError{Field: "path", Message: "must be a path starting with /, optionally with a query string"})
	}
	for k, vs := range req.Headers {
		valid := httpguts.ValidHeaderFieldName(k)
		for _, v := range vs {
			valid = valid && httpguts.ValidHeaderFieldValue(v)
		}
		if !valid {
			errs = append(errs, hooks.FieldError{Field: "headers." + k, Message: "is not a valid header"})
		}
	}
	if len(errs) > 0 {
		p := problem.New(http.StatusUnprocessableEntity, problem.Invalid, errs.Error())
		p.Errors = errs
		p.Write(w)
		return
	}
	a.inject(w, r, relay.Injection{
		Hook: h, Kind: inspect.KindTest, Sender: id,
		Method: method, Path: u.EscapedPath(), Query: u.RawQuery, Header: req.Headers, Body: []byte(req.Body),
		From: r,
	})
}

// inject delivers a replay or test call and answers with the finished call.
func (a *API) inject(w http.ResponseWriter, r *http.Request, in relay.Injection) {
	c, err := a.Relay.Inject(r.Context(), in)
	switch {
	case errors.Is(err, relay.ErrNotRouted):
		problem.New(http.StatusNotFound, problem.NotFound, "hook not found").Write(w)
	case err != nil:
		a.Audit.Slog().Error("delivering a replay or test request failed", "error", err)
		problem.New(http.StatusInternalServerError, problem.Internal, "could not deliver the call").Write(w)
	default:
		writeJSON(w, http.StatusCreated, c)
	}
}
