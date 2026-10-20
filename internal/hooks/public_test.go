package hooks

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Radiergummi/tsrelay/internal/identify"
)

var (
	publicOwner = identify.User("laptop", "moritz@example.com", 1, `{"roles":["receiver"],"public":true}`)
	plainOwner  = identify.User("laptop", "moritz@example.com", 1, `{"roles":["receiver"]}`)
	stranger    = identify.User("desk", "other@example.com", 2, `{"roles":["receiver"],"public":true}`)
)

func newPublicManager(t *testing.T) *Manager {
	t.Helper()
	store, err := OpenJSON(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	m, err := NewManager(context.Background(), store, SlugConfig{})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestCreatePublic(t *testing.T) {
	m := newPublicManager(t)
	ctx := context.Background()
	h, err := m.Create(ctx, publicOwner, CreateRequest{Path: "cb", Public: true})
	if err != nil {
		t.Fatal(err)
	}
	if !h.Public || len(h.PublicToken) != 22 || h.PublicRoute() != "/+"+h.PublicToken {
		t.Fatalf("hook = %+v", h)
	}
	got, ok := m.RoutePublic(h.PublicToken)
	if !ok || got.ID != h.ID {
		t.Fatalf("RoutePublic = %+v, %v", got, ok)
	}
	plain, _ := m.Create(ctx, publicOwner, CreateRequest{Path: "other"})
	if plain.Public || plain.PublicToken != "" {
		t.Fatalf("a hook created without public got %+v", plain)
	}
}

func TestCreatePublicNeedsGrant(t *testing.T) {
	m := newPublicManager(t)
	_, err := m.Create(context.Background(), plainOwner, CreateRequest{Path: "cb", Public: true})
	var ve ValidationError
	if !errors.As(err, &ve) || len(ve) != 1 || ve[0].Field != "public" {
		t.Fatalf("err = %v, want a field error on public", err)
	}
}

func TestPublicScopedToChannel(t *testing.T) {
	m := newPublicManager(t)
	ctx := context.Background()
	devOnly := identify.User("laptop", "moritz@example.com", 1,
		`{"roles":["receiver"],"channels":["dev","prod"]}`,
		`{"roles":["receiver"],"channels":["dev"],"public":true}`)
	if _, err := m.Create(ctx, devOnly, CreateRequest{Path: "a", Channel: "dev", Public: true}); err != nil {
		t.Fatalf("public in dev: %v", err)
	}
	_, err := m.Create(ctx, devOnly, CreateRequest{Path: "b", Channel: "prod", Public: true})
	var ve ValidationError
	if !errors.As(err, &ve) || len(ve) != 1 || ve[0].Field != "public" {
		t.Fatalf("public in prod: err = %v, want a field error on public", err)
	}
	h, _ := m.Create(ctx, devOnly, CreateRequest{Path: "c", Channel: "dev", Public: true})
	_, _, err = m.Update(ctx, devOnly, h.ID, UpdateRequest{Channel: Opt[string]{Set: true, Value: new("prod")}})
	if !errors.As(err, &ve) || len(ve) != 1 || ve[0].Field != "public" {
		t.Fatalf("moving a public hook to prod: err = %v, want a field error on public", err)
	}
}

func TestPublicToggleRotatesToken(t *testing.T) {
	m := newPublicManager(t)
	ctx := context.Background()
	h, _ := m.Create(ctx, publicOwner, CreateRequest{Path: "cb", Public: true})
	old := h.PublicToken
	off, _, err := m.Update(ctx, publicOwner, h.ID, UpdateRequest{Public: Opt[bool]{Set: true, Value: new(false)}})
	if err != nil || off.Public || off.PublicToken != "" {
		t.Fatalf("off = %+v, %v", off, err)
	}
	if _, ok := m.RoutePublic(old); ok {
		t.Fatal("the old token still routes after public was turned off")
	}
	on, _, err := m.Update(ctx, publicOwner, h.ID, UpdateRequest{Public: Opt[bool]{Set: true, Value: new(true)}})
	if err != nil || !on.Public || on.PublicToken == "" || on.PublicToken == old {
		t.Fatalf("on = %+v, %v; want a fresh token", on, err)
	}
	if _, ok := m.RoutePublic(old); ok {
		t.Fatal("the old token routes again after public was turned back on")
	}
	same, _, _ := m.Update(ctx, publicOwner, h.ID, UpdateRequest{Public: Opt[bool]{Set: true, Value: new(true)}})
	if same.PublicToken != on.PublicToken {
		t.Fatal("setting public to true on a public hook must keep its token")
	}
}

func TestUpdatePublicNeedsGrantAndValue(t *testing.T) {
	m := newPublicManager(t)
	ctx := context.Background()
	h, _ := m.Create(ctx, plainOwner, CreateRequest{Path: "cb"})
	_, _, err := m.Update(ctx, plainOwner, h.ID, UpdateRequest{Public: Opt[bool]{Set: true, Value: new(true)}})
	var ve ValidationError
	if !errors.As(err, &ve) || ve[0].Field != "public" {
		t.Fatalf("err = %v, want a field error on public", err)
	}
	_, _, err = m.Update(ctx, plainOwner, h.ID, UpdateRequest{Public: Opt[bool]{Set: true}})
	if !errors.As(err, &ve) || ve[0].Message != "cannot be null" {
		t.Fatalf("null public: err = %v", err)
	}
	// Turning public off never needs the grant.
	if _, _, err := m.Update(ctx, plainOwner, h.ID, UpdateRequest{Public: Opt[bool]{Set: true, Value: new(false)}}); err != nil {
		t.Fatal(err)
	}
}

func TestRotatePublicToken(t *testing.T) {
	m := newPublicManager(t)
	ctx := context.Background()
	h, _ := m.Create(ctx, publicOwner, CreateRequest{Path: "cb", Public: true})
	r, err := m.RotatePublicToken(ctx, publicOwner, h.ID)
	if err != nil || r.PublicToken == h.PublicToken || len(r.PublicToken) != 22 {
		t.Fatalf("rotated = %+v, %v", r, err)
	}
	if _, ok := m.RoutePublic(h.PublicToken); ok {
		t.Fatal("the old token still routes after rotation")
	}
	if _, err := m.RotatePublicToken(ctx, stranger, h.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another user rotating: err = %v, want ErrNotFound", err)
	}
	plain, _ := m.Create(ctx, publicOwner, CreateRequest{Path: "plain"})
	if _, err := m.RotatePublicToken(ctx, publicOwner, plain.ID); !errors.Is(err, ErrNotPublic) {
		t.Fatalf("rotating a non-public hook: err = %v, want ErrNotPublic", err)
	}
}

func TestRoutePublicIgnoresEmptyToken(t *testing.T) {
	m := newPublicManager(t)
	m.Create(context.Background(), publicOwner, CreateRequest{Path: "cb"})
	if _, ok := m.RoutePublic(""); ok {
		t.Fatal("an empty token matched a hook")
	}
}

func TestRoutePublicSkipsExpired(t *testing.T) {
	m := newPublicManager(t)
	h, _ := m.Create(context.Background(), publicOwner, CreateRequest{Path: "cb", Public: true, Ephemeral: true})
	now := m.now()
	m.now = func() time.Time { return now.Add(time.Hour) }
	m.mu.Lock()
	exp := now.Add(time.Minute)
	cur := m.hooks[h.ID]
	cur.ExpiresAt = &exp
	m.hooks[h.ID] = cur
	m.mu.Unlock()
	if _, ok := m.RoutePublic(h.PublicToken); ok {
		t.Fatal("an expired hook was routed")
	}
}

func TestPublicTokenPersists(t *testing.T) {
	dir := t.TempDir()
	store, _ := OpenJSON(dir)
	m, _ := NewManager(context.Background(), store, SlugConfig{})
	h, _ := m.Create(context.Background(), publicOwner, CreateRequest{Path: "cb", Public: true})
	store.Close()
	store, _ = OpenJSON(dir)
	t.Cleanup(func() { store.Close() })
	m, _ = NewManager(context.Background(), store, SlugConfig{})
	if got, ok := m.RoutePublic(h.PublicToken); !ok || got.ID != h.ID {
		t.Fatal("the public token did not survive a reload")
	}
}
