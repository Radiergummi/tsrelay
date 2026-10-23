package hooks

import (
	"context"
	"encoding/json"
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

// DeleteIf decides on the hook as stored, and keeps it when cond fails.
func TestDeleteIf(t *testing.T) {
	m := newManager(t, t.TempDir())
	h, _ := m.Create(ctx, receiver, CreateRequest{Path: "x", Ephemeral: true})
	no := false
	if _, _, err := m.Update(ctx, receiver, h.ID, UpdateRequest{Ephemeral: Opt[bool]{Set: true, Value: &no}}); err != nil {
		t.Fatal(err)
	}
	ephemeral := func(h Hook) bool { return h.Ephemeral }
	if cur, ok, err := m.DeleteIf(ctx, h.ID, ephemeral); err != nil || ok || cur.Ephemeral {
		t.Fatalf("DeleteIf on a persistent hook = %+v, %v, %v", cur, ok, err)
	}
	if _, ok := m.Get(h.ID); !ok {
		t.Fatal("DeleteIf removed a hook cond rejected")
	}
	if cur, ok, err := m.DeleteIf(ctx, h.ID, func(Hook) bool { return true }); err != nil || !ok || cur.ID != h.ID {
		t.Fatalf("DeleteIf = %+v, %v, %v", cur, ok, err)
	}
	if _, _, ok := m.Route("moritz", "x"); ok {
		t.Fatal("deleted hook still routes")
	}
	if _, ok, err := m.DeleteIf(ctx, h.ID, func(Hook) bool { return true }); ok || !errors.Is(err, ErrNotFound) {
		t.Fatalf("DeleteIf on a missing hook = %v, %v", ok, err)
	}
}

func TestCreateWithSettingsAndExpiry(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir)
	now := time.Unix(1000, 0).UTC()
	m.now = func() time.Time { return now }
	exp := now.Add(time.Hour)
	h, err := m.Create(ctx, receiver, CreateRequest{
		Path:      "s",
		Settings:  Settings{HoldTimeout: new(Duration(5 * time.Second)), HostHeader: new(HostRewrite)},
		ExpiresAt: &exp,
	})
	if err != nil {
		t.Fatal(err)
	}
	eff := m.Effective(h)
	if eff.HoldTimeout != Duration(5*time.Second) || eff.HostHeader != HostRewrite || eff.ResponseTimeout != DefaultPolicy.Defaults.ResponseTimeout {
		t.Fatalf("effective = %+v", eff)
	}

	_, err = m.Create(ctx, receiver, CreateRequest{Path: "bad", Settings: Settings{HoldTimeout: new(Duration(time.Hour))}})
	fieldErr(t, err, "settings.holdTimeout")
	past := now.Add(-time.Second)
	_, err = m.Create(ctx, receiver, CreateRequest{Path: "past", ExpiresAt: &past})
	fieldErr(t, err, "expiresAt")
}

func TestCreateExpiryCappedByGrant(t *testing.T) {
	m := newManager(t, t.TempDir())
	now := time.Unix(1000, 0).UTC()
	m.now = func() time.Time { return now }
	capped := identify.User("l", "c@example.com", 5, `{"roles":["receiver"]}`)
	capped.Grants.Limits.MaxHookTTL = 8 * time.Hour

	_, err := m.Create(ctx, capped, CreateRequest{Path: "forever"})
	if msg := fieldErr(t, err, "expiresAt"); !strings.Contains(msg, "8h0m0s") {
		t.Errorf("missing expiry under a TTL limit: %q", msg)
	}
	tooLate := now.Add(9 * time.Hour)
	_, err = m.Create(ctx, capped, CreateRequest{Path: "late", ExpiresAt: &tooLate})
	fieldErr(t, err, "expiresAt")
	ok := now.Add(8 * time.Hour)
	if _, err := m.Create(ctx, capped, CreateRequest{Path: "ok", ExpiresAt: &ok}); err != nil {
		t.Fatalf("expiry at the limit: %v", err)
	}
	// Ephemeral hooks need no expiry: they end with their tunnel.
	if _, err := m.Create(ctx, capped, CreateRequest{Path: "eph", Ephemeral: true}); err != nil {
		t.Fatalf("ephemeral under a TTL limit: %v", err)
	}
}

func TestSettingsSurviveReload(t *testing.T) {
	dir := t.TempDir()
	s1, _ := OpenJSON(dir)
	m1, _ := NewManager(ctx, s1, SlugConfig{})
	h, err := m1.Create(ctx, receiver, CreateRequest{Settings: Settings{MaxBodyBytes: new(int64(4096))}})
	if err != nil {
		t.Fatal(err)
	}
	s1.Close()
	m2 := newManager(t, dir)
	got, _ := m2.Get(h.ID)
	if got.Settings.MaxBodyBytes == nil || *got.Settings.MaxBodyBytes != 4096 {
		t.Fatalf("settings after reload = %+v", got.Settings)
	}
}

func TestCreateRequestOmitsZeroFields(t *testing.T) {
	b, err := json.Marshal(CreateRequest{Path: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"path":"x"}` {
		t.Fatalf("got %s, want {\"path\":\"x\"}", b)
	}
}

func TestUpdateRequestOmitsZeroFields(t *testing.T) {
	b, err := json.Marshal(UpdateRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{}` {
		t.Fatalf("got %s, want {}", b)
	}
}
