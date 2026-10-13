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

	"github.com/Radiergummi/tsrelay/internal/api"
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

func TestUIHookPage(t *testing.T) {
	e := newEnv(t, Config{CapName: testCap})
	h, err := e.api.CreateHook(context.Background(), hooks.CreateRequest{Path: "cb", Description: "for <ci>",
		Settings: hooks.Settings{HoldTimeout: new(hooks.Duration(0))}})
	if err != nil {
		t.Fatal(err)
	}
	e.srv.inspect.Add(inspect.Call{ID: "c_1", HookID: hooks.ID(h.ID), Owner: receiver.UserID, ReceivedAt: time.Now(),
		Kind: inspect.KindSender, Method: "POST", Path: "/done", Query: "token=s3cr3t",
		Outcome: inspect.OutcomeOK, Status: 202, Attempts: []inspect.Attempt{}})

	resp, body := e.page(receiver, "/hooks/"+h.ID)
	if resp.StatusCode != 200 {
		t.Fatalf("owner: status %d:\n%s", resp.StatusCode, body)
	}
	for _, want := range []string{
		h.URL, "for &lt;ci&gt;", "no tunnel", "POST /done", `href="/hooks/` + h.ID + `/calls/c_1"`,
		"Hold timeout", "off", "relay default", `href="/hooks/` + h.ID + `/edit"`,
		`data-action="delete-hook"`, `id="hook-state" data-live data-hooks="` + h.ID + `"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("owner's hook page lacks %q", want)
		}
	}
	if strings.Contains(body, "s3cr3t") {
		t.Error("the call list shows a query string")
	}
	if strings.Contains(body, "data-all") {
		t.Error("the owner's hook page subscribes to every hook's events")
	}
	assertInert(t, body)

	// Admins see the hook as metadata, without controls. Their view
	// subscribes with ?all=true, since the event stream only delivers an
	// admin events for their own hooks otherwise.
	resp, body = e.page(uiAdmin, "/hooks/"+h.ID)
	if resp.StatusCode != 200 || !strings.Contains(body, "metadata only") || strings.Contains(body, "/edit") ||
		strings.Contains(body, "delete-hook") || !strings.Contains(body, "data-all") {
		t.Errorf("admin's hook page = %d:\n%s", resp.StatusCode, body)
	}
	// Anyone else, and unknown IDs, get a 404 page.
	for _, tc := range []struct {
		id   authz.Identity
		path string
	}{{uiOther, "/hooks/" + h.ID}, {receiver, "/hooks/h_nope"}} {
		if resp, body := e.page(tc.id, tc.path); resp.StatusCode != 404 || !strings.Contains(body, "There is no hook") {
			t.Errorf("%s as %s = %d", tc.path, tc.id.Login, resp.StatusCode)
		}
	}
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

func TestUICallPage(t *testing.T) {
	e := newEnv(t, Config{CapName: testCap})
	h, err := e.api.CreateHook(context.Background(), hooks.CreateRequest{Path: "cb"})
	if err != nil {
		t.Fatal(err)
	}
	local := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer local.Close()
	defer e.cli(h.ID, local.URL)()
	e.seed(h, inspect.Call{
		ID: "c_1", Query: "token=s3cr3t",
		RequestHeaders: http.Header{"Content-Type": {"application/json"}, "X-Evil": {`</pre><script>alert(1)</script>`}},
		Redacted:       []string{"Authorization"},
		RequestBody:    full(`{"html":"<script>alert(2)</script>"}`),
		Status:         201,
		Attempts: []inspect.Attempt{{ID: "a_1", Status: 201, TunnelID: "t_1", Node: "laptop",
			ResponseHeaders: http.Header{"X-Reply": {"secret-reply"}}, ResponseBody: full("created")}},
	})

	resp, body := e.page(receiver, "/hooks/"+h.ID+"/calls/c_1")
	if resp.StatusCode != 200 {
		t.Fatalf("owner: status %d:\n%s", resp.StatusCode, body)
	}
	for _, want := range []string{
		"token=s3cr3t", "X-Evil", "&lt;/pre&gt;&lt;script&gt;alert(1)&lt;/script&gt;", "Left out: Authorization",
		"secret-reply", "created", `data-action="replay"`, `data-action="toggle-json"`,
		"To the local target", local.URL + "/done?token=s3cr3t", "To the hook URL",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("owner's call page lacks %q", want)
		}
	}
	if strings.Contains(body, "<script>alert") {
		t.Error("captured markup was rendered as HTML")
	}
	assertInert(t, body)

	resp, body = e.page(uiAdmin, "/hooks/"+h.ID+"/calls/c_1")
	if resp.StatusCode != 200 || !strings.Contains(body, "metadata only") {
		t.Fatalf("admin: status %d:\n%s", resp.StatusCode, body)
	}
	for _, secret := range []string{"s3cr3t", "X-Evil", "alert(", "secret-reply", `data-action="replay"`, "curl"} {
		if strings.Contains(body, secret) {
			t.Errorf("admin's call page shows %q", secret)
		}
	}

	// Markup in the path and query is escaped too, for owner and admin alike.
	e.seed(h, inspect.Call{ID: "c_markup", Method: "POST", Path: `/x"'<script>alert(3)</script>`, Query: `q=<script>alert(4)</script>`})
	for _, id := range []authz.Identity{receiver, uiAdmin} {
		resp, body := e.page(id, "/hooks/"+h.ID+"/calls/c_markup")
		if resp.StatusCode != 200 {
			t.Fatalf("c_markup as %s: status %d:\n%s", id.Login, resp.StatusCode, body)
		}
		if strings.Contains(body, "<script>alert") {
			t.Errorf("c_markup as %s: captured markup in the path or query was rendered as HTML", id.Login)
		}
		assertInert(t, body)
	}

	for _, path := range []string{"/hooks/" + h.ID + "/calls/c_nope", "/hooks/h_nope/calls/c_1"} {
		if resp, _ := e.page(receiver, path); resp.StatusCode != 404 {
			t.Errorf("%s = %d, want 404", path, resp.StatusCode)
		}
	}
	if resp, _ := e.page(uiOther, "/hooks/"+h.ID+"/calls/c_1"); resp.StatusCode != 404 {
		t.Errorf("another user's call = %d, want 404", resp.StatusCode)
	}
}

// Bodies that cannot be shown or replayed in full say so.
func TestUICallBodies(t *testing.T) {
	e := newEnv(t, Config{CapName: testCap})
	h, err := e.api.CreateHook(context.Background(), hooks.CreateRequest{Path: "cb"})
	if err != nil {
		t.Fatal(err)
	}
	e.seed(h, inspect.Call{ID: "c_cut", RequestBody: inspect.Capture{Body: []byte("0123"), Size: 4096, Truncated: true, Captured: true}})
	e.seed(h, inspect.Call{ID: "c_bin", RequestBody: full("\xff\xfe\x00")})
	e.seed(h, inspect.Call{ID: "c_nul", RequestBody: full("a\x00b")})
	e.seed(h, inspect.Call{ID: "c_off", RequestBody: inspect.Capture{Size: 10}})
	e.seed(h, inspect.Call{ID: "c_empty", RequestBody: inspect.Capture{Captured: true}})
	e.seed(h, inspect.Call{ID: "c_refused", Outcome: "hook_not_found", Status: 404, RequestBody: inspect.Capture{Captured: true}})
	e.seed(h, inspect.Call{ID: "c_pending", Outcome: inspect.OutcomePending})
	for id, tc := range map[string]struct {
		want   []string
		replay bool
	}{
		"c_cut":     {[]string{"showing the first 4 B", "not captured in full"}, false},
		"c_bin":     {[]string{"binary body", "not offered as a curl command"}, true},
		"c_nul":     {[]string{"binary body", "not offered as a curl command"}, true},
		"c_off":     {[]string{"not captured: body capture is off"}, false},
		"c_empty":   {[]string{"No body."}, true},
		"c_refused": {[]string{"refused by channel", "No attempts"}, false},
		"c_pending": {[]string{"still being delivered, so it cannot be replayed yet"}, false},
	} {
		_, body := e.page(receiver, "/hooks/"+h.ID+"/calls/"+id)
		for _, w := range tc.want {
			if !strings.Contains(body, w) {
				t.Errorf("%s: page lacks %q", id, w)
			}
		}
		if got := strings.Contains(body, `data-action="replay"`); got != tc.replay {
			t.Errorf("%s: replay offered = %v, want %v", id, got, tc.replay)
		}
		if id == "c_pending" && strings.Contains(body, `id="curl-0"`) {
			t.Errorf("%s: page offers a curl command", id)
		}
	}
}

func TestUIInspectRedirect(t *testing.T) {
	e := newEnv(t, Config{CapName: testCap})
	h, err := e.api.CreateHook(context.Background(), hooks.CreateRequest{Path: "cb"})
	if err != nil {
		t.Fatal(err)
	}
	e.seed(h, inspect.Call{ID: "c_1"})
	resp, _ := e.page(receiver, "/inspect/c_1")
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/hooks/"+h.ID+"/calls/c_1" {
		t.Fatalf("inspect link = %d → %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	for _, tc := range []struct {
		id   authz.Identity
		path string
	}{{uiOther, "/inspect/c_1"}, {receiver, "/inspect/c_gone"}} {
		if resp, body := e.page(tc.id, tc.path); resp.StatusCode != 404 || !strings.Contains(body, "forgets them when it restarts") {
			t.Errorf("%s = %d", tc.path, resp.StatusCode)
		}
	}
}

func TestUITestRequestForm(t *testing.T) {
	e := newEnv(t, Config{CapName: testCap})
	h, err := e.api.CreateHook(context.Background(), hooks.CreateRequest{Path: "cb"})
	if err != nil {
		t.Fatal(err)
	}
	if _, body := e.page(receiver, "/hooks/"+h.ID); !strings.Contains(body, `id="test-form" data-hook="`+h.ID+`"`) {
		t.Errorf("the owner's hook page has no test form:\n%s", body)
	}
	if _, body := e.page(uiAdmin, "/hooks/"+h.ID); strings.Contains(body, "test-form") {
		t.Error("an admin was offered the test form")
	}
}

func TestUIHookForm(t *testing.T) {
	e := newEnv(t, Config{CapName: testCap})
	scoped := identify.User("laptop", "moritz@example.com", 1, `{"roles":["receiver"],"channels":["default","prod","staging.*"]}`)
	resp, body := e.page(scoped, "/hooks/new")
	if resp.StatusCode != 200 {
		t.Fatalf("new form = %d:\n%s", resp.StatusCode, body)
	}
	for _, want := range []string{
		`id="hook-form" data-method="POST" data-path="/api/v1/hooks"`,
		`<option value="default">`, `<option value="prod">`, "default, prod, staging.*",
		`name="settings.holdTimeout"`, `data-type="duration"`, "At most 5m0s.", "~moritz/",
		`data-error-for="settings.holdTimeout"`, `data-error-for=""`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("new form lacks %q", want)
		}
	}
	if strings.Contains(body, `<option value="staging.*">`) {
		t.Error("a channel pattern with a wildcard was offered as a channel")
	}
	assertInert(t, body)

	h, err := e.api.CreateHook(context.Background(), hooks.CreateRequest{Path: "cb", Description: "ci",
		Settings: hooks.Settings{HoldTimeout: new(hooks.Duration(45 * time.Second)), AttachPolicy: new(hooks.AttachReject)}})
	if err != nil {
		t.Fatal(err)
	}
	resp, body = e.page(receiver, "/hooks/"+h.ID+"/edit")
	if resp.StatusCode != 200 {
		t.Fatalf("edit form = %d", resp.StatusCode)
	}
	for _, want := range []string{
		`data-method="PATCH" data-path="/api/v1/hooks/` + h.ID + `" data-editing`,
		`name="path" value="cb"`, `name="settings.holdTimeout" data-type="duration" value="45s"`, `<option value="reject" selected>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("edit form lacks %q", want)
		}
	}
	for _, tc := range []struct {
		id   authz.Identity
		path string
		code int
	}{
		{uiOther, "/hooks/" + h.ID + "/edit", 404},
		{uiAdmin, "/hooks/" + h.ID + "/edit", 404},
		{sender, "/hooks/new", 403},
	} {
		if resp, _ := e.page(tc.id, tc.path); resp.StatusCode != tc.code {
			t.Errorf("%s as %s = %d, want %d", tc.path, tc.id.NodeName, resp.StatusCode, tc.code)
		}
	}
}

func TestUIAdminPage(t *testing.T) {
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
	resp, body := e.page(uiAdmin, "/admin")
	if resp.StatusCode != 200 {
		t.Fatalf("admin page = %d:\n%s", resp.StatusCode, body)
	}
	for _, want := range []string{
		"moritz@example.com", "other@example.com", `href="/hooks/` + mine.ID + `"`, `href="/hooks/` + theirs.ID + `"`,
		`data-hook="` + theirs.ID + `" data-next="/admin"`, "data-all", "2 hooks, 0 tunnels",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("admin page lacks %q", want)
		}
	}
	assertInert(t, body)

	if resp, _ := e.page(receiver, "/admin"); resp.StatusCode != 403 {
		t.Errorf("a non-admin's admin page = %d, want 403", resp.StatusCode)
	}
	// An admin who cannot receive callbacks starts on the admin page.
	if resp, _ := e.page(uiAdmin, "/"); resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/admin" {
		t.Errorf("admin's / = %d → %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	// One who can sees their own hooks first.
	both := identify.User("laptop", "moritz@example.com", 1, `{"roles":["receiver","admin"]}`)
	if resp, body := e.page(both, "/"); resp.StatusCode != 200 || !strings.Contains(body, mine.URL) || !strings.Contains(body, `href="/admin"`) {
		t.Errorf("receiver-admin's / = %d", resp.StatusCode)
	}
}
