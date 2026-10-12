package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Radiergummi/tsrelay/internal/hooks"
)

func TestOps(t *testing.T) {
	e := newEnv(t, Config{})
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(204)
	}))
	defer local.Close()
	hook, err := e.api.CreateHook(context.Background(), hooks.CreateRequest{Path: "cb"})
	if err != nil {
		t.Fatal(err)
	}
	defer e.cli(hook.ID, local.URL)()

	ops := &Ops{}
	get := func(path string) (int, string) {
		rec := httptest.NewRecorder()
		ops.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		return rec.Code, rec.Body.String()
	}
	if code, _ := get("/healthz"); code != 200 {
		t.Fatalf("/healthz before ready = %d, want 200 (liveness while tsnet starts)", code)
	}
	for _, p := range []string{"/readyz", "/metrics"} {
		if code, _ := get(p); code != 503 {
			t.Fatalf("%s before ready = %d, want 503", p, code)
		}
	}
	ops.Ready(e.srv)
	if code, _ := get("/readyz"); code != 200 {
		t.Fatalf("/readyz = %d", code)
	}
	if code, _ := get("/nope"); code != 404 {
		t.Fatalf("/nope = %d", code)
	}

	e.send(hook, "", []byte("hello"), nil) // delivered
	req, _ := http.NewRequest("POST", hook.URL, nil)
	resp, err := e.fake.Client(receiver).Do(req) // a receiver is not a sender: denied
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// Call records are written after the response, so wait for them.
	var body string
	e.waitFor(func() bool {
		_, body = get("/metrics")
		return strings.Contains(body, `tsrelay_calls_total{outcome="ok"} 1`) &&
			strings.Contains(body, `tsrelay_calls_total{outcome="sender_forbidden"} 1`)
	})
	for _, want := range []string{
		`tsrelay_call_bytes_total{direction="in"} 5`,
		`tsrelay_authz_denials_total{action="call"} 1`,
		"\ntsrelay_tunnels 1\n",
		`tsrelay_hooks{ephemeral="false"} 1`,
		`tsrelay_hooks{ephemeral="true"} 0`,
		"\ntsrelay_held_calls 0\n",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics lack %q:\n%s", want, body)
		}
	}
}
