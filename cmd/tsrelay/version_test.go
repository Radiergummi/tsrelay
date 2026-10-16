package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestVersionFlag(t *testing.T) {
	var stdout syncBuf
	if code := run(t.Context(), []string{"--version"}, &stdout, new(nopWriter), deps{}); code != 0 {
		t.Fatalf("--version exited %d", code)
	}
	if !strings.Contains(stdout.String(), version) {
		t.Fatalf("--version printed %q, want it to contain %q", stdout.String(), version)
	}
}

func TestVersionSubcommandIsGone(t *testing.T) {
	if code := run(t.Context(), []string{"version"}, new(nopWriter), new(nopWriter), deps{}); code != 2 {
		t.Fatalf("version subcommand exited %d, want 2 (unknown command)", code)
	}
}

func TestVersionDefaultsToDev(t *testing.T) {
	if version != "dev" {
		t.Fatalf("version = %q, want the unmodified build to say %q", version, "dev")
	}
}

// setVersion makes this CLI report v for the rest of the test. No test in
// this package runs in parallel, so the package variable is safe to swap.
func setVersion(t *testing.T, v string) {
	old := version
	version = v
	t.Cleanup(func() { version = old })
}

func TestVersionMismatchWarnsOnStderr(t *testing.T) {
	for _, tc := range []struct{ cli, relay, rel string }{
		{"v0.2.2", "v0.3.0", "older than"},
		{"v0.4.0", "v0.3.0", "newer than"},
	} {
		t.Run(tc.cli, func(t *testing.T) {
			e := newCLIEnv(t)
			e.restartRelayAt(tc.relay)
			setVersion(t, tc.cli)
			code, out, errOut := e.tsrelay("hooks", "ls")
			want := fmt.Sprintf("warning: tsrelay %s is %s the relay (%s); update with: %s\n", tc.cli, tc.rel, tc.relay, updateCommand(e.relayURL))
			if code != 0 || strings.Contains(out, "warning") || !strings.Contains(errOut, want) {
				t.Fatalf("exit %d\nstdout %q\nstderr %q\nwant %q on stderr", code, out, errOut, want)
			}
		})
	}
}

func TestNoVersionWarningWhenVersionsMatchOrAreDev(t *testing.T) {
	for _, tc := range []struct{ cli, relay string }{
		{"v0.3.0", "v0.3.0"},
		{"dev", "v0.3.0"},
		{"v0.3.0", ""}, // a dev relay
	} {
		e := newCLIEnv(t)
		e.restartRelayAt(tc.relay)
		setVersion(t, tc.cli)
		if code, _, errOut := e.tsrelay("hooks", "ls"); code != 0 || strings.Contains(errOut, "warning") {
			t.Errorf("cli %s, relay %q: exit %d, stderr %q", tc.cli, tc.relay, code, errOut)
		}
	}
}

func TestVersionWarningAsJSON(t *testing.T) {
	e := newCLIEnv(t)
	e.restartRelayAt("v0.3.0")
	setVersion(t, "v0.2.2")
	code, out, errOut := e.tsrelay("hooks", "ls", "--json")
	var w struct{ Warning, CLIVersion, RelayVersion string }
	if code != 0 || json.Unmarshal([]byte(strings.SplitN(errOut, "\n", 2)[0]), &w) != nil ||
		w.CLIVersion != "v0.2.2" || w.RelayVersion != "v0.3.0" || !strings.Contains(w.Warning, "older than the relay") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	var hs []json.RawMessage
	if err := json.Unmarshal([]byte(out), &hs); err != nil {
		t.Fatalf("stdout is not the hook list: %v\n%s", err, out)
	}
}

// tsrelay http creates a hook and dials the tunnel: two responses, one warning.
func TestVersionWarningOncePerRun(t *testing.T) {
	e := newCLIEnv(t)
	e.restartRelayAt("v0.3.0")
	setVersion(t, "v0.2.2")
	r := e.start()
	waitOutput(t, r.stderr, "connected")
	r.cancel()
	<-r.exit
	if n := strings.Count(r.stderr.String(), "warning:"); n != 1 {
		t.Fatalf("%d warnings:\n%s", n, r.stderr)
	}
}
