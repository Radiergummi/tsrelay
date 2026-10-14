package server

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Radiergummi/tsrelay/internal/api"
	"github.com/Radiergummi/tsrelay/internal/authz"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/identify"
	"github.com/Radiergummi/tsrelay/internal/inspect"
	"github.com/Radiergummi/tsrelay/internal/ui"
)

var uiOther = identify.User("desk", "other@example.com", 2, `{"roles":["receiver"]}`)

const testCap = "mazetti.me/cap/relay"

// page GETs path as id without following redirects, and returns the
// response with its body read into a string.
func (e *env) page(id authz.Identity, path string) (*http.Response, string) {
	e.t.Helper()
	c := e.fake.Client(id)
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := c.Get(e.base + path)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

// The UI's routes work whether or not web/dist is built: app routes get
// index.html or the placeholder, both text/html.
func TestUIRoutes(t *testing.T) {
	e := newEnv(t, Config{CapName: testCap})
	h := e.hook(hooks.CreateRequest{Path: "cb"})
	e.seed(h, inspect.Call{ID: "c_1"})

	resp, _ := e.page(receiver, "/inspect/c_1")
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/hooks/"+h.ID+"/calls/c_1" {
		t.Fatalf("inspect link = %d → %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	for _, tc := range []struct {
		id   authz.Identity
		path string
	}{{uiOther, "/inspect/c_1"}, {receiver, "/inspect/c_gone"}} {
		if resp, _ := e.page(tc.id, tc.path); resp.StatusCode != 404 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
			t.Errorf("%s as %s = %d %s, want the app with 404", tc.path, tc.id.Login, resp.StatusCode, resp.Header.Get("Content-Type"))
		}
	}
	for _, path := range []string{"/", "/hooks/" + h.ID, "/hooks/" + h.ID + "/calls/c_1", "/admin", "/inspect/c_gone", "/no/such/page"} {
		resp, _ := e.page(receiver, path)
		hd := resp.Header
		if !strings.HasPrefix(hd.Get("Content-Type"), "text/html") || hd.Get("Content-Security-Policy") != ui.CSP ||
			hd.Get("X-Content-Type-Options") != "nosniff" || hd.Get("Referrer-Policy") != "same-origin" {
			t.Errorf("%s = %d %v", path, resp.StatusCode, hd)
		}
	}
	if resp, _ := e.page(receiver, "/api/v1/nope"); resp.StatusCode != 404 || resp.Header.Get("Content-Type") != "application/problem+json" {
		t.Errorf("unknown API path = %d %s, want a JSON problem", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
}

// hook creates a hook for the test's receiver.
func (e *env) hook(req hooks.CreateRequest) api.Hook {
	e.t.Helper()
	h, err := e.api.CreateHook(context.Background(), req)
	if err != nil {
		e.t.Fatal(err)
	}
	return h
}

// seed records c as a finished call of h for h's owner, filling in what
// every call has.
func (e *env) seed(h api.Hook, c inspect.Call) {
	c.HookID, c.Owner = hooks.ID(h.ID), receiver.UserID
	if c.ReceivedAt.IsZero() {
		c.ReceivedAt = time.Now()
	}
	if c.Kind == "" {
		c.Kind = inspect.KindSender
	}
	if c.Method == "" {
		c.Method, c.Path = "POST", "/done"
	}
	if c.Outcome == "" {
		c.Outcome, c.Status = inspect.OutcomeOK, 200
	}
	if c.Attempts == nil {
		c.Attempts = []inspect.Attempt{}
	}
	e.srv.inspect.Add(c)
}

func full(s string) inspect.Capture {
	return inspect.Capture{Body: []byte(s), Size: int64(len(s)), Captured: true}
}
