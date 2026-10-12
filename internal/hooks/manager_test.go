package hooks

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Radiergummi/tsrelay/internal/authz"
	"github.com/Radiergummi/tsrelay/internal/identify"
)

var (
	ctx      = context.Background()
	receiver = identify.User("laptop", "moritz@example.com", 1, `{"roles":["receiver"],"channels":["default","prod.*"]}`)
)

func newManager(t *testing.T, dir string) *Manager {
	t.Helper()
	m, err := NewManager(ctx, openStore(t, dir), SlugConfig{})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func fieldErr(t *testing.T, err error, field string) string {
	t.Helper()
	var ve ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("err = %v (%T), want ValidationError", err, err)
	}
	for _, fe := range ve {
		if fe.Field == field {
			return fe.Message
		}
	}
	t.Fatalf("no error for field %q in %v", field, ve)
	return ""
}

func TestCreateDefaults(t *testing.T) {
	m := newManager(t, t.TempDir())
	h, err := m.Create(ctx, receiver, CreateRequest{Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	if h.Slug != "moritz" || h.Channel != "default" || !h.Ephemeral || h.Owner != 1 || h.OwnerLogin != "moritz@example.com" {
		t.Fatalf("hook = %+v", h)
	}
	if ValidatePath(h.Path) != nil || strings.Count(h.Path, "-") != 2 {
		t.Fatalf("random path %q", h.Path)
	}
	if got, ok := m.Get(h.ID); !ok || got != h {
		t.Fatalf("Get = %+v, %v", got, ok)
	}
}

func TestCreateForbidden(t *testing.T) {
	m := newManager(t, t.TempDir())
	tagged := identify.Tagged("w", []string{"tag:x"}, `{"roles":["receiver"]}`)
	sender := identify.User("l", "a@b.c", 2, `{"roles":["sender"]}`)
	for name, id := range map[string]authz.Identity{"tagged": tagged, "no receiver role": sender} {
		if _, err := m.Create(ctx, id, CreateRequest{}); !errors.Is(err, ErrForbidden) {
			t.Errorf("%s: err = %v, want ErrForbidden", name, err)
		}
	}
}

func TestCreateValidation(t *testing.T) {
	m := newManager(t, t.TempDir())
	_, err := m.Create(ctx, receiver, CreateRequest{Channel: "billing"})
	if msg := fieldErr(t, err, "channel"); !strings.Contains(msg, "default, prod.*") {
		t.Errorf("channel message %q should list the receiver's patterns", msg)
	}
	_, err = m.Create(ctx, receiver, CreateRequest{Channel: "Not Valid"})
	fieldErr(t, err, "channel")
	_, err = m.Create(ctx, receiver, CreateRequest{Path: "../x"})
	fieldErr(t, err, "path")
	_, err = m.Create(ctx, receiver, CreateRequest{Description: strings.Repeat("x", 281)})
	fieldErr(t, err, "description")
	if _, err := m.Create(ctx, receiver, CreateRequest{Channel: "prod.eu"}); err != nil {
		t.Fatalf("prod.eu matches prod.*: %v", err)
	}
}

func TestCreateRejectsOverlappingPaths(t *testing.T) {
	m := newManager(t, t.TempDir())
	if _, err := m.Create(ctx, receiver, CreateRequest{Path: "a"}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"a", "a/b"} {
		_, err := m.Create(ctx, receiver, CreateRequest{Path: p})
		if msg := fieldErr(t, err, "path"); !strings.Contains(msg, `"a"`) {
			t.Errorf("path %q: message %q should name the conflicting hook", p, msg)
		}
	}
	if _, err := m.Create(ctx, receiver, CreateRequest{Path: "ab"}); err != nil {
		t.Fatalf("ab does not overlap a: %v", err)
	}
	other := identify.User("l2", "someone@example.com", 9, `{"roles":["receiver"]}`)
	if _, err := m.Create(ctx, other, CreateRequest{Path: "a"}); err != nil {
		t.Fatalf("paths are scoped per user: %v", err)
	}
}

func TestCreateMaxHooks(t *testing.T) {
	m := newManager(t, t.TempDir())
	// Grants can only raise limits above the relay default, so the default applies.
	for i := range authz.DefaultLimits.MaxHooks {
		if _, err := m.Create(ctx, receiver, CreateRequest{}); err != nil {
			t.Fatalf("hook %d: %v", i, err)
		}
	}
	_, err := m.Create(ctx, receiver, CreateRequest{})
	fieldErr(t, err, "")
}

func TestCreateSlugCollision(t *testing.T) {
	m := newManager(t, t.TempDir())
	a := identify.User("l1", "alice@a.com", 10, `{"roles":["receiver"]}`)
	b := identify.User("l2", "alice@b.com", 11, `{"roles":["receiver"]}`)
	if _, err := m.Create(ctx, a, CreateRequest{}); err != nil {
		t.Fatal(err)
	}
	_, err := m.Create(ctx, b, CreateRequest{})
	if msg := fieldErr(t, err, "owner"); !strings.Contains(msg, "~alice") || !strings.Contains(msg, "override") {
		t.Errorf("slug collision message %q", msg)
	}
}

func TestRoute(t *testing.T) {
	m := newManager(t, t.TempDir())
	h, _ := m.Create(ctx, receiver, CreateRequest{Path: "cb/v1"})
	cases := []struct {
		slug, rest string
		ok         bool
	}{
		{"moritz", "cb/v1", true},
		{"moritz", "cb/v1/", true},
		{"moritz", "cb/v1/done/now", true},
		{"moritz", "cb/v10", false},
		{"moritz", "cb", false},
		{"someone", "cb/v1", false},
	}
	for _, c := range cases {
		got, segs, ok := m.Route(c.slug, c.rest)
		if ok != c.ok || (ok && (got.ID != h.ID || segs != 2)) {
			t.Errorf("Route(%q, %q) = %v, %d, %v", c.slug, c.rest, got.ID, segs, ok)
		}
	}
}

func TestReap(t *testing.T) {
	m := newManager(t, t.TempDir())
	now := time.Unix(1000, 0)
	m.now = func() time.Time { return now }
	eph, _ := m.Create(ctx, receiver, CreateRequest{Path: "eph", Ephemeral: true})
	att, _ := m.Create(ctx, receiver, CreateRequest{Path: "att", Ephemeral: true})
	per, _ := m.Create(ctx, receiver, CreateRequest{Path: "per"})
	m.SetAttached(att.ID, true)

	now = now.Add(9 * time.Minute)
	if reaped, _ := m.Reap(ctx, 10*time.Minute); len(reaped) != 0 {
		t.Fatalf("reaped %v before grace elapsed", reaped)
	}
	now = now.Add(2 * time.Minute)
	reaped, err := m.Reap(ctx, 10*time.Minute)
	if err != nil || len(reaped) != 1 || reaped[0].ID != eph.ID {
		t.Fatalf("Reap = %v, %v; want only %s", reaped, err, eph.ID)
	}
	if _, ok := m.Get(per.ID); !ok {
		t.Fatal("persistent hook must not be reaped")
	}

	m.SetAttached(att.ID, false)
	now = now.Add(11 * time.Minute)
	if reaped, _ := m.Reap(ctx, 10*time.Minute); len(reaped) != 1 || reaped[0].ID != att.ID {
		t.Fatalf("detached hook not reaped: %v", reaped)
	}
}

// After a relay restart, ephemeral hooks get a full grace period again
// instead of being reaped immediately.
func TestReloadRestartsGrace(t *testing.T) {
	dir := t.TempDir()
	s1, _ := OpenJSON(dir)
	m1, _ := NewManager(ctx, s1, SlugConfig{})
	h, _ := m1.Create(ctx, receiver, CreateRequest{Ephemeral: true})
	s1.Close()

	m2 := newManager(t, dir)
	if got, ok := m2.Get(h.ID); !ok || got.Path != h.Path {
		t.Fatalf("hook lost on reload: %+v %v", got, ok)
	}
	if reaped, _ := m2.Reap(ctx, time.Minute); len(reaped) != 0 {
		t.Fatalf("reaped %v right after restart", reaped)
	}
}

func TestDelete(t *testing.T) {
	m := newManager(t, t.TempDir())
	h, _ := m.Create(ctx, receiver, CreateRequest{Path: "x"})
	if err := m.Delete(ctx, h.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := m.Route("moritz", "x"); ok {
		t.Fatal("deleted hook still routes")
	}
	if err := m.Delete(ctx, h.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
}
