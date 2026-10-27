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
	"github.com/Radiergummi/tsrelay/internal/authz"
	"github.com/Radiergummi/tsrelay/internal/events"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/identify"
	"github.com/Radiergummi/tsrelay/internal/inspect"
	"github.com/Radiergummi/tsrelay/internal/problem"
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
	if c := next(t, controls); c.Type != tunnel.ControlHookUpdated || len(c.Hooks) != 1 || c.Hooks[0].URL != "https://relay"+h.Route() {
		t.Fatalf("control = %+v, want hook-updated with %s", c, h.Route())
	}
	if !strings.Contains(logs.String(), `"msg":"hook.update"`) || !strings.Contains(logs.String(), `"changed":["path"]`) {
		t.Fatalf("no hook.update audit line: %s", logs.String())
	}
	if e := next(t, evs); e.Type != events.TypeHook || e.HookID != string(id) {
		t.Fatalf("event = %+v", e)
	}

	// A refused or empty update has no effects.
	if _, _, err := l.Update(context.Background(), receiver, "h_missing", hooks.UpdateRequest{}); !errors.Is(err, hooks.ErrNotFound) {
		t.Fatalf("Update of a missing hook = %v", err)
	}
	if _, changed, err := l.Update(context.Background(), receiver, id, hooks.UpdateRequest{}); err != nil || len(changed) != 0 {
		t.Fatalf("empty Update = %v, %v", changed, err)
	}
	var ve problem.FieldErrors
	if _, _, err := l.Update(context.Background(), receiver, id, hooks.UpdateRequest{Path: hooks.Opt[string]{Set: true}}); !errors.As(err, &ve) {
		t.Fatalf("Update with a null path = %v", err)
	}
	if n := strings.Count(logs.String(), `"msg":"hook.update"`); n != 1 {
		t.Fatalf("hook.update audit lines = %d", n)
	}
	select {
	case e := <-evs:
		t.Fatalf("event for a refused update: %+v", e)
	default:
	}
	select {
	case c := <-controls:
		t.Fatalf("notice for a refused update: %+v", c)
	case <-time.After(100 * time.Millisecond):
	}
}

// Rotating a public hook's token tells the tunnel its new public URL.
func TestRotatePublicTokenNotifies(t *testing.T) {
	l, mgr := newLifecycle(t)
	logs, evs := observe(l)
	owner := identify.User("laptop", "moritz@example.com", 1, `{"roles":["receiver"],"public":true}`)
	created, err := mgr.Create(context.Background(), owner, hooks.CreateRequest{Public: true})
	if err != nil {
		t.Fatal(err)
	}
	id, old := created.ID, created.PublicToken
	tun, _, controls := controlPair(t)
	admitAttach(t, l, owner, tun, id)
	next(t, controls) // attached
	next(t, evs)      // the attach's hook event

	h, err := l.RotatePublicToken(context.Background(), owner, id)
	if err != nil || h.PublicToken == old {
		t.Fatalf("RotatePublicToken = %v, %v", h.PublicToken, err)
	}
	if c := next(t, controls); c.Type != tunnel.ControlHookUpdated || len(c.Hooks) != 1 || c.Hooks[0].PublicURL != "https://public/+"+h.PublicToken {
		t.Fatalf("control = %+v, want hook-updated with the new token", c)
	}
	if !strings.Contains(logs.String(), `"changed":["publicUrl"]`) {
		t.Fatalf("no hook.update audit line: %s", logs.String())
	}
	if e := next(t, evs); e.Type != events.TypeHook || e.HookID != string(id) {
		t.Fatalf("event = %+v", e)
	}
}

// A delete that races an update is announced after it, so the audit log and
// the hook events never show an update of a deleted hook.
func TestDeleteWaitsForUpdateAnnouncement(t *testing.T) {
	l, mgr := newLifecycle(t)
	logs, _ := observe(l)
	id := createHooks(t, mgr, 1, hooks.CreateRequest{})[0]
	announcing, release := make(chan struct{}), make(chan struct{})
	l.cfg.Audit.Observer = func(event string, _ []slog.Attr) {
		if event == audit.HookUpdate {
			close(announcing)
			<-release
		}
	}

	path := "renamed"
	updated := make(chan error)
	go func() {
		_, _, err := l.Update(context.Background(), receiver, id, hooks.UpdateRequest{Path: hooks.Opt[string]{Set: true, Value: &path}})
		updated <- err
	}()
	<-announcing
	deleted := make(chan error)
	go func() { deleted <- l.Delete(context.Background(), receiver, id) }()
	select {
	case err := <-deleted:
		t.Fatalf("Delete returned (%v) while the update was still being announced", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-updated; err != nil {
		t.Fatal(err)
	}
	if err := <-deleted; err != nil {
		t.Fatal(err)
	}
	if u, d := strings.Index(logs.String(), `"msg":"hook.update"`), strings.Index(logs.String(), `"msg":"hook.delete"`); u < 0 || d < u {
		t.Fatalf("audit order wrong: %s", logs.String())
	}
}

// Creating a hook is audited and published; a refused create is not.
func TestCreateAnnounces(t *testing.T) {
	l, _ := newLifecycle(t)
	logs, evs := observe(l)
	h, err := l.Create(context.Background(), receiver, hooks.CreateRequest{Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), `"msg":"hook.create"`) || !strings.Contains(logs.String(), `"ephemeral":true`) {
		t.Fatalf("no hook.create audit line: %s", logs.String())
	}
	if e := next(t, evs); e.Type != events.TypeHook || e.HookID != string(h.ID) {
		t.Fatalf("event = %+v", e)
	}

	tagged := identify.Tagged("ci", []string{"tag:ci"}, `{"roles":["receiver"]}`)
	if _, err := l.Create(context.Background(), tagged, hooks.CreateRequest{}); !errors.Is(err, hooks.ErrForbidden) {
		t.Fatalf("Create by a tagged node = %v", err)
	}
	if n := strings.Count(logs.String(), `"msg":"hook.create"`); n != 1 {
		t.Fatalf("hook.create audit lines = %d", n)
	}
	select {
	case e := <-evs:
		t.Fatalf("event for a refused create: %+v", e)
	default:
	}
}

var devOnly = identify.User("laptop", "moritz@example.com", 1, `{"roles":["receiver"],"channels":["dev"]}`)

// recheckEnv attaches a tunnel from node to two hooks in "dev" and returns
// them with the client's control messages.
func recheckEnv(t *testing.T, node authz.Identity) (*Lifecycle, *syncBuffer, *tunnel.Tunnel, []hooks.ID, chan tunnel.Control) {
	t.Helper()
	l, mgr := newLifecycle(t)
	logs, _ := observe(l)
	ids := createHooks(t, mgr, 2, hooks.CreateRequest{Channel: "dev"})
	tun, _, controls := controlPair(t)
	admitAttach(t, l, node, tun, ids...)
	next(t, controls) // attached
	return l, logs, tun, ids, controls
}

// byType receives n control messages; the lifecycle sends them concurrently.
func byType(t *testing.T, controls chan tunnel.Control, n int) map[tunnel.ControlType][]tunnel.HookInfo {
	t.Helper()
	got := map[tunnel.ControlType][]tunnel.HookInfo{}
	for range n {
		c := next(t, controls)
		got[c.Type] = c.Hooks
	}
	return got
}

func noControl(t *testing.T, controls chan tunnel.Control) {
	t.Helper()
	select {
	case c := <-controls:
		t.Fatalf("unexpected control %+v", c)
	case <-time.After(100 * time.Millisecond):
	}
}

func moveTo(t *testing.T, l *Lifecycle, id hooks.ID, channel string) {
	t.Helper()
	if _, _, err := l.Update(context.Background(), receiver, id, hooks.UpdateRequest{Channel: hooks.Opt[string]{Set: true, Value: &channel}}); err != nil {
		t.Fatal(err)
	}
}

// A channel change made from one node revokes the hook from the tunnel of
// another node that may not receive the new channel; the tunnel's other
// hooks stay.
func TestChannelChangeRevokesServingTunnel(t *testing.T) {
	l, logs, tun, ids, controls := recheckEnv(t, receiver)
	l.cfg.Identify = func(context.Context, string) (authz.Identity, error) { return devOnly, nil }
	moveTo(t, l, ids[1], "prod")

	if hs := byType(t, controls, 2)[tunnel.ControlRevoked]; len(hs) != 1 || hs[0].ID != string(ids[1]) {
		t.Fatalf("revoked = %+v, want the moved hook", hs)
	}
	if _, ok := l.Lookup(ids[1]); ok {
		t.Fatal("the moved hook stayed attached")
	}
	if cur, ok := l.Lookup(ids[0]); !ok || cur != tun {
		t.Fatal("the tunnel's other hook was detached")
	}
	if !strings.Contains(logs.String(), `"msg":"tunnel.revoked"`) || !strings.Contains(logs.String(), `"reason":"the grants no longer cover the hook's channel"`) {
		t.Fatalf("no tunnel.revoked audit line with the channel reason: %s", logs.String())
	}
	if g := tun.Identity().Grants; !slices.Equal(g.Patterns[authz.RoleReceiver], []string{"dev"}) {
		t.Fatalf("tunnel grants = %+v, want the re-identified ones", g)
	}
}

// When the tunnel's node cannot be identified, its last known grants decide.
func TestRecheckFallsBackToLastGrants(t *testing.T) {
	fail := func(context.Context, string) (authz.Identity, error) {
		return authz.Identity{}, errors.New("whois failed")
	}

	l, _, _, ids, controls := recheckEnv(t, devOnly)
	l.cfg.Identify = fail
	moveTo(t, l, ids[1], "prod")
	if hs := byType(t, controls, 2)[tunnel.ControlRevoked]; len(hs) != 1 || hs[0].ID != string(ids[1]) {
		t.Fatalf("revoked = %+v, want the moved hook", hs)
	}

	l, _, tun, ids, controls := recheckEnv(t, receiver)
	l.cfg.Identify = fail
	moveTo(t, l, ids[1], "prod")
	if c := next(t, controls); c.Type != tunnel.ControlHookUpdated {
		t.Fatalf("control = %+v, want only hook-updated", c)
	}
	noControl(t, controls)
	if cur, ok := l.Lookup(ids[1]); !ok || cur != tun {
		t.Fatal("a hook the last known grants cover was detached")
	}
}

// Losing permission to serve a hook on the internet never revokes it from
// the tailnet.
func TestPublicLossKeepsTunnel(t *testing.T) {
	l, _, tun, ids, controls := recheckEnv(t, devOnly)
	l.cfg.Identify = func(context.Context, string) (authz.Identity, error) { return devOnly, nil }
	publisher := identify.User("desk", "moritz@example.com", 1, `{"roles":["receiver"],"public":true}`)
	if _, _, err := l.Update(context.Background(), publisher, ids[0], hooks.UpdateRequest{Public: hooks.Opt[bool]{Set: true, Value: new(true)}}); err != nil {
		t.Fatal(err)
	}
	if c := next(t, controls); c.Type != tunnel.ControlHookUpdated {
		t.Fatalf("control = %+v, want only hook-updated", c)
	}
	noControl(t, controls)
	if cur, ok := l.Lookup(ids[0]); !ok || cur != tun {
		t.Fatal("the hook was detached")
	}
}

// Delete decides on the stored hook: an admin scoped to prod whose check
// passed while the hook was in prod cannot delete it once it moved to dev,
// and is told it is not found.
func TestDeleteRechecksStoredHook(t *testing.T) {
	l, mgr := newLifecycle(t)
	prodAdmin := identify.User("ops", "ops@example.com", 2, `{"roles":["admin"],"channels":["prod"]}`)
	id := createHooks(t, mgr, 1, hooks.CreateRequest{Channel: "dev"})[0]
	if err := l.Delete(context.Background(), prodAdmin, id); !errors.Is(err, hooks.ErrNotFound) {
		t.Fatalf("Delete = %v, want ErrNotFound", err)
	}
	hookOf(t, mgr, id)
}

// A channel change is announced in both channels, so admins scoped to the
// old one learn the hook left it.
func TestUpdateAnnouncesOldChannel(t *testing.T) {
	l, mgr := newLifecycle(t)
	_, evs := observe(l)
	id := createHooks(t, mgr, 1, hooks.CreateRequest{Channel: "dev"})[0]
	prod := "prod"
	if _, _, err := l.Update(context.Background(), receiver, id, hooks.UpdateRequest{Channel: hooks.Opt[string]{Set: true, Value: &prod}}); err != nil {
		t.Fatal(err)
	}
	var got []string
	for range 2 {
		e := next(t, evs)
		if e.Type != events.TypeHook || e.HookID != string(id) {
			t.Fatalf("event = %+v", e)
		}
		got = append(got, e.Channel)
	}
	slices.Sort(got)
	if !slices.Equal(got, []string{"dev", "prod"}) {
		t.Fatalf("hook events in channels %v, want dev and prod", got)
	}
}
