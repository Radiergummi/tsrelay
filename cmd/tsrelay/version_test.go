package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/Radiergummi/tsrelay/internal/problem"
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

func TestVersionSkew(t *testing.T) {
	for _, tc := range []struct {
		cli, relay string
		want       skew
	}{
		{"v1.2.0", "v1.2.0", skewNone},
		{"v1.3.0", "v1.2.0", skewCLINewer},
		{"v1.1.0", "v1.2.0", skewCLIOlder},
		{"v1.0.0", "v2.0.0", skewMajor},
		{"dev", "v1.0.0", skewNone},
		{"v1.0.0", "dev", skewNone},
		{"v1.0.0", "", skewNone},
		{"v1.0.0", "garbage", skewNone},
	} {
		if got := versionSkew(tc.cli, tc.relay); got != tc.want {
			t.Errorf("versionSkew(%q, %q) = %d, want %d", tc.cli, tc.relay, got, tc.want)
		}
	}
}

func TestMajorMismatchWarns(t *testing.T) {
	for _, tc := range []struct{ cli, relay string }{
		{"v1.0.0", "v2.0.0"},
		{"v2.0.0", "v1.0.0"},
	} {
		t.Run(tc.cli, func(t *testing.T) {
			e := newCLIEnv(t)
			e.restartRelayAt(tc.relay)
			setVersion(t, tc.cli)
			code, out, errOut := e.tsrelay("hooks", "ls")
			want := fmt.Sprintf("warning: tsrelay %s and the relay (%s) are different major versions; install the relay's version with: tsrelay update --relay %s\n", tc.cli, tc.relay, e.relayURL)
			if code != 0 || strings.Contains(out, "warning") || !strings.Contains(errOut, want) {
				t.Fatalf("exit %d\nstdout %q\nstderr %q\nwant %q on stderr", code, out, errOut, want)
			}
			code, _, errOut = e.tsrelay("hooks", "create", "--channel", "Not Valid")
			if code != 1 || !strings.Contains(errOut, fmt.Sprintf("This tsrelay (%s) differs from the relay (%s), which may be why.", tc.cli, tc.relay)) {
				t.Fatalf("errors should carry the version reason: exit %d\n%s", code, errOut)
			}
		})
	}
}

// Within a major version, neither side being newer is worth a warning, nor
// blaming in an error.
func TestNoWarningWithinAMajor(t *testing.T) {
	for _, tc := range []struct{ cli, relay string }{
		{"v1.3.0", "v1.2.0"}, // CLI newer
		{"v1.1.0", "v1.2.0"}, // CLI older
	} {
		t.Run(tc.cli+" vs "+tc.relay, func(t *testing.T) {
			e := newCLIEnv(t)
			e.restartRelayAt(tc.relay)
			setVersion(t, tc.cli)
			if code, _, errOut := e.tsrelay("hooks", "ls"); code != 0 || errOut != "" {
				t.Fatalf("exit %d, stderr %q", code, errOut)
			}
			code, _, errOut := e.tsrelay("hooks", "create", "--channel", "Not Valid")
			if code != 1 || strings.Contains(errOut, "differs from the relay") || strings.Contains(errOut, "tsrelay update") {
				t.Fatalf("exit %d, the error should not blame the version:\n%s", code, errOut)
			}
		})
	}
}

// An unsupported_field from an older relay of the same major is explained
// by its own text, which already says the relay needs upgrading.
func TestNoVersionBlameForNewerCLI(t *testing.T) {
	setVersion(t, "v1.3.0")
	c := &cli{}
	c.version.Store(&versionSeen{relay: "v1.2.0", skew: skewCLINewer})
	ce := c.explain(&problem.Problem{Code: problem.UnsupportedField, Status: 422})
	if strings.Contains(ce.Reason, "differs from the relay") {
		t.Fatalf("reason %q", ce.Reason)
	}
	c.version.Store(&versionSeen{relay: "v2.0.0", skew: skewMajor})
	if ce := c.explain(&problem.Problem{Code: problem.UnsupportedField, Status: 422}); !strings.Contains(ce.Reason, "differs from the relay") {
		t.Fatalf("a major mismatch should be blamed: %q", ce.Reason)
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
	e.restartRelayAt("v2.0.0")
	setVersion(t, "v1.0.0")
	code, out, errOut := e.tsrelay("hooks", "ls", "--json")
	var w struct{ Warning, CLIVersion, RelayVersion string }
	if code != 0 || json.Unmarshal([]byte(strings.SplitN(errOut, "\n", 2)[0]), &w) != nil ||
		w.CLIVersion != "v1.0.0" || w.RelayVersion != "v2.0.0" || !strings.Contains(w.Warning, "different major versions") {
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
	e.restartRelayAt("v2.0.0")
	setVersion(t, "v1.0.0")
	r := e.start()
	waitOutput(t, r.stderr, "connected")
	r.cancel()
	<-r.exit
	if n := strings.Count(r.stderr.String(), "warning:"); n != 1 {
		t.Fatalf("%d warnings:\n%s", n, r.stderr)
	}
}

// Every request to the relay says which tsrelay sends it: the API's and the
// tunnel's upgrade.
func TestUserAgent(t *testing.T) {
	e := newCLIEnv(t)
	setVersion(t, "v1.2.3")
	var mu sync.Mutex
	seen := map[string]string{}
	e.handlerMu.Lock()
	next := e.handler
	e.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.URL.Path] = r.UserAgent()
		mu.Unlock()
		next.ServeHTTP(w, r)
	})
	e.handlerMu.Unlock()
	r := e.start()
	waitOutput(t, r.stderr, "connected")
	r.cancel()
	<-r.exit
	want := fmt.Sprintf("tsrelay/v1.2.3 (%s/%s)", runtime.GOOS, runtime.GOARCH)
	mu.Lock()
	defer mu.Unlock()
	for _, path := range []string{"/hooks", "/tunnel"} {
		if seen[path] != want {
			t.Errorf("%s: User-Agent %q, want %q (saw %v)", path, seen[path], want, seen)
		}
	}
}
