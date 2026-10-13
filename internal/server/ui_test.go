package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Radiergummi/tsrelay/internal/authz"
	"github.com/Radiergummi/tsrelay/internal/client"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/identify"
	"github.com/Radiergummi/tsrelay/internal/inspect"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
	"github.com/Radiergummi/tsrelay/internal/ui"
)

var (
	uiAdmin = identify.User("ops-box", "ops@example.com", 3, `{"roles":["admin"]}`)
	uiOther = identify.User("desk", "other@example.com", 2, `{"roles":["receiver"]}`)
)

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

// apiAs is an API client acting as id.
func (e *env) apiAs(id authz.Identity) *client.API {
	return &client.API{BaseURL: e.base, HTTP: tunnel.NewHTTPClient(e.fake.Dial(id))}
}

var (
	scriptTag = regexp.MustCompile(`<script[^>]*>`)
	eventAttr = regexp.MustCompile(`\son[a-z]+\s*=`)
)

// assertInert fails if a page contains anything the CSP would block: inline
// script, event-handler attributes or inline style.
func assertInert(t *testing.T, body string) {
	t.Helper()
	for _, tag := range scriptTag.FindAllString(body, -1) {
		if !strings.Contains(tag, ` src="/static/`) {
			t.Errorf("inline script: %s", tag)
		}
	}
	if eventAttr.MatchString(body) || strings.Contains(body, " style=") || strings.Contains(body, "<style") {
		t.Errorf("page has an event handler or inline style:\n%s", body)
	}
}

func TestUISecurityHeaders(t *testing.T) {
	e := newEnv(t, Config{CapName: testCap})
	for _, path := range []string{"/", "/static/app.css", "/static/app.js", "/no/such/page"} {
		resp, _ := e.page(receiver, path)
		h := resp.Header
		if h.Get("Content-Security-Policy") != ui.CSP || h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Referrer-Policy") != "same-origin" {
			t.Errorf("%s: security headers = %v", path, h)
		}
	}
	resp, body := e.page(receiver, "/no/such/page")
	if resp.StatusCode != 404 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") || !strings.Contains(body, "There is no page here") {
		t.Errorf("unknown page = %d %s:\n%s", resp.StatusCode, resp.Header.Get("Content-Type"), body)
	}
	assertInert(t, body)
	if resp, _ := e.page(receiver, "/api/v1/nope"); resp.StatusCode != 404 || resp.Header.Get("Content-Type") != "application/problem+json" {
		t.Errorf("unknown API path = %d %s, want a JSON problem", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if resp, _ := e.page(receiver, "/static/app.js"); !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/javascript") {
		t.Errorf("app.js Content-Type = %s", resp.Header.Get("Content-Type"))
	}
}

// Callers who cannot receive callbacks learn why, and how to get access.
func TestUINoAccess(t *testing.T) {
	e := newEnv(t, Config{CapName: testCap})
	for name, tc := range map[string]struct {
		id   authz.Identity
		want []string
	}{
		"no grant":    {identify.User("box", "nobody@example.com", 4), []string{"grants you no access", testCap, "nobody@example.com", "receiver"}},
		"sender only": {identify.User("ci-box", "ci@example.com", 5, `{"roles":["sender"]}`), []string{"the sender role, but not", testCap, "ci@example.com"}},
		"tagged":      {sender, []string{"Tagged nodes cannot own hooks", "tag:prod-worker"}},
	} {
		resp, body := e.page(tc.id, "/")
		if resp.StatusCode != 403 {
			t.Errorf("%s: status %d", name, resp.StatusCode)
		}
		for _, w := range tc.want {
			if !strings.Contains(body, w) {
				t.Errorf("%s: page lacks %q:\n%s", name, w, body)
			}
		}
		assertInert(t, body)
	}
	e.fake.Fail(io.ErrUnexpectedEOF)
	if resp, body := e.page(receiver, "/"); resp.StatusCode != 403 || !strings.Contains(body, "could not identify") {
		t.Errorf("unidentified caller = %d:\n%s", resp.StatusCode, body)
	}
}

func TestUIHooksList(t *testing.T) {
	e := newEnv(t, Config{CapName: testCap})
	ctx := context.Background()
	mine, err := e.api.CreateHook(ctx, hooks.CreateRequest{Path: "cb"})
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := e.apiAs(uiOther).CreateHook(ctx, hooks.CreateRequest{Path: "theirs"})
	if err != nil {
		t.Fatal(err)
	}
	local := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer local.Close()
	defer e.cli(mine.ID, local.URL)()
	e.srv.inspect.Add(inspect.Call{ID: "c_1", HookID: hooks.ID(mine.ID), Owner: receiver.UserID, ReceivedAt: time.Now(),
		Kind: inspect.KindSender, Method: "POST", Path: "/done", Outcome: inspect.OutcomeOK, Status: 202, Attempts: []inspect.Attempt{}})

	resp, body := e.page(receiver, "/")
	if resp.StatusCode != 200 {
		t.Fatalf("status %d:\n%s", resp.StatusCode, body)
	}
	for _, want := range []string{mine.URL, `href="/hooks/` + mine.ID + `"`, "attached", local.URL, "POST /done", "202", "data-live"} {
		if !strings.Contains(body, want) {
			t.Errorf("hooks page lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, theirs.URL) {
		t.Error("the hooks page lists another user's hook")
	}
	assertInert(t, body)
}
