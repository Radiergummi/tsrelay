package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/identify"
)

func TestLogShape(t *testing.T) {
	var buf bytes.Buffer
	a := New(slog.New(slog.NewJSONHandler(&buf, nil)))
	a.Log(context.Background(), HookCreate,
		Actor(identify.User("laptop", "m@x.io", 1)),
		Hook(hooks.Hook{ID: "h_1", OwnerLogin: "m@x.io", Path: "p", Channel: "default"}))
	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatal(err)
	}
	if rec["msg"] != HookCreate {
		t.Fatalf("msg = %v", rec["msg"])
	}
	actor, hook := rec["actor"].(map[string]any), rec["hook"].(map[string]any)
	if actor["node"] != "laptop" || actor["login"] != "m@x.io" || hook["id"] != "h_1" || hook["channel"] != "default" {
		t.Fatalf("record = %v", rec)
	}
}
