package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/Radiergummi/tsrelay/internal/api"
	"github.com/Radiergummi/tsrelay/internal/tunnel"

	"github.com/Radiergummi/tsrelay/internal/problem"
)

func TestRefusal(t *testing.T) {
	cases := map[string]struct {
		err    error
		hook   string
		reason DropReason
		ok     bool
	}{
		"gone":             {&problem.Problem{Status: 404, Hook: "h_1"}, "h_1", DropDeleted, true},
		"channel denied":   {&problem.Problem{Status: 403, Hook: "h_1"}, "h_1", DropRevoked, true},
		"taken over":       {&problem.Problem{Status: 409, Hook: "h_1"}, "h_1", DropReplaced, true},
		"too many tunnels": {&problem.Problem{Status: 403}, "", "", false},
		"bad request":      {&problem.Problem{Status: 400, Hook: "h_1"}, "", "", false},
		"network":          {errors.New("down"), "", "", false},
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

// A dial attaches the hooks tunnel.Client hands it, not the session's own
// list: tunnel.Client drops a hook the relay took away before the session
// hears of it, and a redial in between must not attach it again.
func TestSessionDialsTheTunnelClientsHooks(t *testing.T) {
	var asked []string
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = r.URL.Query()["hook"]
		problem.New(http.StatusServiceUnavailable, "unavailable", "down").Write(w)
	}))
	defer relay.Close()
	s := &Session{API: &API{BaseURL: relay.URL, HTTP: relay.Client()}}
	s.current = []api.Hook{{ID: "h_gone"}, {ID: "h_kept"}}
	s.dial(context.Background(), []string{"h_kept"}, tunnel.Attach{Resume: true})
	if !slices.Equal(asked, []string{"h_kept"}) {
		t.Fatalf("dial attached %v, want [h_kept]", asked)
	}
}
