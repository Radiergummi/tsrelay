package client

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"slices"
	"sync"

	"github.com/Radiergummi/tsrelay/internal/api"
	"github.com/Radiergummi/tsrelay/internal/problem"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
)

// DropReason says why a session stopped serving a hook.
type DropReason string

const (
	DropReplaced DropReason = "replaced" // another tunnel serves it now
	DropRevoked  DropReason = "revoked"  // the owner's grants no longer cover it
	DropDeleted  DropReason = "deleted"  // it no longer exists
)

// EventKind tells the events of a Session apart.
type EventKind int

const (
	EventConnected     EventKind = iota // the tunnel (re)attached
	EventLost                           // Err says why; the session redials
	EventHookChanged                    // Hook is the new state; OldID differs when Recreated
	EventHookDropped                    // Hook as last known; Reason
	EventCall                           // Call
	EventUpstreamError                  // Err: the local app could not be reached
)

// Event is something that happened to a Session; Kind says which fields
// are set.
type Event struct {
	Kind      EventKind
	Err       error
	Hook      api.Hook
	OldID     string
	Recreated bool
	Reason    DropReason
	Call      Call
}

// Session keeps hooks served from one local target over one tunnel. It is
// the one owner of what each hook currently is: it follows renames and
// public URL changes, drops hooks the relay takes away and, once
// connected, hooks a redial is refused for.
type Session struct {
	API      *API
	Hooks    []api.Hook // served from the start
	Target   *url.URL
	Insecure bool
	// Recreate, if set, replaces a hook the relay no longer has (a 404 on
	// any dial) instead of dropping it.
	Recreate func(ctx context.Context, gone api.Hook) (api.Hook, error)
	// OnEvent reports what happens. EventCall and EventUpstreamError may
	// arrive concurrently; all other events arrive one at a time, in order,
	// and OnEvent may call Current while handling them.
	OnEvent func(Event)

	emitMu  sync.Mutex // held while emitting ordered events and changing current; taken before mu
	mu      sync.Mutex
	current []api.Hook // the one list of hooks served, which tunnel.Client dials
	seqs    updateSeqs
}

// Run serves the hooks until ctx is done (nil), no hooks remain
// (tunnel.ErrNoHooks), or the relay refuses in a way retrying would not
// fix (its problem).
func (s *Session) Run(ctx context.Context) error {
	s.mu.Lock()
	s.current, s.seqs = slices.Clone(s.Hooks), updateSeqs{}
	s.mu.Unlock()
	tc := &tunnel.Client{
		List:   s.ids,
		Target: s.Target.Redacted(),
		Dial:   s.dial,
		Handler: LogCalls(NewForwarder(s.Target, Options{
			Insecure:        s.Insecure,
			OnUpstreamError: func(err error) { s.emit(Event{Kind: EventUpstreamError, Err: err}) },
		}), func(c Call) { s.emit(Event{Kind: EventCall, Call: c}) }),
		OnState:   s.state,
		OnControl: s.control,
	}
	return tc.Run(ctx)
}

// Current returns the hooks served now.
func (s *Session) Current() []api.Hook {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.current)
}

func (s *Session) emit(e Event) {
	if s.OnEvent != nil {
		s.OnEvent(e)
	}
}

// ids are the IDs of the current hooks.
func (s *Session) ids() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, len(s.current))
	for i, h := range s.current {
		ids[i] = h.ID
	}
	return ids
}

func (s *Session) state(connected bool, err error) {
	s.emitMu.Lock()
	defer s.emitMu.Unlock()
	if connected {
		s.emit(Event{Kind: EventConnected})
		return
	}
	s.emit(Event{Kind: EventLost, Err: err})
}

// control applies a control message to the current hooks. A type it does not
// know changes nothing, and fields it does not know are ignored when the
// message is decoded: the v1 protocol only grows (DESIGN.md §6.2).
func (s *Session) control(ctl tunnel.Control) {
	s.emitMu.Lock()
	defer s.emitMu.Unlock()
	s.mu.Lock()
	s.seqs.observe(ctl)
	var events []Event
	for _, info := range ctl.Hooks {
		i := slices.IndexFunc(s.current, func(h api.Hook) bool { return h.ID == info.ID })
		if i < 0 {
			continue
		}
		switch ctl.Type {
		case tunnel.ControlHookUpdated:
			cur := s.current[i]
			h := cur
			h.URL, h.Path, h.Public, h.PublicURL = info.URL, info.Path, info.Public, info.PublicURL
			if !s.seqs.fresh(info.ID, ctl.Seq) || sameHook(h, cur) {
				continue
			}
			s.current[i] = h
			events = append(events, Event{Kind: EventHookChanged, Hook: h, OldID: h.ID})
		case tunnel.ControlReplaced, tunnel.ControlRevoked, tunnel.ControlHookDeleted:
			events = append(events, Event{Kind: EventHookDropped, Hook: s.current[i], Reason: controlDrops[ctl.Type]})
			s.current = slices.Delete(s.current, i, i+1)
		}
	}
	s.mu.Unlock()
	for _, e := range events {
		s.emit(e)
	}
}

var controlDrops = map[tunnel.ControlType]DropReason{
	tunnel.ControlReplaced:    DropReplaced,
	tunnel.ControlRevoked:     DropRevoked,
	tunnel.ControlHookDeleted: DropDeleted,
}

// sameHook reports whether a and b look the same to the people using them.
func sameHook(a, b api.Hook) bool {
	return a.URL == b.URL && a.Path == b.Path && a.Public == b.Public && a.PublicURL == b.PublicURL
}

// dial attaches the current hooks. A dial refused for one of them
// recreates it (a 404 with Recreate set, once per hook and dial) or, on a
// redial, drops it, and dials again. Any other refusal is returned.
func (s *Session) dial(ctx context.Context, _ []string, a tunnel.Attach) (net.Conn, error) {
	recreated := map[string]bool{} // a recreated hook refused again is not recreated again
	for {
		conn, err := tunnel.Dial(ctx, s.API.HTTP, s.API.TunnelURL(a, s.ids()...))
		id, reason, ok := refusal(err)
		if !ok {
			return conn, err
		}
		if err := s.refused(ctx, a.Resume, id, reason, err, recreated); err != nil {
			return nil, err
		}
	}
}

// refused handles a dial refused for hook id: it recreates or drops the
// hook and returns nil to dial again, or returns the error to give up
// with. A first dial (resume false) never drops: those hooks were named on
// purpose, and serving fewer of them quietly would hide that. Holding emitMu
// keeps current from changing while a recreate is under way.
func (s *Session) refused(ctx context.Context, resume bool, id string, reason DropReason, err error, recreated map[string]bool) error {
	s.emitMu.Lock()
	defer s.emitMu.Unlock()
	s.mu.Lock()
	i := slices.IndexFunc(s.current, func(h api.Hook) bool { return h.ID == id })
	var gone api.Hook
	if i >= 0 {
		gone = s.current[i]
	}
	s.mu.Unlock()
	switch {
	case i < 0: // not one of ours: nothing to drop
		return err
	case reason == DropDeleted && recreated[id]: // gone again right away: recreating won't help
		return err
	case reason == DropDeleted && s.Recreate != nil:
		h, err := s.Recreate(ctx, gone)
		if err != nil {
			return err
		}
		recreated[h.ID] = true
		s.mu.Lock()
		s.current[i] = h
		s.mu.Unlock()
		s.emit(Event{Kind: EventHookChanged, Hook: h, OldID: gone.ID, Recreated: true})
		return nil
	case !resume:
		return err
	}
	s.mu.Lock()
	s.current = slices.Delete(s.current, i, i+1)
	left := len(s.current)
	s.mu.Unlock()
	s.emit(Event{Kind: EventHookDropped, Hook: gone, Reason: reason})
	if left == 0 {
		return tunnel.ErrNoHooks
	}
	return nil
}

// refusal returns the hook a tunnel-attach refusal names and what dropping
// it means; ok is false for any other error. Only attach refusals name a
// hook, so a code this client doesn't know, from a newer relay, falls back
// to the HTTP status.
func refusal(err error) (hook string, reason DropReason, ok bool) {
	var p *problem.Problem
	if !errors.As(err, &p) || p.Hook == "" {
		return "", "", false
	}
	switch {
	case p.Code == problem.NotFound, p.Status == http.StatusNotFound:
		return p.Hook, DropDeleted, true
	case p.Code == problem.ChannelForbidden, p.Code == problem.TunnelLimit, p.Code == problem.Forbidden, p.Status == http.StatusForbidden:
		return p.Hook, DropRevoked, true
	case p.Code == problem.Superseded, p.Code == problem.AttachRejected, p.Status == http.StatusConflict:
		return p.Hook, DropReplaced, true
	}
	return "", "", false
}

// updateSeqs remembers the newest hook-updated sequence number per hook,
// because control messages can arrive out of order.
type updateSeqs map[string]uint64

// fresh reports whether an update numbered seq for hook is newer than any
// seen so far, and records it. A relay without sequence numbers sends 0,
// which always counts as fresh.
func (s updateSeqs) fresh(hook string, seq uint64) bool {
	if seq == 0 {
		return true
	}
	if seq <= s[hook] {
		return false
	}
	s[hook] = seq
	return true
}

// observe forgets every sequence number when c is attached: a new
// connection starts its own sequence, and attached precedes any
// hook-updated on it. A number remembered from before a reconnect (e.g. a
// relay restart, whose counter starts over at 0) must not suppress an
// update that is actually new.
func (s updateSeqs) observe(c tunnel.Control) {
	if c.Type == tunnel.ControlAttached {
		clear(s)
	}
}
