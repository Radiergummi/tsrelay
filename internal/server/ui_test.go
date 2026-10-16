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

// page GETs path as id the way a browser does, without following
// redirects, and returns the response with its body read into a string.
func (e *env) page(id authz.Identity, path string) (*http.Response, string) {
	e.t.Helper()
	c := e.fake.Client(id)
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, _ := http.NewRequest("GET", e.base+path, nil)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	resp, err := c.Do(req)
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
	for _, path := range []string{"/hooks", "/hooks/new", "/hooks/" + h.ID, "/hooks/" + h.ID + "/edit", "/hooks/" + h.ID + "/calls",
		"/hooks/" + h.ID + "/calls/c_1", "/admin", "/inspect/c_gone", "/no/such/page"} {
		resp, _ := e.page(receiver, path)
		hd := resp.Header
		if !strings.HasPrefix(hd.Get("Content-Type"), "text/html") || hd.Get("Content-Security-Policy") != ui.CSP ||
			hd.Get("X-Content-Type-Options") != "nosniff" || hd.Get("Referrer-Policy") != "same-origin" {
			t.Errorf("%s = %d %v", path, resp.StatusCode, hd)
		}
	}
	// The app routes only bare URLs, so a .html extension is redirected away.
	for path, want := range map[string]string{"/hooks.html?all=true": "/hooks?all=true", "/admin.html": "/admin"} {
		if resp, _ := e.page(receiver, path); resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != want {
			t.Errorf("%s = %d → %q, want a redirect to %s", path, resp.StatusCode, resp.Header.Get("Location"), want)
		}
	}
	if resp, _ := e.page(receiver, "/nope.json"); resp.StatusCode != 404 || resp.Header.Get("Content-Type") != "application/problem+json" {
		t.Errorf("unknown path as JSON = %d %s, want a JSON problem", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if resp, _ := e.page(receiver, "/"); resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/hooks" {
		t.Errorf("/ for a browser = %d → %q, want a redirect to /hooks", resp.StatusCode, resp.Header.Get("Location"))
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

// The relay names its version on everything but callbacks, whose
// responses stay exactly what the receiver's app returned.
func TestRelayVersionHeader(t *testing.T) {
	e := newEnv(t, Config{CapName: testCap, Version: "v0.3.0"})
	for _, path := range []string{"/", "/hooks", "/nope", "/cli", "/install.sh", "/download/linux/amd64"} {
		if resp, _ := e.page(receiver, path); resp.Header.Get(api.VersionHeader) != "v0.3.0" {
			t.Errorf("%s: %s = %q", path, api.VersionHeader, resp.Header.Get(api.VersionHeader))
		}
	}
	resp := e.send(api.Hook{URL: e.base + "/~nobody/none"}, "", nil, nil)
	resp.Body.Close()
	if v := resp.Header.Get(api.VersionHeader); v != "" {
		t.Errorf("a callback response carries %s: %q", api.VersionHeader, v)
	}
}

func TestRelayVersionDefaultsToDev(t *testing.T) {
	e := newEnv(t, Config{CapName: testCap})
	if resp, _ := e.page(receiver, "/"); resp.Header.Get(api.VersionHeader) != "dev" {
		t.Errorf("%s = %q, want dev", api.VersionHeader, resp.Header.Get(api.VersionHeader))
	}
}
