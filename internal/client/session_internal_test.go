package client

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/Radiergummi/tsrelay/internal/api"
	"github.com/Radiergummi/tsrelay/internal/problem"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
)

func TestRefusalByCode(t *testing.T) {
	cases := map[string]struct {
		err    error
		hook   string
		reason DropReason
		ok     bool
	}{
		"gone":           {&problem.Problem{Status: 404, Code: problem.NotFound, Hook: "h_1"}, "h_1", DropDeleted, true},
		"channel denied": {&problem.Problem{Status: 403, Code: problem.ChannelForbidden, Hook: "h_1"}, "h_1", DropRevoked, true},
		"forbidden":      {&problem.Problem{Status: 403, Code: problem.Forbidden, Hook: "h_1"}, "h_1", DropRevoked, true},
		"tunnel limit":   {&problem.Problem{Status: 403, Code: problem.TunnelLimit, Hook: "h_1"}, "h_1", DropRevoked, true},
		"taken over":     {&problem.Problem{Status: 409, Code: problem.Superseded, Hook: "h_1"}, "h_1", DropReplaced, true},
		"rejected":       {&problem.Problem{Status: 409, Code: problem.AttachRejected, Hook: "h_1"}, "h_1", DropReplaced, true},
		"no hook named":  {&problem.Problem{Status: 403, Code: problem.TunnelLimit}, "", "", false},
		"bad request":    {&problem.Problem{Status: 400, Code: problem.BadRequest, Hook: "h_1"}, "", "", false},
		"newer 404 code": {&problem.Problem{Status: 404, Code: "from_a_newer_relay", Hook: "h_1"}, "h_1", DropDeleted, true},
		"newer 403 code": {&problem.Problem{Status: 403, Code: "from_a_newer_relay", Hook: "h_1"}, "h_1", DropRevoked, true},
		"newer 409 code": {&problem.Problem{Status: 409, Code: "from_a_newer_relay", Hook: "h_1"}, "h_1", DropReplaced, true},
		"network":        {errors.New("down"), "", "", false},
	}
	for name, c := range cases {
		hook, reason, ok := refusal(c.err)
		if hook != c.hook || reason != c.reason || ok != c.ok {
			t.Errorf("%s: refusal = %q, %q, %v; want %q, %q, %v", name, hook, reason, ok, c.hook, c.reason, c.ok)
		}
	}
}

func TestUpdateSeqs(t *testing.T) {
	s := updateSeqs{}
	if !s.fresh("h_1", 2) {
		t.Fatal("first update must be fresh")
	}
	if s.fresh("h_1", 1) || s.fresh("h_1", 2) {
		t.Fatal("an older or repeated update must not be fresh")
	}
	if !s.fresh("h_2", 1) {
		t.Fatal("sequence numbers are tracked per hook")
	}
	if !s.fresh("h_1", 0) || !s.fresh("h_1", 0) {
		t.Fatal("0 (a relay without sequence numbers) is always fresh")
	}
}

// An attached message overtaken by a newer hook-updated must not undo it.
func TestSessionIgnoresStaleAttached(t *testing.T) {
	var events []Event
	s := &Session{OnEvent: func(e Event) { events = append(events, e) }}
	s.current = []api.Hook{{ID: "h_1", Path: "pay"}}
	s.seqs = updateSeqs{}
	s.control(tunnel.Control{Type: tunnel.ControlHookUpdated, Seq: 5, Hooks: []tunnel.HookInfo{{ID: "h_1", Path: "billing"}}})
	s.control(tunnel.Control{Type: tunnel.ControlAttached, Seq: 4, Hooks: []tunnel.HookInfo{{ID: "h_1", Path: "pay"}}})
	// Unnumbered, from a v1.0.0 relay: no telling whether it is newer.
	s.control(tunnel.Control{Type: tunnel.ControlAttached, Hooks: []tunnel.HookInfo{{ID: "h_1", Path: "pay"}}})
	if cur := s.Current(); len(cur) != 1 || cur[0].Path != "billing" || len(events) != 1 {
		t.Fatalf("Current = %+v, events = %+v", cur, events)
	}
}

// TestSessionIgnoresUnknownControl pins the v1 rule that a client skips
// control types and fields it does not know, so the relay can add them.
func TestSessionIgnoresUnknownControl(t *testing.T) {
	var events []Event
	s := &Session{OnEvent: func(e Event) { events = append(events, e) }}
	s.current = []api.Hook{{ID: "h_1"}, {ID: "h_2"}}
	s.seqs = updateSeqs{}

	cc, sc := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tunnel.Serve(ctx, sc, http.NotFoundHandler(), s.control)
	var h2 http.Protocols
	h2.SetUnencryptedHTTP2(true)
	tr := &http.Transport{Protocols: &h2, DialContext: func(context.Context, string, string) (net.Conn, error) { return cc, nil }}
	conn, err := tr.NewClientConn(ctx, "http", "tunnel:80")
	if err != nil {
		t.Fatal(err)
	}
	post := func(body string) {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, "http://tunnel/", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set(tunnel.HeaderControl, "1")
		resp, err := conn.RoundTrip(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("%s: status %d, want 204", body, resp.StatusCode)
		}
	}

	post(`{"type":"from-the-future","hooks":[{"id":"h_1"}]}`)
	post(`{"type":"attached","hooks":[{"id":"h_1","later":true}],"later":1}`)
	if len(events) != 0 || len(s.Current()) != 2 {
		t.Fatalf("unknown control changed the session: events %+v, current %+v", events, s.Current())
	}

	post(`{"type":"hook-deleted","hooks":[{"id":"h_1","later":true}],"later":1}`)
	if len(events) != 1 || events[0].Kind != EventHookDropped || events[0].Hook.ID != "h_1" || events[0].Reason != DropDeleted {
		t.Fatalf("events = %+v, want h_1 dropped as deleted", events)
	}
	if cur := s.Current(); len(cur) != 1 || cur[0].ID != "h_2" {
		t.Fatalf("Current = %+v", cur)
	}
}
