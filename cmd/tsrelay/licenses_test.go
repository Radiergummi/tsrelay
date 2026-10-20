package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"tailscale.com/ipn/ipnstate"

	"github.com/Radiergummi/tsrelay/internal/sbom"
)

// requireSBOM skips tests of the notices in a build without the SBOM. CI's
// sbom job builds it and runs these tests.
func requireSBOM(t *testing.T) {
	t.Helper()
	if !sbom.Available() {
		t.Skip("this build has no SBOM; run 'make sbom'")
	}
}

func TestLicensesWithoutSBOM(t *testing.T) {
	if sbom.Available() {
		t.Skip("this build has an SBOM")
	}
	var stdout, stderr syncBuf
	if code := run(t.Context(), []string{"licenses"}, &stdout, &stderr, deps{Status: noTailscale}); code == 0 {
		t.Fatal("licenses exited 0 without notices to print")
	}
	if !strings.Contains(stderr.String(), "no license notices") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func noTailscale(context.Context) (*ipnstate.Status, error) {
	return nil, errors.New("tailscale is not running")
}

func TestLicensesPrintsTheNotices(t *testing.T) {
	requireSBOM(t)
	t.Setenv("TSRELAY_RELAY", "")
	var stdout syncBuf
	if code := run(t.Context(), []string{"licenses"}, &stdout, new(nopWriter), deps{Status: noTailscale}); code != 0 {
		t.Fatalf("licenses exited %d", code)
	}
	out := stdout.String()
	// fang is MIT-licensed: its copyright notice must travel with the binary.
	for _, want := range []string{"charm.land/fang/v2 v", "Copyright (c) 2024-2026 Charmbracelet, Inc"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q", want)
		}
	}
	if strings.Contains(out, "The relay lists them") {
		t.Error("named a relay, but none is configured")
	}
}

func TestLicensesNamesTheRelaysPage(t *testing.T) {
	requireSBOM(t)
	var stdout syncBuf
	if code := run(t.Context(), []string{"licenses", "--relay", "tsrelay.example.ts.net"}, &stdout, new(nopWriter), deps{Status: noTailscale}); code != 0 {
		t.Fatalf("licenses exited %d", code)
	}
	out := stdout.String()
	if want := "The relay lists them at https://tsrelay.example.ts.net/licenses\n"; !strings.HasSuffix(out, want) {
		t.Errorf("output ends %q, want %q", out[max(0, len(out)-80):], want)
	}
}
