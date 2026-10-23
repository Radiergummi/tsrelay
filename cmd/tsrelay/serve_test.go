package main

import (
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/Radiergummi/tsrelay/internal/inspect"
)

func serveExit(t *testing.T, args ...string) (int, string) {
	t.Helper()
	var stderr syncBuf
	code := run(t.Context(), append([]string{"serve"}, args...), new(nopWriter), &stderr, deps{})
	return code, stderr.String()
}

func TestServeRejectsInvalidSlugOverride(t *testing.T) {
	code, out := serveExit(t, "--user-slug-override", "alice@example.com=Alice B")
	if code != 2 || !strings.Contains(out, "--user-slug-override") || !strings.Contains(out, "Alice B") {
		t.Fatalf("exit %d, stderr %q", code, out)
	}
}

func TestServeChecksHookPolicy(t *testing.T) {
	code, out := serveExit(t, "--hook-hold-timeout-default=10m") // above the 5m maximum
	if code != 2 || !strings.Contains(out, "--hook-hold-timeout-default 10m0s exceeds --hook-hold-timeout-max 5m0s") {
		t.Fatalf("exit %d, stderr %q", code, out)
	}
}

func TestAllowedHostsDropsEmpty(t *testing.T) {
	got := allowedHosts("", "tsrelay", []netip.Addr{netip.MustParseAddr("100.64.0.1")})
	if !slices.Equal(got, []string{"tsrelay", "100.64.0.1"}) {
		t.Fatalf("allowedHosts = %q; an empty name would let requests without a Host header through", got)
	}
	got = allowedHosts("tsrelay.tail1.ts.net", "tsrelay", nil)
	if !slices.Equal(got, []string{"tsrelay.tail1.ts.net", "tsrelay"}) {
		t.Fatalf("allowedHosts = %q", got)
	}
}

func TestReleaseCheckOptOut(t *testing.T) {
	if releaseChecker(true, nil) != nil {
		t.Error("a dev relay checks for releases")
	}
	setVersion(t, "v0.3.0")
	if releaseChecker(true, nil) == nil {
		t.Error("a released relay doesn't check for releases")
	}
	if releaseChecker(false, nil) != nil {
		t.Error("--update-check=false still checks for releases")
	}
	t.Setenv("TSRELAY_UPDATE_CHECK", "nope")
	if code, out := serveExit(t); code != 2 || !strings.Contains(out, "TSRELAY_UPDATE_CHECK") {
		t.Errorf("TSRELAY_UPDATE_CHECK=nope: exit %d, stderr %q", code, out)
	}
}

func TestServeRejectsEmptyHoldQueue(t *testing.T) {
	code, out := serveExit(t, "--hook-hold-queue=0")
	if code != 2 || !strings.Contains(out, "--hook-hold-queue") {
		t.Fatalf("exit %d, stderr %q", code, out)
	}
}

func TestServeRejectsBadCaptureFlags(t *testing.T) {
	for _, args := range [][]string{{"--capture-limit-bytes=0"}, {"--capture-calls=0"}} {
		if code, out := serveExit(t, args...); code != 2 || !strings.Contains(out, strings.Split(strings.TrimPrefix(args[0], "--"), "=")[0]) {
			t.Errorf("%v: exit %d, stderr %q", args, code, out)
		}
	}
}

func TestServeRejectsBadRevalidateInterval(t *testing.T) {
	for _, v := range []string{"0", "-1s"} {
		// An unknown flag also exits 2, so check the message too.
		if code, out := serveExit(t, "--revalidate-interval="+v); code != 2 || !strings.Contains(out, "--revalidate-interval must be positive") {
			t.Errorf("--revalidate-interval=%s: exit %d, stderr %q", v, code, out)
		}
	}
}

func TestServeFlagNames(t *testing.T) {
	fs := newServeCmd(&cli{}).Flags()
	for _, name := range []string{"user-max-hooks", "user-max-tunnels", "user-max-hook-ttl", "hook-hold-queue", "capture-limit-bytes", "otel-propagation"} {
		if fs.Lookup(name) == nil {
			t.Errorf("serve has no --%s", name)
		}
	}
	for _, name := range []string{"max-hooks-default", "max-tunnels-default", "max-hook-ttl-default", "hold-queue", "capture-limit", "trace-propagation"} {
		if fs.Lookup(name) != nil {
			t.Errorf("serve still has --%s", name)
		}
	}
}

func TestRedactHeadersAddsToDefaults(t *testing.T) {
	fs := newServeCmd(&cli{}).Flags()
	if err := fs.Parse([]string{"--redact-headers", "X-Secret,authorization"}); err != nil {
		t.Fatal(err)
	}
	extra := strings.Split(fs.Lookup("redact-headers").Value.String(), ",")
	in := inspect.New(inspect.Config{Redact: extra})
	kept, redacted := in.Redact(http.Header{"X-Secret": {"s"}, "Authorization": {"a"}, "Cookie": {"c"}, "X-Ok": {"o"}})
	if !slices.Equal(redacted, []string{"Authorization", "Cookie", "X-Secret"}) || len(kept) != 1 {
		t.Fatalf("redacted %v, kept %v", redacted, kept)
	}
}

// Minor 3: fang capitalizes a flag description's first word, so a quoted
// value (here "json" or "text") must never be first.
