package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Radiergummi/tsrelay/internal/authz"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/identify"
	"github.com/Radiergummi/tsrelay/internal/inspect"
)

// getJSON GETs path as the receiver and decodes a 2xx answer into out.
func (e *env) getJSON(path string, out any) int {
	e.t.Helper()
	resp, err := e.api.HTTP.Get(e.base + path)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 300 && out != nil {
		json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

// postJSON POSTs in (nil for no body) as the receiver and decodes a 2xx
// answer into out.
func (e *env) postJSON(path string, in, out any) int {
	e.t.Helper()
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}
	req, _ := http.NewRequest("POST", e.base+path, body)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := e.api.HTTP.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 300 && out != nil {
		json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

func TestCallsEndToEnd(t *testing.T) {
	e := newEnv(t, Config{})
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusAccepted) }))
	defer local.Close()
	h, err := e.api.CreateHook(context.Background(), hooks.CreateRequest{Path: "cb"})
	if err != nil {
		t.Fatal(err)
	}
	defer e.cli(h.ID, local.URL)()
	resp := e.send(h, "/done", []byte("{}"), nil)
	io.ReadAll(resp.Body)
	var list []inspect.Summary
	e.waitFor(func() bool {
		list = nil
		return e.getJSON("/api/v1/hooks/"+h.ID+"/calls", &list) == 200 && len(list) == 1 && list[0].Outcome == inspect.OutcomeOK
	})
	if list[0].ID != resp.Header.Get("Tsrelay-Call-Id") || list[0].Status != 202 || list[0].Path != "/done" {
		t.Fatalf("summary = %+v", list[0])
	}
}

// A replay re-sends the captured body and leaves out redacted headers.
func TestReplayEndToEnd(t *testing.T) {
	e := newEnv(t, Config{})
	type seen struct{ auth, body string }
	got := make(chan seen, 2)
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- seen{r.Header.Get("Authorization"), string(b)}
	}))
	defer local.Close()
	h, _ := e.api.CreateHook(context.Background(), hooks.CreateRequest{Path: "cb", Settings: hooks.Settings{Capture: new(true)}})
	defer e.cli(h.ID, local.URL)()

	resp := e.send(h, "/done", []byte(`{"n":1}`), http.Header{"Authorization": {"Bearer s3cr3t"}})
	io.ReadAll(resp.Body)
	if first := <-got; first.auth == "" {
		t.Fatal("precondition: the original call carries Authorization")
	}
	callID := resp.Header.Get("Tsrelay-Call-Id")
	e.waitFor(func() bool {
		var c inspect.Call
		return e.getJSON("/api/v1/hooks/"+h.ID+"/calls/"+callID, &c) == 200 && c.Outcome == inspect.OutcomeOK
	})
	var replay inspect.Call
	if code := e.postJSON("/api/v1/hooks/"+h.ID+"/calls/"+callID+"/replay", nil, &replay); code != 201 {
		t.Fatalf("replay = %d", code)
	}
	if second := <-got; second.auth != "" || second.body != `{"n":1}` {
		t.Fatalf("replay delivered %+v; want the same body and no Authorization", second)
	}
	if replay.Kind != inspect.KindReplay || replay.ReplayOf != callID || replay.Outcome != inspect.OutcomeOK {
		t.Fatalf("replay = %+v", replay)
	}
}

type sseEvent struct{ typ, data string }

// sse opens the event stream as id. The subscription is live when it
// returns: the relay subscribes before it sends the response headers.
func (e *env) sse(id authz.Identity, query string) <-chan sseEvent {
	e.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", e.base+"/api/v1/events"+query, nil)
	resp, err := e.fake.Client(id).Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/event-stream" {
		e.t.Fatalf("event stream = %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	e.t.Cleanup(func() { cancel(); resp.Body.Close() })
	out := make(chan sseEvent, 64)
	go func() {
		defer close(out)
		sc := bufio.NewScanner(resp.Body)
		var ev sseEvent
		for sc.Scan() {
			switch line := sc.Text(); {
			case strings.HasPrefix(line, "event: "):
				ev.typ = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				ev.data = strings.TrimPrefix(line, "data: ")
			case line == "" && ev.typ != "":
				out <- ev
				ev = sseEvent{}
			}
		}
	}()
	return out
}

// until reads events until one matches, and returns all it read.
func until(t *testing.T, events <-chan sseEvent, match func(sseEvent) bool) []sseEvent {
	t.Helper()
	var seen []sseEvent
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatalf("stream ended; events: %+v", seen)
			}
			seen = append(seen, ev)
			if match(ev) {
				return seen
			}
		case <-timeout:
			t.Fatalf("no matching event; events: %+v", seen)
		}
	}
}

func TestEventStream(t *testing.T) {
	e := newEnv(t, Config{})
	operator := identify.User("ops-box", "ops@example.com", 3, `{"roles":["admin"]}`)
	stranger := identify.User("desk", "other@example.com", 2, `{"roles":["receiver"]}`)
	mine, all, theirs := e.sse(receiver, ""), e.sse(operator, "?all=true"), e.sse(stranger, "")

	h, err := e.api.CreateHook(context.Background(), hooks.CreateRequest{Path: "cb"})
	if err != nil {
		t.Fatal(err)
	}
	isHook := func(ev sseEvent) bool { return ev.typ == "hook" && strings.Contains(ev.data, h.ID) }
	until(t, mine, isHook) // created
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusAccepted) }))
	defer local.Close()
	defer e.cli(h.ID, local.URL)()
	until(t, mine, isHook) // tunnel attached

	resp := e.send(h, "/done?token=s3cr3t", []byte(`{"secret":true}`), http.Header{"X-Custom": {"secret-header"}})
	io.ReadAll(resp.Body)
	finished := func(ev sseEvent) bool { return ev.typ == "call" && strings.Contains(ev.data, `"outcome":"ok"`) }
	for name, stream := range map[string]<-chan sseEvent{"owner": mine, "admin": all} {
		for _, ev := range until(t, stream, finished) {
			if strings.Contains(ev.data, "s3cr3t") || strings.Contains(ev.data, "secret-header") || strings.Contains(ev.data, "secret") {
				t.Errorf("%s's stream carried owner-only data: %s", name, ev.data)
			}
		}
	}
	select {
	case ev := <-theirs:
		t.Fatalf("another user's stream received %+v", ev)
	case <-time.After(100 * time.Millisecond):
	}

	if resp, err := e.fake.Client(sender).Get(e.base + "/api/v1/events"); err != nil || resp.StatusCode != 403 {
		t.Fatalf("tagged node's stream = %v, %v; want 403", resp, err)
	}
}

// Closing the server ends open streams, so a graceful shutdown does not
// wait for them.
func TestEventStreamEndsOnClose(t *testing.T) {
	e := newEnv(t, Config{})
	stream := e.sse(receiver, "")
	e.srv.Close()
	select {
	case _, ok := <-stream:
		if ok {
			t.Fatal("unexpected event")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the stream outlived Server.Close")
	}
}
