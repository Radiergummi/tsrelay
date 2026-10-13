package events

import (
	"testing"
	"time"
)

func TestPublishMatches(t *testing.T) {
	var h Hub
	mine, cancel := h.Subscribe(func(e Event) bool { return e.Owner == 1 })
	defer cancel()
	h.Publish(Event{Type: TypeCall, Owner: 2, HookID: "h_other"})
	h.Publish(Event{Type: TypeCall, Owner: 1, HookID: "h_mine"})
	select {
	case e := <-mine:
		if e.HookID != "h_mine" {
			t.Fatalf("got %+v, want only the matching event", e)
		}
	case <-time.After(time.Second):
		t.Fatal("matching event not delivered")
	}
}

// A subscriber that never reads must not block Publish; it is dropped.
func TestSlowSubscriberIsDropped(t *testing.T) {
	var h Hub
	slow, _ := h.Subscribe(func(Event) bool { return true })
	done := make(chan struct{})
	go func() {
		for range Buffer + 10 {
			h.Publish(Event{Type: TypeHook})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Publish blocked on a subscriber that does not read")
	}
	n := 0
	for range slow {
		n++
	}
	if n != Buffer {
		t.Fatalf("dropped subscriber received %d events before its channel closed, want %d", n, Buffer)
	}
}

func TestCancelAndClose(t *testing.T) {
	var h Hub
	a, cancelA := h.Subscribe(func(Event) bool { return true })
	cancelA()
	cancelA() // idempotent
	if _, open := <-a; open {
		t.Fatal("cancel must close the channel")
	}
	b, _ := h.Subscribe(func(Event) bool { return true })
	h.Close()
	if _, open := <-b; open {
		t.Fatal("Close must end subscriptions")
	}
	c, _ := h.Subscribe(func(Event) bool { return true })
	if _, open := <-c; open {
		t.Fatal("subscribing after Close must end at once")
	}
	var nilHub *Hub
	nilHub.Publish(Event{})
	d, cancelD := nilHub.Subscribe(func(Event) bool { return true })
	cancelD()
	if _, open := <-d; open {
		t.Fatal("a nil hub's subscription must be closed")
	}
}

func TestHookChanged(t *testing.T) {
	e := HookChanged(7, "h_x")
	if e.Type != TypeHook || e.Owner != 7 || e.HookID != "h_x" {
		t.Fatalf("event = %+v", e)
	}
	if d, ok := e.Data.(map[string]string); !ok || d["id"] != "h_x" {
		t.Fatalf("data = %#v", e.Data)
	}
}
