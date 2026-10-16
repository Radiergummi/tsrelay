package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Radiergummi/tsrelay/internal/audit"
	"github.com/Radiergummi/tsrelay/internal/authz"
	"github.com/Radiergummi/tsrelay/internal/client"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/identify"
	"github.com/Radiergummi/tsrelay/internal/inspect"
	"github.com/Radiergummi/tsrelay/internal/problem"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
)

// connIdentity is the context key for the identity a dev listener stands for.
type connIdentity struct{}

// byListener identifies each connection as the identity its listener
// stands for (set through http.Server.ConnContext).
type byListener struct{}

func (byListener) Identify(ctx context.Context, _ string) (authz.Identity, error) {
	if id, ok := ctx.Value(connIdentity{}).(authz.Identity); ok {
		return id, nil
	}
	return authz.Identity{}, errors.New("no identity for this listener")
}

// TestUIDev runs one relay for working on the web UI, behind four
// listeners that each stand for one identity:
//
//	127.0.0.1:8099  dev@example.com     receiver and admin
//	127.0.0.1:8098  ops@example.com     admin only
//	127.0.0.1:8097  nobody@example.com  no grant
//	127.0.0.1:8096  other@example.com   receiver
//
// It serves the API and the embedded UI (a placeholder until `make ui`).
// `make dev` runs Vite in front of it, and Playwright starts it too. It
// runs only with TSRELAY_UI_DEV=1, until interrupted:
//
//	TSRELAY_UI_DEV=1 go test ./internal/server -run '^TestUIDev$' -count=1 -timeout 0 -v
func TestUIDev(t *testing.T) {
	if os.Getenv("TSRELAY_UI_DEV") == "" {
		t.Skip("set TSRELAY_UI_DEV=1 to serve the UI for development")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	me := identify.User("dev-laptop", "dev@example.com", 1, `{"roles":["receiver","admin"],"channels":["default","prod","staging.*"]}`)
	ops := identify.User("ops-box", "ops@example.com", 3, `{"roles":["admin"]}`)
	nobody := identify.User("box", "nobody@example.com", 4)
	other := identify.User("desk", "other@example.com", 2, `{"roles":["receiver"]}`)

	store, err := hooks.OpenJSON(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	mgr, err := hooks.NewManager(ctx, store, hooks.SlugConfig{})
	if err != nil {
		t.Fatal(err)
	}
	base := "http://127.0.0.1:8099"
	s := New(Config{BaseURL: base, AllowedHosts: []string{"127.0.0.1", "localhost"}, CapName: "mazetti.me/cap/relay"},
		byListener{}, mgr, audit.New(slog.New(slog.NewTextHandler(os.Stderr, nil))))
	defer s.Close()

	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"received":true}`))
	}))
	defer local.Close()

	create := func(owner authz.Identity, req hooks.CreateRequest) hooks.Hook {
		h, err := mgr.Create(ctx, owner, req)
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	live := create(me, hooks.CreateRequest{Path: "payments/done", Description: "has a tunnel"})
	create(me, hooks.CreateRequest{Path: "idle", Channel: "prod"})
	busy := create(me, hooks.CreateRequest{Path: "busy", Description: "100 calls"})
	hostile := create(me, hooks.CreateRequest{Path: "hostile", Description: "captures that must stay text"})
	theirs := create(other, hooks.CreateRequest{Path: "theirs"})

	sample := func(h hooks.Hook, owner authz.Identity, id string, c inspect.Call) {
		c.ID, c.HookID, c.Owner = id, h.ID, owner.UserID
		if c.ReceivedAt.IsZero() {
			c.ReceivedAt = time.Now()
		}
		if c.Kind == "" {
			c.Kind = inspect.KindSender
		}
		if c.Sender.Node == "" {
			c.Sender = inspect.Sender{Node: "worker", Tags: []string{"tag:prod-worker"}, IP: "100.64.0.9"}
		}
		if c.Method == "" {
			c.Method = "POST"
		}
		if c.Path == "" {
			c.Path = "/" + h.Path
		}
		if c.Outcome == "" {
			c.Outcome = inspect.OutcomeOK
		}
		if c.Status == 0 && c.Outcome == inspect.OutcomeOK {
			c.Status = 200
		}
		if c.Duration == 0 && c.Outcome != inspect.OutcomePending {
			c.Duration = hooks.Duration(42 * time.Millisecond)
		}
		if c.Attempts == nil {
			c.Attempts = []inspect.Attempt{{ID: "a_" + id, StartedAt: c.ReceivedAt, TunnelID: "t_dev", Node: "dev-laptop", Status: c.Status,
				TTFB: hooks.Duration(30 * time.Millisecond), Duration: c.Duration,
				ResponseHeaders: http.Header{"Content-Type": {"application/json"}}, ResponseBody: full(`{"received":true}`)}}
		}
		s.inspect.Add(c)
	}
	now := time.Now()
	sample(live, me, "c_json", inspect.Call{ReceivedAt: now.Add(-5 * time.Minute), Query: "order=42",
		RequestHeaders: http.Header{"Content-Type": {"application/json"}, "Accept-Encoding": {"gzip"}}, Redacted: []string{"Authorization"},
		RequestBody: full(`{"order":42,"amount":12345678901234567890,"items":[{"sku":"A-1","qty":2}],"note":"it's <b>bold</b>"}`)})
	sample(live, me, "c_replay", inspect.Call{ReceivedAt: now.Add(-4 * time.Minute), Kind: inspect.KindReplay, ReplayOf: "c_json",
		Sender: inspect.Sender{Node: "dev-laptop", Login: "dev@example.com"}, Query: "order=42",
		RequestHeaders: http.Header{"Content-Type": {"application/json"}},
		RequestBody:    full(`{"order":42,"amount":12345678901234567890,"items":[{"sku":"A-1","qty":2}],"note":"it's <b>bold</b>"}`),
		Status:         201,
		Attempts: []inspect.Attempt{{ID: "a_c_replay", StartedAt: now.Add(-4 * time.Minute), Node: "dev-laptop", Status: 201,
			ResponseHeaders: http.Header{"Content-Type": {"application/json"}, "X-Request-Id": {"r-2"}}, ResponseBody: full(`{"received":true,"again":true}`)}}})
	sample(live, me, "c_cut", inspect.Call{ReceivedAt: now.Add(-3 * time.Minute),
		RequestHeaders: http.Header{"Content-Type": {"application/json"}},
		RequestBody:    inspect.Capture{Body: []byte(`{"big":"aaaaaaaa`), Size: 1 << 20, Truncated: true, Captured: true}})
	sample(live, me, "c_bin", inspect.Call{ReceivedAt: now.Add(-2 * time.Minute),
		RequestHeaders: http.Header{"Content-Type": {"application/octet-stream"}}, RequestBody: full("\x89PNG\r\n\x1a\n\x00\xff")})
	sample(live, me, "c_refused", inspect.Call{ReceivedAt: now.Add(-time.Minute), Outcome: "hook_not_found", Status: 404, Attempts: []inspect.Attempt{}})
	sample(live, me, "c_pending", inspect.Call{Outcome: inspect.OutcomePending, Attempts: []inspect.Attempt{}})

	methods := []string{"POST", "POST", "POST", "PUT", "GET"}
	statuses := []int{200, 200, 201, 204, 302, 400, 404, 500, 502}
	for i := range 100 {
		st := statuses[i%len(statuses)]
		c := inspect.Call{ReceivedAt: now.Add(-time.Duration(100-i) * time.Second), Method: methods[i%len(methods)],
			Path: fmt.Sprintf("/busy/item/%d", i), Status: st, RequestBody: full(fmt.Sprintf(`{"n":%d}`, i))}
		if st >= 500 && i%2 == 0 {
			c.Outcome, c.Status = problem.UpstreamUnreachable, problem.StatusUpstreamUnreachable
		}
		if i%7 == 0 {
			c.Sender = inspect.Sender{Node: "billing", Tags: []string{"tag:billing"}}
		}
		sample(busy, me, fmt.Sprintf("c_busy_%03d", i), c)
	}

	hostileBody := `<script>alert(1)</script><img src=x onerror=alert(2)><a href="javascript:alert(3)">x</a>` +
		"\u202Egnp.exe\u202C " + strings.Repeat("A", 200_000)
	sample(hostile, me, "c_hostile_html", inspect.Call{Path: `/x"'<script>alert(4)</script>`, Query: `q=<img src=x onerror=alert(5)>`,
		RequestHeaders: http.Header{"Content-Type": {"text/html"}, "X-Evil": {`</td><script>alert(6)</script>`}, "X-Bidi": {"abc\u202Edef"}},
		RequestBody:    full(hostileBody)})
	sample(hostile, me, "c_hostile_json", inspect.Call{
		RequestHeaders: http.Header{"Content-Type": {"application/json"}},
		RequestBody:    full(`{"<img src=x onerror=alert(7)>":"javascript:alert(8)","a":1,"a":2}`)})
	sample(hostile, me, "c_hostile_form", inspect.Call{
		RequestHeaders: http.Header{"Content-Type": {"application/x-www-form-urlencoded"}},
		RequestBody:    full(`a=%3Cscript%3E&b=<script>alert(9)</script>`)})
	sample(theirs, other, "c_theirs", inspect.Call{Query: "token=s3cr3t", RequestBody: full("their secret")})

	listeners := []struct {
		addr string
		id   authz.Identity
	}{{"127.0.0.1:8099", me}, {"127.0.0.1:8098", ops}, {"127.0.0.1:8097", nobody}, {"127.0.0.1:8096", other}}
	for _, l := range listeners {
		ln, err := net.Listen("tcp", l.addr)
		if err != nil {
			t.Fatal(err)
		}
		srv := &http.Server{Handler: s.Handler(), ConnContext: func(ctx context.Context, _ net.Conn) context.Context {
			return context.WithValue(ctx, connIdentity{}, l.id)
		}}
		go srv.Serve(ln)
		defer srv.Close()
	}

	target, _ := url.Parse(local.URL)
	relayAPI := &client.API{BaseURL: base, HTTP: tunnel.NewHTTPClient((&net.Dialer{}).DialContext)}
	go (&tunnel.Client{
		Hooks:  []string{string(live.ID)},
		Target: target.String(),
		Dial: func(ctx context.Context, ids []string, a tunnel.Attach) (net.Conn, error) {
			return tunnel.Dial(ctx, relayAPI.HTTP, relayAPI.TunnelURL(a, ids...))
		},
		Handler:    client.NewForwarder(target, client.Options{}),
		MinBackoff: time.Second,
	}).Run(ctx)

	t.Logf("UI at %s (dev), :8098 (admin), :8097 (no access), :8096 (other user); local app at %s; interrupt to stop", base, local.URL)
	<-ctx.Done()
}
