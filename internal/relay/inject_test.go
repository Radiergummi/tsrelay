package relay

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/inspect"
	"github.com/Radiergummi/tsrelay/internal/problem"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
)

func TestInject(t *testing.T) {
	e := newEnv(t)
	e.patch(hooks.SettingsPatch{Capture: hooks.Opt[bool]{Set: true, Value: new(true)}})
	type seen struct{ method, uri, body, login, xff, custom string }
	got := make(chan seen, 1)
	e.attach(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- seen{r.Method, r.URL.RequestURI(), string(b), r.Header.Get(HeaderSenderLogin), r.Header.Get("X-Forwarded-For"), r.Header.Get("X-Custom")}
		w.WriteHeader(http.StatusAccepted)
		io.WriteString(w, "accepted")
	}), tunnel.Options{})
	from := httptest.NewRequest("POST", "http://relay.test/hooks/x/test", nil) // RemoteAddr 192.0.2.1:1234

	// receiver has no sender role: a test request needs none.
	c, err := e.h.Inject(context.Background(), Injection{
		Hook: e.hook, Kind: inspect.KindTest, Sender: receiver,
		Method: "PUT", Path: "/x", Query: "y=1", Header: http.Header{"X-Custom": {"v"}}, Body: []byte("hello"), From: from,
	})
	if err != nil {
		t.Fatal(err)
	}
	if g := <-got; g != (seen{"PUT", "/x?y=1", "hello", "moritz@example.com", "192.0.2.1", "v"}) {
		t.Fatalf("local app saw %+v", g)
	}
	if c.Kind != inspect.KindTest || c.Outcome != inspect.OutcomeOK || c.Status != 202 || c.Sender.Login != "moritz@example.com" ||
		len(c.Attempts) != 1 || string(c.Attempts[0].ResponseBody.Body) != "accepted" {
		t.Fatalf("call = %+v", c)
	}
	e.waitLog(`"kind":"test"`)
}

// A local app that stops responding mid-stream makes the reverse proxy
// panic with http.ErrAbortHandler (it runs under an http.Server, since a
// real call carries the API request's context). Inject must recover that
// and still return the recorded (failed) call, not crash the owner's
// request.
func TestInjectTunnelLostMidResponse(t *testing.T) {
	e := newEnv(t)
	e.patch(hooks.SettingsPatch{IdleTimeout: hooks.Opt[hooks.Duration]{Set: true, Value: new(hooks.Duration(50 * time.Millisecond))}})
	e.attach(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "partial")
		w.(http.Flusher).Flush()
		<-r.Context().Done() // the idle timeout cancels the call's context
	}), tunnel.Options{})

	from := httptest.NewRequest("POST", "http://relay.test/", nil)
	// Stands in for the real call's context, which carries this because
	// the API request is served by a real http.Server.
	ctx := context.WithValue(context.Background(), http.ServerContextKey, &http.Server{})
	c, err := e.h.Inject(ctx, Injection{Hook: e.hook, Kind: inspect.KindTest, Sender: receiver, Method: "POST", From: from})
	if err != nil {
		t.Fatalf("Inject returned an error instead of the failed call: %v", err)
	}
	if c.Outcome != problem.UpstreamTimeout || len(c.Attempts) != 1 {
		t.Fatalf("call = %+v, want one attempt with outcome %q", c, problem.UpstreamTimeout)
	}
}

// An injected call goes only to the hook it names, even if another hook
// now has its path.
func TestInjectNotRouted(t *testing.T) {
	e := newEnv(t)
	gone := e.hook
	gone.ID = "h_gone"
	from := httptest.NewRequest("POST", "http://relay.test/", nil)
	if _, err := e.h.Inject(context.Background(), Injection{Hook: gone, Kind: inspect.KindTest, Sender: receiver, Method: "POST", From: from}); !errors.Is(err, ErrNotRouted) {
		t.Fatalf("err = %v, want ErrNotRouted", err)
	}
}
