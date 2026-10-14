package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/Radiergummi/tsrelay/internal/api"
	"github.com/Radiergummi/tsrelay/internal/hooks"
)

func sampleHooks() []api.Hook {
	return []api.Hook{
		{ID: "h_1", URL: "https://r/~m/payments", Path: "payments", Channel: "default", Ephemeral: true,
			Tunnel: &api.TunnelInfo{Node: "laptop", Target: "http://localhost:8080"},
			Effective: hooks.Effective{AttachPolicy: "takeover", HostHeader: "preserve", HoldTimeout: hooks.Duration(30 * time.Second),
				ResponseTimeout: hooks.Duration(time.Minute), IdleTimeout: hooks.Duration(time.Minute),
				RateLimit: hooks.RateLimit{PerSecond: 10, Burst: 20}, Capture: true}},
		{ID: "h_2", URL: "https://r/~m/ship/v1", Path: "ship/v1", Channel: "ci", Description: "shipping callbacks"},
	}
}

func TestHookListPlain(t *testing.T) {
	t.Setenv("TTY_FORCE", "0")
	var buf, errBuf bytes.Buffer
	writeHookList(newStream(&buf, false, false), newStream(&errBuf, false, false), sampleHooks())
	want := "ID\tPATH\tCHANNEL\tKIND\tTUNNEL\tEXPIRES\tURL\n" +
		"h_1\tpayments\tdefault\tephemeral\tlaptop\t-\thttps://r/~m/payments\n" +
		"h_2\tship/v1\tci\tpersistent\t-\t-\thttps://r/~m/ship/v1\n"
	if buf.String() != want {
		t.Fatalf("got:\n%q\nwant:\n%q", buf.String(), want)
	}
}

func TestHookListExpiry(t *testing.T) {
	t.Setenv("TTY_FORCE", "0")
	at := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	h := sampleHooks()[1]
	h.ExpiresAt = &at
	var buf, errBuf bytes.Buffer
	writeHookList(newStream(&buf, false, false), newStream(&errBuf, false, false), []api.Hook{h})
	if !strings.Contains(buf.String(), "\t"+at.Local().Format(time.DateTime)+"\t") {
		t.Fatalf("expiry not in local time:\n%s", buf.String())
	}
}

func TestHookListFancy(t *testing.T) {
	forceTerminal(t)
	var buf, errBuf bytes.Buffer
	writeHookList(newStream(&buf, false, false), newStream(&errBuf, false, false), sampleHooks())
	golden(t, "hooks-list", buf.String())
}

// Review finding: a fancy, empty list must not write its "no hooks yet"
// hint to stdout, which hooks commands otherwise reserve for the hook's
// URL or data; the hint belongs on stderr.
func TestHookListEmptyFancy(t *testing.T) {
	forceTerminal(t)
	var buf, errBuf bytes.Buffer
	writeHookList(newStream(&buf, false, false), newStream(&errBuf, false, false), nil)
	if buf.String() != "" {
		t.Fatalf("stdout = %q, want empty", buf.String())
	}
	if !strings.Contains(errBuf.String(), "no hooks yet; tsrelay http <port> creates one") {
		t.Fatalf("stderr = %q, want the hint", errBuf.String())
	}
}

// A plain, empty list prints only the header row, on stdout, and nothing
// on stderr.
func TestHookListEmptyPlain(t *testing.T) {
	t.Setenv("TTY_FORCE", "0")
	var buf, errBuf bytes.Buffer
	writeHookList(newStream(&buf, false, false), newStream(&errBuf, false, false), nil)
	if buf.String() != "ID\tPATH\tCHANNEL\tKIND\tTUNNEL\tEXPIRES\tURL\n" {
		t.Fatalf("stdout = %q", buf.String())
	}
	if errBuf.String() != "" {
		t.Fatalf("stderr = %q, want empty", errBuf.String())
	}
}

func TestHookShow(t *testing.T) {
	t.Setenv("TTY_FORCE", "0")
	var buf bytes.Buffer
	writeHookShow(newStream(&buf, false, false), sampleHooks()[0])
	for _, want := range []string{
		"id: h_1\n", "url: https://r/~m/payments\n", "kind: ephemeral\n", "description: -\n",
		"tunnel: laptop → http://localhost:8080\n", "hold timeout: 30s\n", "max body bytes: unlimited\n",
		"rate limit: 10/20\n", "capture: yes\n",
	} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("hooks show lacks %q:\n%s", want, buf.String())
		}
	}
	if strings.Contains(buf.String(), "\x1b") {
		t.Errorf("plain output has escapes: %q", buf.String())
	}
	forceTerminal(t)
	buf.Reset()
	writeHookShow(newStream(&buf, false, false), sampleHooks()[0])
	golden(t, "hooks-show", buf.String())
}

// A hook with no tunnel and no rate limit shows dashes for both, not an
// empty value or a zero rate limit's "0/0".
func TestHookShowDashes(t *testing.T) {
	t.Setenv("TTY_FORCE", "0")
	var buf bytes.Buffer
	writeHookShow(newStream(&buf, false, false), sampleHooks()[1])
	for _, want := range []string{"tunnel: -\n", "rate limit: -\n"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("hooks show lacks %q:\n%s", want, buf.String())
		}
	}
}
