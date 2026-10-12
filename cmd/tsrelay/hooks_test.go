package main

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Radiergummi/tsrelay/internal/api"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
)

// tsrelay runs a tsrelay command against the test relay to completion.
func (e *cliEnv) tsrelay(args ...string) (code int, stdout, stderr string) {
	var out, errOut syncBuf
	d := deps{HTTP: tunnel.NewHTTPClient(e.fake.Dial(receiver)), StatePath: e.statePath}
	code = run(context.Background(), slices.Concat(args, []string{"--relay", e.relayURL}), &out, &errOut, d)
	return code, out.String(), errOut.String()
}

func (e *cliEnv) listJSON() []api.Hook {
	e.t.Helper()
	code, out, errOut := e.tsrelay("hooks", "ls", "--json")
	if code != 0 {
		e.t.Fatalf("hooks ls --json: exit %d\n%s", code, errOut)
	}
	var hs []api.Hook
	if err := json.Unmarshal([]byte(out), &hs); err != nil {
		e.t.Fatalf("hooks ls --json is not a JSON array: %v\n%s", err, out)
	}
	return hs
}

func TestHooksCommands(t *testing.T) {
	e := newCLIEnv(t)
	code, out, errOut := e.tsrelay("hooks", "create", "cb", "--description", "payments", "--hold-timeout", "5s", "--rate-limit", "10/20")
	if code != 0 || strings.TrimSpace(out) != e.relayURL+"/~moritz/cb" {
		t.Fatalf("create: exit %d, stdout %q\n%s", code, out, errOut)
	}
	hs := e.listJSON()
	if len(hs) != 1 || hs[0].Ephemeral || hs[0].Description != "payments" || hs[0].Effective.HoldTimeout != hooks.Duration(5*time.Second) {
		t.Fatalf("created = %+v", hs)
	}
	if rl := hs[0].Settings.RateLimit; rl == nil || *rl != (hooks.RateLimit{PerSecond: 10, Burst: 20}) {
		t.Fatalf("rate limit = %+v", rl)
	}
	if hs[0].Settings.ResponseTimeout != nil {
		t.Error("only the setting flags given may become overrides")
	}
	id := hs[0].ID

	if code, out, _ := e.tsrelay("hooks", "ls"); code != 0 || !strings.Contains(out, id) || !strings.Contains(out, "persistent") || !strings.Contains(out, e.relayURL+"/~moritz/cb") {
		t.Fatalf("ls: exit %d\n%s", code, out)
	}

	// By path; only the given fields change.
	code, out, errOut = e.tsrelay("hooks", "update", "cb", "--path", "cb2", "--hold-timeout", "1s")
	if code != 0 || strings.TrimSpace(out) != e.relayURL+"/~moritz/cb2" {
		t.Fatalf("update: exit %d, stdout %q\n%s", code, out, errOut)
	}
	if h := e.listJSON()[0]; h.Path != "cb2" || h.Effective.HoldTimeout != hooks.Duration(time.Second) || h.Description != "payments" || h.Settings.RateLimit == nil {
		t.Fatalf("updated = %+v", h)
	}
	if code, _, errOut := e.tsrelay("hooks", "update", id, "--hold-timeout", "1h"); code != 1 || !strings.Contains(errOut, "holdTimeout") {
		t.Fatalf("update over the maximum: exit %d\n%s", code, errOut)
	}
	if code, _, _ := e.tsrelay("hooks", "update", id); code != 2 {
		t.Fatalf("update without changes: exit %d, want 2", code)
	}

	if code, _, errOut := e.tsrelay("hooks", "rm", "nope"); code != 1 || !strings.Contains(errOut, `"nope"`) {
		t.Fatalf("rm unknown: exit %d\n%s", code, errOut)
	}
	if code, _, errOut := e.tsrelay("hooks", "rm", id); code != 0 {
		t.Fatalf("rm: exit %d\n%s", code, errOut)
	}
	if hs := e.listJSON(); len(hs) != 0 {
		t.Fatalf("after rm: %+v", hs)
	}
}

func TestURLCommand(t *testing.T) {
	e := newCLIEnv(t)
	if code, out, errOut := e.tsrelay("url", e.local.URL); code != 1 || out != "" || !strings.Contains(errOut, "tsrelay http") {
		t.Fatalf("before any tsrelay http: exit %d, stdout %q\n%s", code, out, errOut)
	}
	r := e.start()
	want := e.url(r)
	r.cancel()
	waitExit(t, r)
	before := len(e.listJSON())
	for _, args := range [][]string{{"url", e.local.URL}, {"url"}} {
		if code, out, errOut := e.tsrelay(args...); code != 0 || strings.TrimSpace(out) != want {
			t.Errorf("%v: exit %d, stdout %q, want %q\n%s", args, code, out, want, errOut)
		}
	}
	if after := len(e.listJSON()); after != before {
		t.Fatalf("tsrelay url changed the hooks: %d → %d", before, after)
	}
}

func TestHooksUsage(t *testing.T) {
	for _, args := range [][]string{{"hooks"}, {"hooks", "bogus"}, {"hooks", "rm"}, {"url", "8080", "9090"}} {
		if code := run(context.Background(), args, &syncBuf{}, &syncBuf{}, deps{}); code != 2 {
			t.Errorf("run(%q) = %d, want 2", args, code)
		}
	}
}
