package api

import (
	"bufio"
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Radiergummi/tsrelay/internal/authz"
	"github.com/Radiergummi/tsrelay/internal/events"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/identify"
	"github.com/Radiergummi/tsrelay/internal/problem"
)

// openStream opens path as an event stream with c. The returned channel
// carries the stream's "data:" lines and is closed when the stream ends.
func (e *env) openStream(c *http.Client, path string) (*http.Response, <-chan string) {
	e.t.Helper()
	req, _ := http.NewRequest("GET", e.srv.URL+path, nil)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := c.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { resp.Body.Close() })
	lines := make(chan string, 16)
	go func() {
		defer close(lines)
		s := bufio.NewScanner(resp.Body)
		for s.Scan() {
			if d, ok := strings.CutPrefix(s.Text(), "data: "); ok {
				lines <- d
			}
		}
	}()
	return resp, lines
}

// nextData returns the next data line, failing if the stream ends or stays
// quiet.
func nextData(t *testing.T, lines <-chan string) string {
	t.Helper()
	select {
	case d, ok := <-lines:
		if !ok {
			t.Fatal("the stream ended")
		}
		return d
	case <-time.After(5 * time.Second):
		t.Fatal("no event")
	}
	return ""
}

func streamEnds(t *testing.T, lines <-chan string) {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case d, ok := <-lines:
			if !ok {
				return
			}
			t.Errorf("event %s before the stream ended", d)
		case <-timeout:
			t.Fatal("the stream did not end")
		}
	}
}

func withHub(e *env) *events.Hub {
	h := &events.Hub{}
	e.api.Events = h
	return h
}

func hookEvent(owner authz.Identity, channel, id string) events.Event {
	return events.HookChanged(owner.UserID, channel, id)
}

func TestStreamNeedsRole(t *testing.T) {
	e := newEnv(t)
	nobody := identify.User("bare", "bare@example.com", 9)
	req, _ := http.NewRequest("GET", e.srv.URL+"/hooks", nil)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := e.fake.Client(nobody).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden || resp.Header.Get(problem.HeaderError) != problem.Forbidden {
		t.Fatalf("stream without a role = %d %s", resp.StatusCode, resp.Header.Get(problem.HeaderError))
	}
}

func TestStreamCap(t *testing.T) {
	e := newEnv(t)
	withHub(e) // without a hub, streams end at once
	c := e.fake.Client(moritz)
	var open []*http.Response
	for range 8 {
		resp, _ := e.openStream(c, "/hooks")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("stream %d = %d", len(open)+1, resp.StatusCode)
		}
		open = append(open, resp)
	}
	resp, _ := e.openStream(c, "/hooks")
	if resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get(problem.HeaderError) != problem.RateLimited {
		t.Fatalf("9th stream = %d %s", resp.StatusCode, resp.Header.Get(problem.HeaderError))
	}
	// Another node of the same relay is not affected.
	if resp, _ := e.openStream(e.fake.Client(other), "/hooks"); resp.StatusCode != http.StatusOK {
		t.Fatalf("other node's stream = %d", resp.StatusCode)
	}
	open[0].Body.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, _ := e.openStream(c, "/hooks")
		if resp.StatusCode == http.StatusOK {
			break
		}
		resp.Body.Close()
		if time.Now().After(deadline) {
			t.Fatalf("closing a stream did not free its slot: %d", resp.StatusCode)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestStreamAdminScoped(t *testing.T) {
	e := newEnv(t)
	hub := withHub(e)
	prodAdmin := identify.User("prod-admin", "padmin@example.com", 5, `{"roles":["admin"],"channels":["prod"]}`)
	_, lines := e.openStream(e.fake.Client(prodAdmin), "/hooks?all=true")
	hub.Publish(hookEvent(moritz, "dev", "h_dev"))
	hub.Publish(hookEvent(moritz, "prod", "h_prod"))
	if d := nextData(t, lines); !strings.Contains(d, "h_prod") {
		t.Fatalf("first event = %s, want h_prod's (h_dev's is outside the admin's channels)", d)
	}
}

// A stream checks its viewer again while open: grants it lost stop
// applying, and a failed identification keeps the last ones.
func TestStreamReidentifies(t *testing.T) {
	e := newEnv(t)
	e.api.ReidentifyEvery = 20 * time.Millisecond
	hub := withHub(e)
	boss := identify.User("boss-box", "boss@example.com", 6, `{"roles":["admin"]}`)
	_, lines := e.openStream(e.fake.Client(boss), "/hooks?all=true")
	hub.Publish(hookEvent(moritz, "default", "h_before"))
	if d := nextData(t, lines); !strings.Contains(d, "h_before") {
		t.Fatalf("event = %s", d)
	}

	// Still a receiver: the stream goes on, but only with boss's own hooks.
	e.fake.Update(identify.User("boss-box", "boss@example.com", 6, `{"roles":["receiver"]}`))
	time.Sleep(100 * time.Millisecond)
	hub.Publish(hookEvent(moritz, "default", "h_others"))
	hub.Publish(hookEvent(boss, "default", "h_own"))
	if d := nextData(t, lines); !strings.Contains(d, "h_own") {
		t.Fatalf("event = %s, want h_own (h_others is no longer visible)", d)
	}

	// No role at all: the stream ends.
	e.fake.Update(identify.User("boss-box", "boss@example.com", 6))
	time.Sleep(100 * time.Millisecond)
	hub.Publish(hookEvent(moritz, "default", "h_after"))
	streamEnds(t, lines)
}

func TestStreamKeepsIdentityWhenIdentifyFails(t *testing.T) {
	e := newEnv(t)
	e.api.ReidentifyEvery = 20 * time.Millisecond
	hub := withHub(e)
	boss := identify.User("boss-box", "boss@example.com", 6, `{"roles":["admin"]}`)
	_, lines := e.openStream(e.fake.Client(boss), "/hooks?all=true")
	e.fake.Fail(errors.New("not on the tailnet"))
	time.Sleep(100 * time.Millisecond)
	hub.Publish(hookEvent(moritz, "default", "h_others"))
	if d := nextData(t, lines); !strings.Contains(d, "h_others") {
		t.Fatalf("event = %s", d)
	}
}

// hangingIdentifier blocks Identify until its context ends once hang is set,
// as a WhoIs to an unresponsive node would.
type hangingIdentifier struct {
	identify.Identifier
	hang   atomic.Bool
	called chan struct{}
}

func (h *hangingIdentifier) Identify(ctx context.Context, addr string) (authz.Identity, error) {
	if !h.hang.Load() {
		return h.Identifier.Identify(ctx, addr)
	}
	select {
	case h.called <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return authz.Identity{}, ctx.Err()
}

// A hanging identification times out and keeps the last identity, rather
// than stalling the stream.
func TestStreamSurvivesHangingIdentify(t *testing.T) {
	// Restored after the server closes (cleanups run last first), when no
	// stream reads it any more.
	old := streamIdentifyTimeout
	t.Cleanup(func() { streamIdentifyTimeout = old })
	streamIdentifyTimeout = 50 * time.Millisecond
	e := newEnv(t)
	e.api.ReidentifyEvery = 20 * time.Millisecond
	hub := withHub(e)
	hi := &hangingIdentifier{Identifier: e.fake, called: make(chan struct{}, 1)}
	e.api.Identifier = hi
	boss := identify.User("boss-box", "boss@example.com", 6, `{"roles":["admin"]}`)
	_, lines := e.openStream(e.fake.Client(boss), "/hooks?all=true")
	hi.hang.Store(true)
	select {
	case <-hi.called:
	case <-time.After(5 * time.Second):
		t.Fatal("the stream never identified its viewer again")
	}
	hub.Publish(hookEvent(moritz, "default", "h_others"))
	if d := nextData(t, lines); !strings.Contains(d, "h_others") {
		t.Fatalf("event = %s", d)
	}
}

// A stream of one hook carries only the events its viewer may see now: an
// admin scoped to prod does not get a call the hook took in another channel.
func TestHookStreamsFilterByChannel(t *testing.T) {
	prodAdmin := identify.User("prod-admin", "padmin@example.com", 5, `{"roles":["admin"],"channels":["prod.*"]}`)
	for _, path := range []string{"", "/calls"} {
		t.Run("events"+path, func(t *testing.T) {
			e := newEnv(t)
			hub := withHub(e)
			h, err := e.mgr.Create(context.Background(), moritz, hooks.CreateRequest{Path: "cb", Channel: "prod.api"})
			if err != nil {
				t.Fatal(err)
			}
			_, lines := e.openStream(e.fake.Client(prodAdmin), "/hooks/"+string(h.ID)+path)
			call := func(channel, data string) events.Event {
				return events.Event{Type: events.TypeCall, Owner: moritz.UserID, Channel: channel, HookID: string(h.ID), Data: data}
			}
			hub.Publish(call("default", "in default"))
			hub.Publish(call("prod.api", "in prod"))
			if d := nextData(t, lines); d != `"in prod"` {
				t.Fatalf("first event = %s, want the prod call's", d)
			}
		})
	}
}
