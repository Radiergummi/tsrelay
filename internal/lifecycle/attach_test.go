package lifecycle

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Radiergummi/tsrelay/internal/audit"
	"github.com/Radiergummi/tsrelay/internal/authz"
	"github.com/Radiergummi/tsrelay/internal/events"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/identify"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
	"github.com/Radiergummi/tsrelay/internal/tunnel/tunneltest"
)

func next[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(5 * time.Second):
		t.Fatal("timed out")
		panic("unreachable")
	}
}

// controlPair opens a tunnel whose client reports its control messages.
func controlPair(t *testing.T) (*tunnel.Tunnel, func(), chan tunnel.Control) {
	t.Helper()
	controls := make(chan tunnel.Control, 8)
	tun, drop := tunneltest.Pair(t, http.NotFoundHandler(), tunnel.Options{}, func(c tunnel.Control) { controls <- c })
	return tun, drop, controls
}

// admitAttach admits ids for owner and attaches tun to them.
func admitAttach(t *testing.T, l *Lifecycle, owner authz.Identity, tun *tunnel.Tunnel, ids ...hooks.ID) {
	t.Helper()
	tk, err := l.Admit(context.Background(), owner, ids, "", false)
	if err != nil {
		t.Fatal(err)
	}
	tk.Attach(context.Background(), tun, nil)
}

func TestAdmitChecks(t *testing.T) {
	l, mgr := newLifecycle(t)
	var logs bytes.Buffer
	l.cfg.Audit = audit.New(slog.New(slog.NewJSONHandler(&logs, nil)))
	ctx := context.Background()
	other := identify.User("desk", "other@example.com", 2, `{"roles":["receiver"]}`)
	devOnly := identify.User("laptop", "moritz@example.com", 1, `{"roles":["receiver"],"channels":["dev"]}`)

	mine := createHooks(t, mgr, 2)
	theirs, err := mgr.Create(ctx, other, hooks.CreateRequest{})
	if err != nil {
		t.Fatal(err)
	}
	ops, err := mgr.Create(ctx, receiver, hooks.CreateRequest{Channel: "ops"})
	if err != nil {
		t.Fatal(err)
	}
	reject := hooks.AttachReject
	rejecting, err := mgr.Create(ctx, receiver, hooks.CreateRequest{Settings: hooks.Settings{AttachPolicy: &reject}})
	if err != nil {
		t.Fatal(err)
	}
	served := &tunnel.Tunnel{ID: "served", Session: "s1", Owner: receiver}
	l.attach(served, mine[0], rejecting.ID)
	limit := receiver.Grants.Limits.MaxTunnels
	for i, id := range createHooks(t, mgr, limit-1) {
		l.attach(&tunnel.Tunnel{ID: fmt.Sprint("more", i), Owner: receiver}, id)
	}

	cases := []struct {
		name    string
		owner   authz.Identity
		ids     []hooks.ID
		session string
		resume  bool
		want    AdmitError
	}{
		{"missing", receiver, []hooks.ID{"h_missing"}, "", false, AdmitError{Kind: HookNotFound, Hook: "h_missing"}},
		{"not the owner's", receiver, []hooks.ID{theirs.ID}, "", false, AdmitError{Kind: HookNotFound, Hook: theirs.ID}},
		{"channel", devOnly, []hooks.ID{ops.ID}, "", false, AdmitError{Kind: ChannelDenied, Hook: ops.ID, Channel: "ops"}},
		{"resume elsewhere", receiver, []hooks.ID{mine[0]}, "s2", true, AdmitError{Kind: Superseded, Hook: mine[0]}},
		{"reject", receiver, []hooks.ID{rejecting.ID}, "s2", false, AdmitError{Kind: Rejected, Hook: rejecting.ID}},
		{"reject without session", receiver, []hooks.ID{rejecting.ID}, "", false, AdmitError{Kind: Rejected, Hook: rejecting.ID}},
		{"maxTunnels", receiver, []hooks.ID{mine[1]}, "", false, AdmitError{Kind: TooManyTunnels, Have: limit, Limit: limit}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tk, err := l.Admit(ctx, c.owner, c.ids, c.session, c.resume)
			var ae *AdmitError
			if !errors.As(err, &ae) || *ae != c.want {
				t.Fatalf("Admit = %v, %v; want %+v", tk, err, c.want)
			}
		})
	}
	if !strings.Contains(logs.String(), `"msg":"authz.deny"`) || !strings.Contains(logs.String(), `"reason":"maxTunnels"`) {
		t.Fatalf("no maxTunnels denial in the audit log: %s", logs.String())
	}

	// The serving session may replace its own tunnel, even under "reject".
	tk, err := l.Admit(ctx, receiver, []hooks.ID{rejecting.ID, mine[0]}, "s1", true)
	if err != nil {
		t.Fatalf("Admit by the serving session = %v", err)
	}
	if got := tk.HookIDs(); !slices.Equal(got, []hooks.ID{rejecting.ID, mine[0]}) {
		t.Fatalf("HookIDs = %v", got)
	}
	tk.Close()
}

func TestAttachNotifiesAndDetaches(t *testing.T) {
	l, mgr := newLifecycle(t)
	l.cfg.Events = &events.Hub{}
	evs, stop := l.cfg.Events.Subscribe(func(events.Event) bool { return true })
	defer stop()
	ids := createHooks(t, mgr, 2)
	tun, drop, controls := controlPair(t)

	tk, err := l.Admit(context.Background(), receiver, ids, "", false)
	if err != nil {
		t.Fatal(err)
	}
	var ended atomic.Int32
	tk.Attach(context.Background(), tun, func(time.Duration) { ended.Add(1) })

	c := next(t, controls)
	if c.Type != tunnel.ControlAttached || len(c.Hooks) != 2 {
		t.Fatalf("control = %+v, want attached for both hooks", c)
	}
	for i, info := range c.Hooks {
		h, _ := mgr.Get(ids[i])
		if info.ID != string(ids[i]) || info.URL != "https://relay"+h.Route() {
			t.Errorf("attached hook %d = %+v", i, info)
		}
	}
	for _, id := range ids {
		if got, ok := l.Lookup(id); !ok || got != tun {
			t.Fatalf("Lookup(%s) = %v, %v", id, got, ok)
		}
	}
	for i := range 2 {
		if e := next(t, evs); e.HookID != string(ids[i]) {
			t.Fatalf("attach event %d = %+v", i, e)
		}
	}

	drop()
	for i := range 2 {
		if e := next(t, evs); e.HookID != string(ids[i]) {
			t.Fatalf("detach event %d = %+v", i, e)
		}
	}
	if _, ok := l.Lookup(ids[0]); ok {
		t.Fatal("hook still attached after its tunnel ended")
	}
	deadline := time.Now().Add(5 * time.Second)
	for ended.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	if n := ended.Load(); n != 1 {
		t.Fatalf("onEnd called %d times, want 1", n)
	}
	l.mu.Lock()
	_, stamped := l.detached[ids[0]]
	l.mu.Unlock()
	if !stamped {
		t.Fatal("no detached-since after the tunnel ended")
	}

	// Attaching again clears it.
	again, _ := tunneltest.Pair(t, http.NotFoundHandler(), tunnel.Options{}, nil)
	admitAttach(t, l, receiver, again, ids[0])
	l.mu.Lock()
	_, stamped = l.detached[ids[0]]
	l.mu.Unlock()
	if stamped {
		t.Fatal("detached-since left after an attach")
	}
}

func TestAttachTakeover(t *testing.T) {
	l, mgr := newLifecycle(t)
	ids := createHooks(t, mgr, 2)
	old, _, controls := controlPair(t)
	admitAttach(t, l, receiver, old, ids...)
	next(t, controls) // attached

	new1, _ := tunneltest.Pair(t, http.NotFoundHandler(), tunnel.Options{}, nil)
	admitAttach(t, l, receiver, new1, ids[0])
	c := next(t, controls)
	if c.Type != tunnel.ControlReplaced || len(c.Hooks) != 1 || c.Hooks[0].ID != string(ids[0]) {
		t.Fatalf("control = %+v, want replaced for the first hook", c)
	}
	select {
	case <-old.Done():
		t.Fatal("a tunnel that still serves a hook was closed")
	case <-time.After(50 * time.Millisecond):
	}

	new2, _ := tunneltest.Pair(t, http.NotFoundHandler(), tunnel.Options{}, nil)
	admitAttach(t, l, receiver, new2, ids[1])
	if c := next(t, controls); c.Type != tunnel.ControlReplaced || len(c.Hooks) != 1 || c.Hooks[0].ID != string(ids[1]) {
		t.Fatalf("control = %+v, want replaced for the last hook", c)
	}
	select {
	case <-old.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the orphaned tunnel was not closed")
	}
}

// A hook deleted between Admit and Attach must not end up attached
// (plan 1 minor 8).
func TestAttachRechecksDeletedHooks(t *testing.T) {
	l, mgr := newLifecycle(t)
	ids := createHooks(t, mgr, 2)
	keep, gone := ids[0], ids[1]
	tk, err := l.Admit(context.Background(), receiver, []hooks.ID{keep, gone}, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.Delete(context.Background(), gone); err != nil {
		t.Fatal(err)
	}

	tun, _, controls := controlPair(t)
	tk.Attach(context.Background(), tun, nil)
	if _, ok := l.Lookup(gone); ok {
		t.Fatal("the deleted hook stayed attached")
	}
	if cur, ok := l.Lookup(keep); !ok || cur != tun {
		t.Fatal("the remaining hook was not attached")
	}
	got := map[tunnel.ControlType][]tunnel.HookInfo{}
	for range 2 {
		c := next(t, controls)
		got[c.Type] = c.Hooks
	}
	if hs := got[tunnel.ControlAttached]; len(hs) != 1 || hs[0].ID != string(keep) {
		t.Errorf("attached = %+v, want only the remaining hook", hs)
	}
	if hs := got[tunnel.ControlHookDeleted]; len(hs) != 1 || hs[0].ID != string(gone) {
		t.Errorf("hook-deleted = %+v, want the deleted hook", hs)
	}

	// A tunnel left with no hooks at all is closed.
	other := createHooks(t, mgr, 1)[0]
	tk, err = l.Admit(context.Background(), receiver, []hooks.ID{other}, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.Delete(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	tun2, _ := tunneltest.Pair(t, http.NotFoundHandler(), tunnel.Options{}, nil)
	tk.Attach(context.Background(), tun2, nil)
	select {
	case <-tun2.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("a tunnel whose only hook was deleted was not closed")
	}
	if _, ok := l.Lookup(other); ok {
		t.Fatal("the deleted hook stayed attached")
	}
}

func TestTicketCloseReleasesLock(t *testing.T) {
	l, mgr := newLifecycle(t)
	ids := createHooks(t, mgr, 1)
	tk, err := l.Admit(context.Background(), receiver, ids, "", false)
	if err != nil {
		t.Fatal(err)
	}
	tk.Close()
	tk.Close()
	done := make(chan *Ticket)
	go func() {
		tk, err := l.Admit(context.Background(), receiver, ids, "", false)
		if err != nil {
			t.Error(err)
		}
		done <- tk
	}()
	select {
	case tk := <-done:
		tk.Close()
	case <-time.After(time.Second):
		t.Fatal("Admit blocked after Close")
	}
}
