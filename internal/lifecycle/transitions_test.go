package lifecycle

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Radiergummi/tsrelay/internal/audit"
	"github.com/Radiergummi/tsrelay/internal/events"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/identify"
	"github.com/Radiergummi/tsrelay/internal/inspect"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
)

// hookOf returns the stored hook with the given id; a missing one fails t.
func hookOf(t *testing.T, mgr *hooks.Manager, id hooks.ID) hooks.Hook {
	t.Helper()
	h, ok := mgr.Get(id)
	if !ok {
		t.Fatalf("hook %s missing", id)
	}
	return h
}

// syncBuffer is a log sink that tunnel goroutines may write while the
// test reads.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// observe makes l log its audit records to the returned buffer, and
// returns a channel of its events.
func observe(l *Lifecycle) (*syncBuffer, <-chan events.Event) {
	logs := &syncBuffer{}
	l.cfg.Audit = audit.New(slog.New(slog.NewJSONHandler(logs, nil)))
	l.cfg.Events = &events.Hub{}
	ch, _ := l.cfg.Events.Subscribe(func(events.Event) bool { return true })
	return logs, ch
}

func TestDeleteDetachesAndReleases(t *testing.T) {
	l, mgr := newLifecycle(t)
	l.cfg.Inspect = inspect.New(inspect.Config{})
	logs, evs := observe(l)
	ids := createHooks(t, mgr, 2, hooks.CreateRequest{})
	id, waited := ids[0], ids[1]
	tun, _, controls := controlPair(t)
	admitAttach(t, l, receiver, tun, id)
	next(t, controls) // attached
	next(t, evs)      // the attach's hook event
	l.cfg.Inspect.Add(inspect.Call{ID: "c1", HookID: id})

	// A call held for a hook that has no tunnel is released by the delete.
	held := startWait(context.Background(), l, waited)
	time.Sleep(20 * time.Millisecond) // let it block
	if err := l.Delete(context.Background(), receiver, waited); err != nil {
		t.Fatal(err)
	}
	if res := <-held; !errors.Is(res.err, ErrHookGone) {
		t.Fatalf("held Wait = %+v, want ErrHookGone", res)
	}
	next(t, evs)

	if err := l.Delete(context.Background(), receiver, id); err != nil {
		t.Fatal(err)
	}
	if c := next(t, controls); c.Type != tunnel.ControlHookDeleted || len(c.Hooks) != 1 || c.Hooks[0].ID != string(id) {
		t.Fatalf("control = %+v, want hook-deleted for the hook", c)
	}
	select {
	case <-tun.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the tunnel that served only the deleted hook stayed open")
	}
	if _, ok := l.Lookup(id); ok {
		t.Fatal("Lookup still finds the hook")
	}
	if _, ok := mgr.Get(id); ok {
		t.Fatal("the hook is still stored")
	}
	if calls := l.cfg.Inspect.List(id); len(calls) != 0 {
		t.Fatalf("inspector kept %d calls", len(calls))
	}
	if e := next(t, evs); e.Type != events.TypeHook || e.HookID != string(id) {
		t.Fatalf("event = %+v", e)
	}
	select {
	case e := <-evs:
		t.Fatalf("extra event %+v", e)
	case <-time.After(50 * time.Millisecond):
	}
	if n := strings.Count(logs.String(), `"msg":"hook.delete"`); n != 2 { // one per Delete above
		t.Fatalf("hook.delete audit lines = %d in %s", n, logs.String())
	}
	l.mu.Lock()
	_, stamped := l.detached[id]
	l.mu.Unlock()
	if stamped {
		t.Fatal("a deleted hook keeps its detached-since")
	}
}

// A second delete finds the hook gone: not an error, and it has no effects
// of its own, e.g. no second audit record.
func TestDeleteTwice(t *testing.T) {
	l, mgr := newLifecycle(t)
	logs, _ := observe(l)
	id := createHooks(t, mgr, 1, hooks.CreateRequest{})[0]
	tun, _, controls := controlPair(t)
	admitAttach(t, l, receiver, tun, id)
	next(t, controls) // attached
	for range 2 {
		if err := l.Delete(context.Background(), receiver, id); err != nil {
			t.Fatal(err)
		}
	}
	if c := next(t, controls); c.Type != tunnel.ControlHookDeleted {
		t.Fatalf("control = %+v", c)
	}
	select {
	case c := <-controls:
		t.Fatalf("a second notice was sent: %+v", c)
	case <-time.After(100 * time.Millisecond):
	}
	if n := strings.Count(logs.String(), `"msg":"hook.delete"`); n != 1 {
		t.Fatalf("hook.delete audit lines = %d in %s", n, logs.String())
	}
}

func TestDeleteRacesTunnelEnd(t *testing.T) {
	for range 50 {
		l, mgr := newLifecycle(t)
		id := createHooks(t, mgr, 1, hooks.CreateRequest{})[0]
		h := hookOf(t, mgr, id)
		tun, drop, _ := controlPair(t)
		admitAttach(t, l, receiver, tun, id)
		var wg sync.WaitGroup
		wg.Add(2)
		var err error
		go func() { defer wg.Done(); err = l.Delete(context.Background(), receiver, h.ID) }()
		go func() { defer wg.Done(); drop() }()
		wg.Wait()
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := l.Lookup(id); ok {
			t.Fatal("hook still attached")
		}
		// The tunnel's detach may still be pending; it stamps only hooks the
		// tunnel serves, and the delete took this one.
		l.mu.Lock()
		n := len(l.detached)
		l.mu.Unlock()
		if n != 0 {
			t.Fatalf("detached names %d hooks after the delete", n)
		}
	}
}

func TestRevokeKeepsTakenOverHooks(t *testing.T) {
	l, mgr := newLifecycle(t)
	logs, _ := observe(l)
	ids := createHooks(t, mgr, 2, hooks.CreateRequest{})
	h1, h2 := ids[0], ids[1]
	a, _, ac := controlPair(t)
	b, _, _ := controlPair(t)
	admitAttach(t, l, receiver, a, h1, h2)
	next(t, ac) // attached
	admitAttach(t, l, receiver, b, h2)
	next(t, ac) // replaced [h2]

	l.Revoke(context.Background(), a, []hooks.ID{h1, h2}, "grants changed")
	c := next(t, ac)
	if c.Type != tunnel.ControlRevoked || len(c.Hooks) != 1 || c.Hooks[0].ID != string(h1) {
		t.Fatalf("control = %+v, want revoked [h1]", c)
	}
	select {
	case <-a.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the orphaned tunnel stayed open")
	}
	if cur, ok := l.Lookup(h2); !ok || cur != b {
		t.Fatal("revoke took h2 from the tunnel that took it over")
	}
	if _, ok := l.Lookup(h1); ok {
		t.Fatal("h1 still attached")
	}
	if s := logs.String(); !strings.Contains(s, `"msg":"tunnel.revoked"`) || !strings.Contains(s, `"reason":"grants changed"`) {
		t.Fatalf("audit = %s", s)
	}
	l.mu.Lock()
	_, stamped := l.detached[h1]
	l.mu.Unlock()
	if !stamped {
		t.Fatal("the revoked hook was not stamped detached")
	}
	// Nothing left to revoke: silent.
	l.Revoke(context.Background(), a, []hooks.ID{h1}, "again")
	if strings.Contains(logs.String(), `"reason":"again"`) {
		t.Fatal("a revoke that detached nothing was logged")
	}
}

func TestNotifyUpdatedSequence(t *testing.T) {
	l, mgr := newLifecycle(t)
	id := createHooks(t, mgr, 1, hooks.CreateRequest{})[0]
	tun, _, controls := controlPair(t)
	admitAttach(t, l, receiver, tun, id)
	next(t, controls) // attached
	l.notifyUpdated(id)
	l.notifyUpdated(id)
	c1, c2 := next(t, controls), next(t, controls)
	for _, c := range []tunnel.Control{c1, c2} {
		if c.Type != tunnel.ControlHookUpdated || len(c.Hooks) != 1 || c.Hooks[0].URL != "https://relay"+hookOf(t, mgr, id).Route() {
			t.Fatalf("control = %+v", c)
		}
	}
	if c1.Seq == c2.Seq || c1.Seq == 0 || c2.Seq == 0 {
		t.Fatalf("seqs = %d, %d, want distinct and nonzero", c1.Seq, c2.Seq)
	}
	// Messages may overtake each other; the numbers are what order them.
	if lo, hi := min(c1.Seq, c2.Seq), max(c1.Seq, c2.Seq); hi != lo+1 {
		t.Fatalf("seqs = %d, %d, want consecutive", c1.Seq, c2.Seq)
	}

	if err := l.Delete(context.Background(), receiver, id); err != nil {
		t.Fatal(err)
	}
	next(t, controls) // hook-deleted
	l.notifyUpdated(id)
	select {
	case c := <-controls:
		t.Fatalf("notice for a deleted hook: %+v", c)
	case <-time.After(100 * time.Millisecond):
	}
}

// An update is audited and published, and the tunnel serving the hook
// learns its new URL.
func TestUpdateNotifies(t *testing.T) {
	l, mgr := newLifecycle(t)
	logs, evs := observe(l)
	id := createHooks(t, mgr, 1, hooks.CreateRequest{})[0]
	tun, _, controls := controlPair(t)
	admitAttach(t, l, receiver, tun, id)
	next(t, controls) // attached
	next(t, evs)      // the attach's hook event

	path := "renamed"
	h, changed, err := l.Update(context.Background(), receiver, id, hooks.UpdateRequest{Path: hooks.Opt[string]{Set: true, Value: &path}})
	if err != nil || !slices.Equal(changed, []string{"path"}) {
		t.Fatalf("Update = %v, %v", changed, err)
	}
	if c := next(t, controls); c.Type != tunnel.ControlHookUpdated || c.Hooks[0].URL != "https://relay"+h.Route() {
		t.Fatalf("control = %+v, want hook-updated with %s", c, h.Route())
	}
	if !strings.Contains(logs.String(), `"msg":"hook.update"`) || !strings.Contains(logs.String(), `"changed":["path"]`) {
		t.Fatalf("no hook.update audit line: %s", logs.String())
	}
	if e := next(t, evs); e.Type != events.TypeHook || e.HookID != string(id) {
		t.Fatalf("event = %+v", e)
	}

	// A refused update has no effects.
	if _, _, err := l.Update(context.Background(), receiver, "h_missing", hooks.UpdateRequest{}); !errors.Is(err, hooks.ErrNotFound) {
		t.Fatalf("Update of a missing hook = %v", err)
	}
	if n := strings.Count(logs.String(), `"msg":"hook.update"`); n != 1 {
		t.Fatalf("hook.update audit lines = %d", n)
	}
}

// Rotating a public hook's token tells the tunnel its new public URL.
func TestRotatePublicTokenNotifies(t *testing.T) {
	l, mgr := newLifecycle(t)
	logs, _ := observe(l)
	owner := identify.User("laptop", "moritz@example.com", 1, `{"roles":["receiver"],"public":true}`)
	created, err := mgr.Create(context.Background(), owner, hooks.CreateRequest{Public: true})
	if err != nil {
		t.Fatal(err)
	}
	id, old := created.ID, created.PublicToken
	tun, _, controls := controlPair(t)
	admitAttach(t, l, owner, tun, id)
	next(t, controls) // attached

	h, err := l.RotatePublicToken(context.Background(), owner, id)
	if err != nil || h.PublicToken == old {
		t.Fatalf("RotatePublicToken = %v, %v", h.PublicToken, err)
	}
	if c := next(t, controls); c.Type != tunnel.ControlHookUpdated || c.Hooks[0].PublicURL != "https://public/+"+h.PublicToken {
		t.Fatalf("control = %+v, want hook-updated with the new token", c)
	}
	if !strings.Contains(logs.String(), `"changed":["publicUrl"]`) {
		t.Fatalf("no hook.update audit line: %s", logs.String())
	}
}
