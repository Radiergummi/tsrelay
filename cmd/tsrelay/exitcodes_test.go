package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Radiergummi/tsrelay/internal/hooks"
)

func TestHookPathFlagEverywhere(t *testing.T) {
	e := newCLIEnv(t)
	r := e.start("--path", "from-http")
	e.url(r)
	if code, _, errOut := e.tsrelay("hooks", "create", "--path", "from-create"); code != 0 {
		t.Fatalf("hooks create --path: exit %d\n%s", code, errOut)
	}
	if code, _, errOut := e.tsrelay("hooks", "update", "from-create", "--path", "from-update"); code != 0 {
		t.Fatalf("hooks update --path: exit %d\n%s", code, errOut)
	}
	var got []string
	for _, h := range e.listJSON() {
		got = append(got, h.Path)
	}
	if strings.Join(got, ",") != "from-http,from-update" {
		t.Fatalf("paths = %v", got)
	}

	for _, args := range [][]string{
		{"http", e.local.URL, "--name", "x"},
		{"http", e.local.URL, "--persistent"},
		{"hooks", "create", "positional"},
	} {
		if code, _, _ := e.tsrelay(args...); code != exitUsage {
			t.Errorf("%q: exit %d, want a usage error", args, code)
		}
	}
}

func TestEphemeralDefaults(t *testing.T) {
	e := newCLIEnv(t)
	r1 := e.start("--path", "dflt")
	e.url(r1)
	r2 := e.start("--path", "kept", "--ephemeral=false")
	e.url(r2)
	if code, _, errOut := e.tsrelay("hooks", "create", "--path", "made"); code != 0 {
		t.Fatalf("hooks create: exit %d\n%s", code, errOut)
	}
	want := map[string]bool{"dflt": true, "kept": false, "made": false}
	for _, h := range e.listJSON() {
		if h.Ephemeral != want[h.Path] {
			t.Errorf("%s: ephemeral = %v, want %v", h.Path, h.Ephemeral, want[h.Path])
		}
		delete(want, h.Path)
	}
	if len(want) != 0 {
		t.Errorf("hooks missing: %v", want)
	}
}

func TestHooksDeleteReportsPartial(t *testing.T) {
	e := newCLIEnv(t)
	ctx := context.Background()
	for _, p := range []string{"a", "b"} {
		if _, err := e.api().CreateHook(ctx, hooks.CreateRequest{Path: p}); err != nil {
			t.Fatal(err)
		}
	}
	ids := map[string]string{}
	for _, h := range e.listJSON() {
		ids[h.Path] = h.ID
	}
	code, out, errOut := e.tsrelay("hooks", "delete", "a", "missing", "b", "--json")
	if want := `{"deleted":["` + ids["a"] + `","` + ids["b"] + `"]}` + "\n"; code != 1 || out != want {
		t.Fatalf("exit %d, stdout %q, want 1 and %q\n%s", code, out, want, errOut)
	}
	if hs := e.listJSON(); len(hs) != 0 {
		t.Fatalf("hooks left: %+v", hs)
	}
}

func TestExitCodesWithChild(t *testing.T) {
	e := newCLIEnv(t)
	notExecutable := filepath.Join(t.TempDir(), "script")
	if err := os.WriteFile(notExecutable, []byte("#!/bin/sh\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	noShebang := filepath.Join(t.TempDir(), "script")
	if err := os.WriteFile(noShebang, []byte("exit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		command []string
		want    int
	}{
		{"fails", []string{"false"}, 1},
		{"exits 7", []string{"sh", "-c", "exit 7"}, 7},
		{"not found", []string{"/nonexistent/tsrelay-test"}, exitNotFound},
		{"not on the PATH", []string{"tsrelay-no-such-command"}, exitNotFound},
		{"not executable", []string{notExecutable}, exitNotExecutable},
		{"no interpreter line", []string{noShebang}, exitNotExecutable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := e.start(append([]string{"--"}, tc.command...)...)
			if code := waitExit(t, r); code != tc.want {
				t.Fatalf("exit = %d, want %d; stderr:\n%s", code, tc.want, r.stderr)
			}
		})
	}

	t.Run("hook deleted", func(t *testing.T) {
		r, _ := e.startChild("sleep")
		e.url(r)
		hs := e.listJSON()
		if err := e.api().DeleteHook(context.Background(), hs[0].ID); err != nil {
			t.Fatal(err)
		}
		if code := waitExit(t, r); code != exitTsrelayWithChild {
			t.Fatalf("exit = %d, want %d", code, exitTsrelayWithChild)
		}
	})

	t.Run("tsrelay fails before the command starts", func(t *testing.T) {
		var out, errOut syncBuf
		code := run(t.Context(), []string{"http", e.local.URL, "--relay", "http://127.0.0.1:1", "--", "true"}, &out, &errOut, deps{StatePath: e.statePath})
		if code != exitTsrelayWithChild {
			t.Fatalf("exit = %d, want %d; stderr:\n%s", code, exitTsrelayWithChild, &errOut)
		}
	})
}

func TestExitCodesWithoutChild(t *testing.T) {
	e := newCLIEnv(t)
	if code, _, _ := e.tsrelay("http"); code != exitUsage {
		t.Errorf("usage error: exit %d, want %d", code, exitUsage)
	}
	if code, _, _ := e.tsrelay("hooks", "show", "nope"); code != exitError {
		t.Errorf("error: exit %d, want %d", code, exitError)
	}
	// Hook deleted while serving: see TestHTTPStopsWhenHookDeleted. An
	// update --check exits 4 for a newer relay: see TestUpdateCheckOnlyReports.
}
