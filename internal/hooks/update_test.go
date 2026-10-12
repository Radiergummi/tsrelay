package hooks

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/Radiergummi/tsrelay/internal/identify"
)

func TestOptJSON(t *testing.T) {
	var req UpdateRequest
	if err := json.Unmarshal([]byte(`{"description":"x","channel":null,"settings":{"holdTimeout":"5s","rateLimit":null}}`), &req); err != nil {
		t.Fatal(err)
	}
	if !req.Description.Set || *req.Description.Value != "x" {
		t.Errorf("description = %+v", req.Description)
	}
	if !req.Channel.Set || req.Channel.Value != nil {
		t.Errorf("explicit null must be Set with nil Value: %+v", req.Channel)
	}
	if req.Path.Set || req.Ephemeral.Set {
		t.Error("absent fields must not be Set")
	}
	if *req.Settings.HoldTimeout.Value != Duration(5*time.Second) || !req.Settings.RateLimit.Set || req.Settings.RateLimit.Value != nil {
		t.Errorf("settings = %+v", req.Settings)
	}
	out, _ := json.Marshal(UpdateRequest{Description: Opt[string]{Set: true, Value: ptr("y")}, Channel: Opt[string]{Set: true}})
	if string(out) != `{"channel":null,"description":"y","settings":{}}` {
		t.Errorf("marshal = %s", out)
	}
}

func TestUpdate(t *testing.T) {
	m := newManager(t, t.TempDir())
	h, _ := m.Create(ctx, receiver, CreateRequest{Path: "a", Settings: Settings{HoldTimeout: ptr(Duration(time.Second))}})
	m.Create(ctx, receiver, CreateRequest{Path: "b"})

	got, changed, err := m.Update(ctx, receiver, h.ID, UpdateRequest{
		Path:        Opt[string]{Set: true, Value: ptr("a2")},
		Channel:     Opt[string]{Set: true, Value: ptr("prod.eu")},
		Description: Opt[string]{Set: true, Value: ptr("payments")},
		Settings: SettingsPatch{
			HoldTimeout: Opt[Duration]{Set: true}, // null: back to the relay default
			HostHeader:  Opt[HostHeader]{Set: true, Value: ptr(HostRewrite)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != "a2" || got.Channel != "prod.eu" || got.Description != "payments" || got.Settings.HoldTimeout != nil || *got.Settings.HostHeader != HostRewrite {
		t.Fatalf("updated = %+v", got)
	}
	slices.Sort(changed)
	if !slices.Equal(changed, []string{"channel", "description", "path", "settings.holdTimeout", "settings.hostHeader"}) {
		t.Fatalf("changed = %v", changed)
	}
	if r, _, ok := m.Route("moritz", "a2/x"); !ok || r.ID != h.ID {
		t.Fatal("renamed hook does not route")
	}
	if _, _, ok := m.Route("moritz", "a"); ok {
		t.Fatal("old path still routes")
	}
}

func TestUpdateRejections(t *testing.T) {
	m := newManager(t, t.TempDir())
	h, _ := m.Create(ctx, receiver, CreateRequest{Path: "a"})
	m.Create(ctx, receiver, CreateRequest{Path: "b"})
	cases := map[string]UpdateRequest{
		"path":                 {Path: Opt[string]{Set: true, Value: ptr("b/c")}},
		"channel":              {Channel: Opt[string]{Set: true, Value: ptr("billing")}},
		"ephemeral":            {Ephemeral: Opt[bool]{Set: true}},
		"settings.idleTimeout": {Settings: SettingsPatch{IdleTimeout: Opt[Duration]{Set: true, Value: ptr(Duration(0))}}},
		"expiresAt":            {ExpiresAt: Opt[time.Time]{Set: true, Value: ptr(time.Unix(1, 0))}},
	}
	for field, req := range cases {
		_, _, err := m.Update(ctx, receiver, h.ID, req)
		fieldErr(t, err, field)
	}
	other := identify.User("l2", "other@example.com", 9, `{"roles":["receiver"]}`)
	if _, _, err := m.Update(ctx, other, h.ID, UpdateRequest{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-owner update: %v, want ErrNotFound", err)
	}
	// A rejected update must leave the hook untouched.
	if got, _ := m.Get(h.ID); got.Path != "a" || got.Channel != "default" {
		t.Fatalf("hook changed by failed updates: %+v", got)
	}
}

func TestExpiry(t *testing.T) {
	m := newManager(t, t.TempDir())
	now := time.Unix(1000, 0).UTC()
	m.now = func() time.Time { return now }
	exp := now.Add(time.Minute)
	h, _ := m.Create(ctx, receiver, CreateRequest{Path: "x", ExpiresAt: &exp})
	m.SetAttached(h.ID, true)
	now = now.Add(2 * time.Minute)
	if _, _, ok := m.Route("moritz", "x"); ok {
		t.Fatal("expired hook still routes before the reaper ran")
	}
	reaped, err := m.Reap(ctx, time.Hour, func(ID) bool { return true })
	if err != nil || len(reaped) != 1 || reaped[0].ID != h.ID {
		t.Fatalf("Reap = %v, %v; expired hooks go even while served", reaped, err)
	}
}
