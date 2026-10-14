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
	"github.com/Radiergummi/tsrelay/internal/problem"
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
	code, out, errOut := e.tsrelay("hooks", "list", "--json")
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

	if code, _, errOut := e.tsrelay("hooks", "delete", "nope"); code != 1 || !strings.Contains(errOut, `"nope"`) {
		t.Fatalf("delete unknown: exit %d\n%s", code, errOut)
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

func TestJSONErrors(t *testing.T) {
	e := newCLIEnv(t)
	for _, args := range [][]string{
		{"hooks", "delete", "nope", "--json"},                   // unknown hook (a CLI error)
		{"hooks", "create", "--channel", "Not Valid", "--json"}, // a relay refusal (problem)
	} {
		code, out, errOut := e.tsrelay(args...)
		if code != 1 || out != "" {
			t.Errorf("%q: exit %d, stdout %q; want 1 and nothing on stdout", args, code, out)
		}
		var je struct {
			Error   string           `json:"error"`
			Problem *problem.Problem `json:"problem"`
		}
		if err := json.Unmarshal([]byte(errOut), &je); err != nil || je.Error == "" {
			t.Errorf("%q: stderr is not a JSON error: %v\n%s", args, err, errOut)
		}
		if args[1] == "create" && (je.Problem == nil || je.Problem.Status != 422) {
			t.Errorf("%q: want the relay's problem in the JSON error, got %+v", args, je.Problem)
		}
	}
}

func TestJSONErrorUnreachableRelay(t *testing.T) {
	var out, errOut syncBuf
	code := run(t.Context(), []string{"hooks", "list", "--json", "--relay", "http://127.0.0.1:1"}, &out, &errOut, deps{})
	if code != 1 || out.String() != "" || !json.Valid([]byte(errOut.String())) {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, out.String(), errOut.String())
	}
}

func TestRelayFromEnvironment(t *testing.T) {
	e := newCLIEnv(t)
	t.Setenv("TSRELAY_RELAY", e.relayURL)
	var out, errOut syncBuf
	d := deps{HTTP: tunnel.NewHTTPClient(e.fake.Dial(receiver)), StatePath: e.statePath}
	if code := run(t.Context(), []string{"hooks", "list", "--json"}, &out, &errOut, d); code != 0 {
		t.Fatalf("exit %d\n%s", code, errOut.String())
	}
}
