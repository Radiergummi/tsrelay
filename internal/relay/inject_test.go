package relay

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Radiergummi/tsrelay/internal/inspect"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
)

func TestInject(t *testing.T) {
	e := newEnv(t)
	type seen struct{ method, uri, body, login, xff, custom string }
	got := make(chan seen, 1)
	e.attach(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- seen{r.Method, r.URL.RequestURI(), string(b), r.Header.Get(HeaderSenderLogin), r.Header.Get("X-Forwarded-For"), r.Header.Get("X-Custom")}
		w.WriteHeader(http.StatusAccepted)
		io.WriteString(w, "accepted")
	}), tunnel.Options{})
	from := httptest.NewRequest("POST", "http://relay.test/api/v1/hooks/x/test", nil) // RemoteAddr 192.0.2.1:1234

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
