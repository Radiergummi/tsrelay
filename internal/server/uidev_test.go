package server

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/signal"
	"testing"
	"time"

	"github.com/Radiergummi/tsrelay/internal/audit"
	"github.com/Radiergummi/tsrelay/internal/authz"
	"github.com/Radiergummi/tsrelay/internal/client"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/identify"
	"github.com/Radiergummi/tsrelay/internal/inspect"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
)

// fixedIdentity identifies every connection as the same node.
type fixedIdentity authz.Identity

func (f fixedIdentity) Identify(context.Context, string) (authz.Identity, error) {
	return authz.Identity(f), nil
}

// TestUIDev serves the UI on 127.0.0.1:8099 for checking it in a browser.
// Every request comes from one user who is a receiver and an admin. Another
// user owns a hook too. One of the user's hooks has a tunnel to a local app
// that answers every call, and sample calls cover the cases the call page
// must render. It runs only with TSRELAY_UI_DEV=1, until interrupted:
//
//	TSRELAY_UI_DEV=1 go test ./internal/server -run TestUIDev -timeout 0 -v
func TestUIDev(t *testing.T) {
	if os.Getenv("TSRELAY_UI_DEV") == "" {
		t.Skip("set TSRELAY_UI_DEV=1 to serve the UI for manual checks")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	me := identify.User("dev-laptop", "dev@example.com", 1, `{"roles":["receiver","admin"],"channels":["default","prod","staging.*"]}`)
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
	const addr = "127.0.0.1:8099"
	base := "http://" + addr
	s := New(Config{BaseURL: base, AllowedHosts: []string{"127.0.0.1", "localhost"}, CapName: "mazetti.me/cap/relay"},
		fixedIdentity(me), mgr, audit.New(slog.New(slog.NewTextHandler(os.Stderr, nil))))
	defer s.Close()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	go http.Serve(ln, s.Handler())

	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"received":true}`))
	}))
	defer local.Close()

	live, err := mgr.Create(ctx, me, hooks.CreateRequest{Path: "payments/done", Description: "has a tunnel"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.Create(ctx, me, hooks.CreateRequest{Path: "idle", Channel: "prod"}); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.Create(ctx, other, hooks.CreateRequest{Path: "theirs"}); err != nil {
		t.Fatal(err)
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

	sample := func(id string, c inspect.Call) {
		c.ID, c.HookID, c.Owner, c.ReceivedAt, c.Kind = id, live.ID, me.UserID, time.Now(), inspect.KindSender
		c.Sender = inspect.Sender{Node: "worker", Tags: []string{"tag:prod-worker"}, IP: "100.64.0.9"}
		if c.Method == "" {
			c.Method, c.Path = "POST", "/payments/done"
		}
		if c.Attempts == nil {
			c.Attempts = []inspect.Attempt{{ID: "a_" + id, StartedAt: time.Now(), TunnelID: "t_dev", Node: "dev-laptop", Status: c.Status,
				ResponseHeaders: http.Header{"Content-Type": {"application/json"}}, ResponseBody: full(`{"received":true}`)}}
		}
		s.inspect.Add(c)
	}
	sample("c_json", inspect.Call{Query: "order=42", Outcome: inspect.OutcomeOK, Status: 200,
		RequestHeaders: http.Header{"Content-Type": {"application/json"}}, Redacted: []string{"Authorization"},
		RequestBody: full(`{"order":42,"items":[{"sku":"A-1","qty":2}],"note":"it's <b>bold</b>"}`)})
	sample("c_cut", inspect.Call{Outcome: inspect.OutcomeOK, Status: 200,
		RequestBody: inspect.Capture{Body: []byte(`{"big":"aaaaaaaa`), Size: 1 << 20, Truncated: true, Captured: true}})
	sample("c_bin", inspect.Call{Outcome: inspect.OutcomeOK, Status: 200,
		RequestHeaders: http.Header{"Content-Type": {"application/octet-stream"}}, RequestBody: full("\x89PNG\r\n\x1a\n\x00")})
	sample("c_refused", inspect.Call{Outcome: "hook_not_found", Status: 404, Attempts: []inspect.Attempt{}})

	t.Logf("UI at %s, local app at %s; interrupt to stop", base, local.URL)
	<-ctx.Done()
}
