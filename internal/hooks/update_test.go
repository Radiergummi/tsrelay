package hooks

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
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
	out, _ := json.Marshal(UpdateRequest{Description: Opt[string]{Set: true, Value: new("y")}, Channel: Opt[string]{Set: true}})
	if string(out) != `{"channel":null,"description":"y","settings":{}}` {
		t.Errorf("marshal = %s", out)
	}
}

func TestUpdate(t *testing.T) {
	m := newManager(t, t.TempDir())
	h, _ := m.Create(ctx, receiver, CreateRequest{Path: "a", Settings: Settings{HoldTimeout: new(Duration(time.Second))}})
	m.Create(ctx, receiver, CreateRequest{Path: "b"})

	got, changed, err := m.Update(ctx, receiver, h.ID, UpdateRequest{
		Path:        Opt[string]{Set: true, Value: new("a2")},
		Channel:     Opt[string]{Set: true, Value: new("prod.eu")},
		Description: Opt[string]{Set: true, Value: new("payments")},
		Settings: SettingsPatch{
			HoldTimeout: Opt[Duration]{Set: true}, // null: back to the relay default
			HostHeader:  Opt[HostHeader]{Set: true, Value: new(HostRewrite)},
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

// Fields set to the value they already have are not reported as changed.
func TestUpdateUnchanged(t *testing.T) {
	m := newManager(t, t.TempDir())
	exp := time.Now().Add(time.Hour)
	h, _ := m.Create(ctx, receiver, CreateRequest{Path: "a", ExpiresAt: &exp, Settings: Settings{HoldTimeout: new(Duration(time.Second))}})

	_, changed, err := m.Update(ctx, receiver, h.ID, UpdateRequest{
		Path:        Opt[string]{Set: true, Value: new("a")},
		Channel:     Opt[string]{Set: true}, // null: the default it already has
		Description: Opt[string]{Set: true},
		Public:      Opt[bool]{Set: true, Value: new(false)},
		ExpiresAt:   Opt[time.Time]{Set: true, Value: new(exp.In(time.FixedZone("x", 3600)))},
		Settings: SettingsPatch{
			HoldTimeout: Opt[Duration]{Set: true, Value: new(Duration(time.Second))},
			IdleTimeout: Opt[Duration]{Set: true}, // null: already the relay default
		},
	})
	if err != nil || len(changed) != 0 {
		t.Fatalf("changed = %v, %v; want none", changed, err)
	}
	_, changed, err = m.Update(ctx, receiver, h.ID, UpdateRequest{
		Path:     Opt[string]{Set: true, Value: new("a")},
		Settings: SettingsPatch{HoldTimeout: Opt[Duration]{Set: true, Value: new(Duration(2 * time.Second))}},
	})
	if err != nil || !slices.Equal(changed, []string{"settings.holdTimeout"}) {
		t.Fatalf("changed = %v, %v", changed, err)
	}
}

func TestUpdateRejections(t *testing.T) {
	m := newManager(t, t.TempDir())
	h, _ := m.Create(ctx, receiver, CreateRequest{Path: "a"})
	m.Create(ctx, receiver, CreateRequest{Path: "b"})
	cases := map[string]UpdateRequest{
		"path":                 {Path: Opt[string]{Set: true, Value: new("b/c")}},
		"channel":              {Channel: Opt[string]{Set: true, Value: new("billing")}},
		"ephemeral":            {Ephemeral: Opt[bool]{Set: true}},
		"settings.idleTimeout": {Settings: SettingsPatch{IdleTimeout: Opt[Duration]{Set: true, Value: new(Duration(0))}}},
		"expiresAt":            {ExpiresAt: Opt[time.Time]{Set: true, Value: new(time.Unix(1, 0))}},
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

// Lowering a maximum must not make a hook's unrelated fields uneditable.
func TestUpdateValidatesOnlyTouchedSettings(t *testing.T) {
	m := newManager(t, t.TempDir())
	h, _ := m.Create(ctx, receiver, CreateRequest{Path: "a", Settings: Settings{MaxBodyBytes: new(int64(10 << 20))}})
	p := m.Policy()
	p.Max.MaxBodyBytes = 1 << 20
	m.SetPolicy(p)

	if _, _, err := m.Update(ctx, receiver, h.ID, UpdateRequest{Description: Opt[string]{Set: true, Value: new("x")}}); err != nil {
		t.Fatalf("description-only update: %v", err)
	}
	_, _, err := m.Update(ctx, receiver, h.ID, UpdateRequest{Settings: SettingsPatch{MaxBodyBytes: Opt[int64]{Set: true, Value: new(int64(10 << 20))}}})
	fieldErr(t, err, "settings.maxBodyBytes")
}

func TestUpdateCapture(t *testing.T) {
	m := newManager(t, t.TempDir())
	h, _ := m.Create(ctx, receiver, CreateRequest{Path: "a"})
	got, changed, err := m.Update(ctx, receiver, h.ID, UpdateRequest{Settings: SettingsPatch{Capture: Opt[bool]{Set: true, Value: new(false)}}})
	if err != nil || got.Settings.Capture == nil || *got.Settings.Capture || !slices.Contains(changed, "settings.capture") {
		t.Fatalf("update = %+v %v %v", got.Settings, changed, err)
	}
}

func TestExpiry(t *testing.T) {
	m := newManager(t, t.TempDir())
	now := time.Unix(1000, 0).UTC()
	m.now = func() time.Time { return now }
	exp := now.Add(time.Minute)
	m.Create(ctx, receiver, CreateRequest{Path: "x", ExpiresAt: &exp})
	now = now.Add(2 * time.Minute)
	if _, _, ok := m.Route("moritz", "x"); ok {
		t.Fatal("expired hook still routes before the reaper ran")
	}
}

// A grant change can leave a hook in a channel its owner no longer receives
// on: editing anything but the channel then fails, and moving it succeeds.
func TestUpdateRevalidatesResultingChannel(t *testing.T) {
	m := newManager(t, t.TempDir())
	before := identify.User("laptop", "moritz@example.com", 1, `{"roles":["receiver"],"channels":["dev","prod"]}`)
	after := identify.User("laptop", "moritz@example.com", 1, `{"roles":["receiver"],"channels":["prod"]}`)
	h, err := m.Create(ctx, before, CreateRequest{Channel: "dev"})
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = m.Update(ctx, after, h.ID, UpdateRequest{Description: Opt[string]{Set: true, Value: new("x")}})
	if msg := fieldErr(t, err, "channel"); !strings.Contains(msg, `"dev"`) {
		t.Errorf("message %q should name the channel", msg)
	}
	got, _, err := m.Update(ctx, after, h.ID, UpdateRequest{Channel: Opt[string]{Set: true, Value: new("prod")}})
	if err != nil || got.Channel != "prod" {
		t.Fatalf("moving into a granted channel = %+v, %v", got, err)
	}
}

func TestUpdateRevalidatesPublic(t *testing.T) {
	m := newManager(t, t.TempDir())
	before := identify.User("laptop", "moritz@example.com", 1, `{"roles":["receiver"],"channels":["dev","prod"],"public":true}`)
	after := identify.User("laptop", "moritz@example.com", 1, `{"roles":["receiver"],"channels":["dev","prod"]}`)
	h, err := m.Create(ctx, before, CreateRequest{Channel: "dev", Public: true})
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = m.Update(ctx, after, h.ID, UpdateRequest{Description: Opt[string]{Set: true, Value: new("x")}})
	fieldErr(t, err, "public")
	got, _, err := m.Update(ctx, after, h.ID, UpdateRequest{Public: Opt[bool]{Set: true, Value: new(false)}})
	if err != nil || got.Public {
		t.Fatalf("turning public off = %+v, %v", got, err)
	}
}
