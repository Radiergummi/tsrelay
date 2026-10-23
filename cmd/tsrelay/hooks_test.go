package main

import (
	"context"
	"encoding/json"
	"fmt"
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
	var list struct {
		Hooks []api.Hook `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil || list.Hooks == nil {
		e.t.Fatalf("hooks list --json is not {\"hooks\": [...]}: %v\n%s", err, out)
	}
	return list.Hooks
}

func TestHooksCommands(t *testing.T) {
	e := newCLIEnv(t)
	code, out, errOut := e.tsrelay("hooks", "create", "--path", "cb", "--description", "payments", "--hold-timeout", "5s", "--rate-limit", "10/20")
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

	if code, out, _ := e.tsrelay("hooks", "ls"); code != 0 || !strings.Contains(out, id) || !strings.Contains(out, "persistent") ||
		!strings.Contains(out, e.relayURL+"/~moritz/cb") ||
		!strings.HasPrefix(out, "ID\tPATH\tCHANNEL\tKIND\tTUNNEL\tEXPIRES\tURL\n") || strings.Contains(out, "\x1b") {
		t.Fatalf("ls: exit %d\n%q", code, out)
	}

	code, out, errOut = e.tsrelay("hooks", "create", "--json", "--path", "sv")
	if code != 0 {
		t.Fatalf("create --json: exit %d\n%s", code, errOut)
	}
	var created api.Hook
	if err := json.Unmarshal([]byte(out), &created); err != nil || created.Path != "sv" {
		t.Fatalf("create --json: not a Hook with Path %q: %v\n%s", "sv", err, out)
	}
	if code, _, errOut := e.tsrelay("hooks", "delete", "sv"); code != 0 {
		t.Fatalf("delete sv: exit %d\n%s", code, errOut)
	}

	if code, out, errOut := e.tsrelay("hooks", "show", "cb"); code != 0 ||
		!strings.Contains(out, "path: cb") || !strings.Contains(out, "hold timeout: 5s") {
		t.Fatalf("show: exit %d, stdout %q\n%s", code, out, errOut)
	}
	if code, out, errOut := e.tsrelay("hooks", "show", "cb", "--json"); code != 0 {
		t.Fatalf("show --json: exit %d\n%s", code, errOut)
	} else {
		var shown api.Hook
		if err := json.Unmarshal([]byte(out), &shown); err != nil {
			t.Fatalf("show --json: not a Hook: %v\n%s", err, out)
		}
	}
	if code, _, errOut := e.tsrelay("hooks", "show", "nope"); code != 1 || !strings.Contains(errOut, `"nope"`) {
		t.Fatalf("show nope: exit %d\n%s", code, errOut)
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
	if code, out, errOut := e.tsrelay("hooks", "rm", id, "--json"); code != 0 || !strings.HasPrefix(out, `{"deleted":[{"id":"`+id+`",`) || !strings.HasSuffix(out, `],"failed":[]}`+"\n") {
		t.Fatalf("rm --json: exit %d, stdout %q\n%s", code, out, errOut)
	}
	if hs := e.listJSON(); len(hs) != 0 {
		t.Fatalf("after rm: %+v", hs)
	}
}

// Fix round "final": a stray TSRELAY_* variable must not silently change
// hooks update settings it was never asked to change, and must not count
// towards "give at least one flag".
func TestHooksUpdateIgnoresEnvVars(t *testing.T) {
	e := newCLIEnv(t)
	code, _, errOut := e.tsrelay("hooks", "create", "--path", "cb", "--description", "payments", "--hold-timeout", "5s")
	if code != 0 {
		t.Fatalf("create: exit %d\n%s", code, errOut)
	}

	t.Setenv("TSRELAY_DESCRIPTION", "from-env")
	t.Setenv("TSRELAY_HOLD_TIMEOUT", "2s")

	// Only --path should take effect; the env-set description and hold
	// timeout must be left alone.
	code, out, errOut := e.tsrelay("hooks", "update", "cb", "--path", "cb2")
	if code != 0 || strings.TrimSpace(out) != e.relayURL+"/~moritz/cb2" {
		t.Fatalf("update: exit %d, stdout %q\n%s", code, out, errOut)
	}
	h := e.listJSON()[0]
	if h.Path != "cb2" {
		t.Fatalf("path = %q, want cb2", h.Path)
	}
	if h.Description != "payments" {
		t.Fatalf("description = %q, a stray TSRELAY_DESCRIPTION must not have changed it", h.Description)
	}
	if h.Effective.HoldTimeout != hooks.Duration(5*time.Second) {
		t.Fatalf("hold timeout = %v, a stray TSRELAY_HOLD_TIMEOUT must not have changed it", h.Effective.HoldTimeout)
	}

	// With no flags at all, env vars alone must not count as a change.
	if code, _, _ := e.tsrelay("hooks", "update", "cb2"); code != 2 {
		t.Fatalf("update with only env vars: exit %d, want 2", code)
	}
}

// TSRELAY_* must still act as a default for hooks create.
func TestHooksCreateAppliesEnvVars(t *testing.T) {
	e := newCLIEnv(t)
	t.Setenv("TSRELAY_DESCRIPTION", "from-env")
	code, _, errOut := e.tsrelay("hooks", "create", "--path", "envcb")
	if code != 0 {
		t.Fatalf("create: exit %d\n%s", code, errOut)
	}
	if h := e.listJSON()[0]; h.Description != "from-env" {
		t.Fatalf("description = %q, want the TSRELAY_DESCRIPTION default", h.Description)
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
	if code, out, errOut := e.tsrelay("url", e.local.URL, "--json"); code != 0 || out != `{"url":"`+want+`"}`+"\n" {
		t.Errorf("url --json: exit %d, stdout %q, want %q\n%s", code, out, `{"url":"`+want+`"}`+"\n", errOut)
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
		wantOut := "" // a delete still reports what it deleted (nothing) and what failed
		if args[1] == "delete" {
			// The failure carries the error stderr has: {"error":{…}}.
			wantOut = `{"deleted":[],"failed":[{"ref":"nope",` + strings.TrimSuffix(errOut, "}\n")[1:] + "}]}\n"
		}
		if code != 1 || out != wantOut {
			t.Errorf("%q: exit %d, stdout %q; want 1 and %q on stdout", args, code, out, wantOut)
		}
		var je jsonError
		if err := json.Unmarshal([]byte(errOut), &je); err != nil || je.Error.Title == "" || je.Error.Fixes == nil {
			t.Errorf("%q: stderr is not a JSON error: %v\n%s", args, err, errOut)
		}
		if args[1] == "create" && (je.Error.Code != problem.Invalid || je.Error.Problem == nil ||
			je.Error.Problem.Status != 422 || !strings.Contains(fmt.Sprint(je.Error.Problem.Errors), "channel")) {
			t.Errorf("%q: want the relay's problem, with the invalid field, in the JSON error, got %+v", args, je.Error)
		}
		if args[1] == "delete" && je.Error.Problem != nil {
			t.Errorf("%q: a CLI error carries no problem: %+v", args, je.Error.Problem)
		}
	}
}

func TestJSONErrorUnreachableRelay(t *testing.T) {
	var out, errOut syncBuf
	code := run(t.Context(), []string{"hooks", "list", "--json", "--relay", "http://127.0.0.1:1"}, &out, &errOut, deps{})
	if code != 1 || out.String() != "" || !json.Valid([]byte(errOut.String())) {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, out.String(), errOut.String())
	}
	if !strings.Contains(errOut.String(), `"code":"relay_unreachable"`) || !strings.Contains(errOut.String(), "Tried http://127.0.0.1:1 (from --relay)") {
		t.Fatalf("stderr should name the unreachable relay: %s", errOut.String())
	}
}

// Minor 8: in text mode, an unreachable relay reads as a friendly message
// with a hint, not a raw Go dial error.
func TestUnreachableRelayText(t *testing.T) {
	var out, errOut syncBuf
	code := run(t.Context(), []string{"hooks", "list", "--relay", "http://127.0.0.1:1"}, &out, &errOut, deps{})
	if code != 1 || out.String() != "" {
		t.Fatalf("exit %d, stdout %q", code, out.String())
	}
	if !strings.HasPrefix(errOut.String(), "✗ Can't reach the relay\n  Tried http://127.0.0.1:1 (from --relay): ") ||
		!strings.Contains(errOut.String(), "Try:\n    tailscale status\n") {
		t.Fatalf("stderr = %q", errOut.String())
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

func TestHooksPublic(t *testing.T) {
	e := newCLIEnv(t)
	code, out, errOut := e.tsrelay("hooks", "create", "--path", "cb", "--public")
	if code != 0 || !strings.HasPrefix(strings.TrimSpace(out), e.relayURL+"/~moritz/cb") {
		t.Fatalf("create: exit %d %q\n%s", code, out, errOut)
	}
	if !strings.Contains(errOut, "https://tsrelay.example.ts.net/+") {
		t.Fatalf("create does not print the public URL:\n%s", errOut)
	}
	hs := e.listJSON()
	if len(hs) != 1 || !hs[0].Public || hs[0].PublicURL == "" {
		t.Fatalf("hooks = %+v", hs)
	}
	old := hs[0].PublicURL
	code, out, errOut = e.tsrelay("hooks", "rotate-url", "cb")
	if code != 0 || strings.TrimSpace(out) == old || !strings.HasPrefix(strings.TrimSpace(out), "https://tsrelay.example.ts.net/+") {
		t.Fatalf("rotate-url: exit %d %q\n%s", code, out, errOut)
	}
	if code, _, errOut := e.tsrelay("hooks", "update", "cb", "--public=false"); code != 0 {
		t.Fatalf("update --public=false: exit %d\n%s", code, errOut)
	}
	if hs := e.listJSON(); hs[0].Public || hs[0].PublicURL != "" {
		t.Fatalf("after --public=false: %+v", hs[0])
	}
	if code, _, errOut := e.tsrelay("hooks", "rotate-url", "cb"); code == 0 || !strings.Contains(errOut, "not public") {
		t.Fatalf("rotate-url on a private hook: exit %d\n%s", code, errOut)
	}
}

// TestHooksRotateURLWithoutFunnel covers a relay running without --funnel:
// a public hook has no PublicURL, so rotate-url must not print the tailnet
// URL as if it were the new public one.
func TestHooksRotateURLWithoutFunnel(t *testing.T) {
	e := newCLIEnv(t)
	if code, _, errOut := e.tsrelay("hooks", "create", "--path", "cb", "--public"); code != 0 {
		t.Fatalf("create: exit %d\n%s", code, errOut)
	}
	e.restartRelayNoFunnel()
	code, out, errOut := e.tsrelay("hooks", "rotate-url", "cb")
	if code != 0 {
		t.Fatalf("rotate-url: exit %d\n%s", code, errOut)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("stdout = %q, want nothing printed as a public URL", out)
	}
	if !strings.Contains(errOut, "does not serve hooks on the internet") {
		t.Fatalf("rotate-url without --funnel does not warn:\n%s", errOut)
	}
}
