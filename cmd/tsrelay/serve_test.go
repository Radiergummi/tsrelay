package main

import (
	"net/netip"
	"slices"
	"strings"
	"testing"
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
	if code != 2 || !strings.Contains(out, "holdTimeout") {
		t.Fatalf("exit %d, stderr %q", code, out)
	}
}

// --response-timeout still works, as a deprecated alias of
// --hook-response-timeout-default.
func TestServeResponseTimeoutAlias(t *testing.T) {
	code, out := serveExit(t, "--response-timeout=1h") // above the 30m maximum
	if code != 2 || !strings.Contains(out, "deprecated") || !strings.Contains(out, "responseTimeout") {
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

func TestServeRejectsEmptyHoldQueue(t *testing.T) {
	code, out := serveExit(t, "--hold-queue=0")
	if code != 2 || !strings.Contains(out, "--hold-queue") {
		t.Fatalf("exit %d, stderr %q", code, out)
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
