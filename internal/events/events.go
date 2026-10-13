// Package events fans out relay changes to live subscribers, such as the
// API's server-sent event stream (DESIGN.md §10.1).
package events

import (
	"sync"

	"tailscale.com/tailcfg"
)

const (
	TypeCall = "call" // Data is an inspect.Summary
	TypeHook = "hook" // a hook or its tunnel changed; Data names the hook
)

// Buffer is how many events a subscriber may fall behind before it is dropped.
const Buffer = 64

// Event is one change. Owner is the user who owns the hook it concerns.
type Event struct {
	Type   string
	Owner  tailcfg.UserID
	HookID string
	Data   any // encoded as JSON for the event stream
}

// Hub delivers published events to matching subscribers. Publish never
// blocks: a subscriber whose buffer is full is dropped and its channel
// closed, so a slow reader only loses its own stream, never delays the
// relay. The zero Hub is ready to use; a nil *Hub ignores everything.
type Hub struct {
	mu     sync.Mutex
	subs   map[*sub]struct{}
	closed bool
}

type sub struct {
	ch    chan Event
	match func(Event) bool
}

// Subscribe returns a channel of the events match accepts, and a function
// that ends the subscription. The channel is closed when the subscription
// ends, is dropped for falling behind, or the hub closes. match runs with
// the hub's lock held and must be quick.
func (h *Hub) Subscribe(match func(Event) bool) (<-chan Event, func()) {
	s := &sub{ch: make(chan Event, Buffer), match: match}
	if h == nil {
		close(s.ch)
		return s.ch, func() {}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		close(s.ch)
		return s.ch, func() {}
	}
	if h.subs == nil {
		h.subs = map[*sub]struct{}{}
	}
	h.subs[s] = struct{}{}
	return s.ch, func() { h.drop(s) }
}

func (h *Hub) drop(s *sub) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.subs[s]; ok {
		delete(h.subs, s)
		close(s.ch)
	}
}

func (h *Hub) Publish(e Event) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.subs {
		if !s.match(e) {
			continue
		}
		select {
		case s.ch <- e:
		default: // too far behind: drop it rather than wait
			delete(h.subs, s)
			close(s.ch)
		}
	}
}

// HookChanged is the event for a hook that was created, updated, deleted or
// expired, or whose tunnel attached or detached. Subscribers refetch it.
func HookChanged(owner tailcfg.UserID, hookID string) Event {
	return Event{Type: TypeHook, Owner: owner, HookID: hookID, Data: map[string]string{"id": hookID}}
}

// Close ends every subscription, for relay shutdown. Later subscriptions
// end at once.
func (h *Hub) Close() {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	for s := range h.subs {
		close(s.ch)
	}
	h.subs = nil
}
