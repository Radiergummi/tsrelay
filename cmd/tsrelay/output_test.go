package main

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Radiergummi/tsrelay/internal/api"
	"github.com/Radiergummi/tsrelay/internal/client"
	"github.com/Radiergummi/tsrelay/internal/problem"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
	"github.com/charmbracelet/x/ansi"
)

const testURL = "https://r/~m/cb"

// testOutput returns an output writing to buffers, with a fixed clock.
func testOutput(t *testing.T, asJSON bool) (o *output, stdout, stderr *syncBuf) {
	t.Helper()
	stdout, stderr = &syncBuf{}, &syncBuf{}
	o = newOutput(&cli{stdout: stdout, stderr: stderr, json: asJSON})
	o.now = func() time.Time { return time.Date(2026, 10, 14, 15, 4, 5, 0, time.Local) }
	o.inspectBase = "https://r"
	return o, stdout, stderr
}

// sampleEvents drives o through everything a tunnel run can report.
func sampleEvents(o *output) {
	o.headline("http://localhost:8080")
	o.url(api.Hook{ID: "h_1", URL: testURL, Path: "cb", Channel: "default", Ephemeral: true}, false)
	state := o.onState("http://localhost:8080", 30*time.Second)
	state(true, nil)
	for _, status := range []int{200, 302, 404, 502, problem.StatusUpstreamUnreachable} {
		o.call(client.Call{Hook: "h_1", Method: "POST", Path: "/done", Status: status, Duration: 12 * time.Millisecond, Sender: "tag:ci", CallID: "c_1"})
	}
	o.call(client.Call{Method: "GET", Path: "/" + strings.Repeat("very-long-segment/", 20), Status: 200, Duration: time.Second, Sender: "node build-01", CallID: "c_2"})
	state(false, errors.New("boom"))
	state(true, nil)
	o.onState("", 0)(false, errors.New("boom"))
	o.upstreamError(errors.New("could not reach http://localhost:8080: connection refused"))
	o.dropped(api.Hook{ID: "h_1", URL: testURL}, client.DropRevoked)
	o.finish(tunnel.ErrNoHooks)
}

func TestOutputText(t *testing.T) {
	t.Setenv("TTY_FORCE", "0")
	o, stdout, stderr := testOutput(t, false)
	sampleEvents(o)
	if stdout.String() != testURL+"\n" {
		t.Errorf("stdout = %q, want only the URL", stdout.String())
	}
	got := stderr.String()
	for _, want := range []string{
		"tsrelay  forwarding to http://localhost:8080\n",
		"hook h_1  ephemeral  channel default\n",
		"connected\n",
		"15:04:05 POST /done → 502  12ms  tag:ci  https://r/inspect/c_1\n",
		"15:04:05 POST /done → 564 Upstream unreachable  12ms  tag:ci  https://r/inspect/c_1\n",
		"tunnel lost (boom), reconnecting… calls are held for 30s\n",
		"reconnected\n",
		"calls fail until it is back\n",
		"your grants no longer allow serving " + testURL + "\n",
		"no hooks left to serve; stopping\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("stderr lacks %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "your grants") != 1 {
		t.Errorf("attached must not be reported as a drop:\n%s", got)
	}
	if strings.ContainsAny(got, "\x1b●↗") {
		t.Errorf("plain output has escapes or glyphs:\n%q", got)
	}
}

func TestOutputFancy(t *testing.T) {
	forceTerminal(t)
	o, stdout, stderr := testOutput(t, false)
	sampleEvents(o)
	golden(t, "tunnel-stdout", stdout.String())
	golden(t, "tunnel-stderr", stderr.String())
	if !strings.Contains(stderr.String(), "\x1b]8;;https://r/inspect/c_1") {
		t.Error("call IDs must be hyperlinks")
	}
}

func TestOutputNoHyperlinks(t *testing.T) {
	forceTerminal(t)
	stdout, stderr := &syncBuf{}, &syncBuf{}
	o := newOutput(&cli{stdout: stdout, stderr: stderr, noLinks: true})
	o.inspectBase = "https://r"
	o.call(client.Call{Method: "POST", Path: "/done", Status: 200, CallID: "c_1"})
	if s := stderr.String(); strings.Contains(s, "\x1b]8;") || !strings.Contains(s, "https://r/inspect/c_1") {
		t.Fatalf("want the full URL and no hyperlink: %q", s)
	}
}

// Review focus 5: a long path in a narrow terminal is truncated, not
// wrapped, and nothing panics.
func TestOutputNarrowTerminal(t *testing.T) {
	forceTerminal(t)
	o, _, stderr := testOutput(t, false)
	o.err.width = 40
	o.call(client.Call{Method: "POST", Path: "/" + strings.Repeat("x", 300), Status: 200, Sender: "tag:ci", CallID: "c_1"})
	line := ansi.Strip(strings.TrimRight(stderr.String(), "\n"))
	if !strings.Contains(line, "…") || strings.Contains(line, strings.Repeat("x", 11)) {
		t.Fatalf("path not truncated to its 10-cell minimum: %q", line)
	}
}

func TestOutputJSONOnTerminal(t *testing.T) {
	forceTerminal(t)
	o, stdout, _ := testOutput(t, true)
	o.url(api.Hook{ID: "h_1", URL: testURL}, false)
	if !strings.Contains(stdout.String(), "{\n  \"event\": \"url\"") {
		t.Fatalf("JSON on a terminal must be indented:\n%s", stdout.String())
	}
}

func TestOutputJSON(t *testing.T) {
	t.Setenv("TTY_FORCE", "0")
	o, stdout, stderr := testOutput(t, true)
	o.url(api.Hook{ID: "h_1", URL: testURL}, false)
	o.onState("http://localhost:8080", time.Second)(true, nil)
	o.call(client.Call{Hook: "h_1", Method: "POST", Path: "/done", Status: 200, Duration: 1500 * time.Microsecond, Sender: "tag:ci"})
	o.dropped(api.Hook{ID: "h_1", URL: testURL}, client.DropReplaced)
	if code := o.finish(tunnel.ErrNoHooks); code != 1 {
		t.Fatalf("finish = %d, want 1", code)
	}

	var events []jsonEvent
	for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		var e jsonEvent
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("stdout line is not JSON: %q", line)
		}
		events = append(events, e)
	}
	var names []string
	for _, e := range events {
		names = append(names, e.Event)
	}
	if !slices.Equal(names, []string{"url", "connected", "call", "stopped", "stopped"}) {
		t.Fatalf("events = %v", names)
	}
	if e := events[2]; e.Hook != "h_1" || e.Path != "/done" || e.DurationMS != 1.5 || e.Sender != "tag:ci" {
		t.Errorf("call = %+v", e)
	}
	if e := events[3]; e.Hook != "h_1" || e.Reason != "replaced" || e.ExitCode != nil {
		t.Errorf("hook stopped = %+v", e)
	}
	if e := events[4]; e.Reason != "no-hooks" || e.ExitCode == nil || *e.ExitCode != 1 {
		t.Errorf("command stopped = %+v", e)
	}
	if stderr.String() != "" {
		t.Errorf("JSON mode wrote to stderr: %q", stderr.String())
	}
}

// Minor 7: a fancy call line with no inspector link (no call ID) must not
// end in trailing spaces for the dropped ref column.
func TestCallLineNoInspectLink(t *testing.T) {
	forceTerminal(t)
	o, _, stderr := testOutput(t, false)
	o.call(client.Call{Method: "POST", Path: "/done", Status: 200, Duration: 12 * time.Millisecond, Sender: "tag:ci"})
	line := strings.TrimRight(stderr.String(), "\n")
	if strings.HasSuffix(line, " ") {
		t.Fatalf("fancy call line without a link must not end in trailing spaces: %q", line)
	}
}

func TestOutputMarksAbortedCalls(t *testing.T) {
	t.Setenv("TTY_FORCE", "0")
	o, _, stderr := testOutput(t, false)
	o.call(client.Call{Method: "GET", Path: "/stream", Status: 200, Aborted: true})
	if !strings.Contains(stderr.String(), "→ 200 aborted") {
		t.Fatalf("plain line = %q, want the status marked aborted", stderr.String())
	}
	j, stdout, _ := testOutput(t, true)
	j.call(client.Call{Method: "GET", Path: "/stream", Status: 200, Aborted: true})
	if !strings.Contains(stdout.String(), `"aborted":true`) {
		t.Fatalf("JSON event = %q, want aborted", stdout.String())
	}
}

func TestOutputCallLinks(t *testing.T) {
	t.Setenv("TTY_FORCE", "0")
	o, _, stderr := testOutput(t, false)
	o.call(client.Call{CallID: "c_7f3a", Method: "POST", Path: "/done", Status: 200, Duration: 12 * time.Millisecond, Sender: "tag:ci"})
	o.call(client.Call{Method: "GET", Path: "/old", Status: 200}) // from a relay that sends no call ID
	lines := strings.Split(strings.TrimSpace(stderr.String()), "\n")
	if len(lines) != 2 || !strings.HasSuffix(lines[0], "POST /done → 200  12ms  tag:ci  https://r/inspect/c_7f3a") || strings.Contains(lines[1], "inspect") {
		t.Fatalf("call lines:\n%s", stderr.String())
	}

	o, stdout, _ := testOutput(t, true)
	o.call(client.Call{CallID: "c_7f3a", Method: "POST", Path: "/done", Status: 200})
	var e jsonEvent
	if err := json.Unmarshal([]byte(stdout.String()), &e); err != nil || e.CallID != "c_7f3a" || e.Inspect != "https://r/inspect/c_7f3a" {
		t.Fatalf("call event = %+v, %v", e, err)
	}
}
