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
	"github.com/Radiergummi/tsrelay/internal/tunnel"
)

const testURL = "https://r/~m/cb"

func urlOfTest(string) string { return testURL }

func TestOutputText(t *testing.T) {
	var stdout, stderr syncBuf
	o := newOutput(&stdout, &stderr, false)
	o.url(api.Hook{ID: "h_1", URL: testURL}, false)
	state := o.onState([]string{testURL}, "http://localhost:8080", 30*time.Second)
	state(true, nil)
	state(false, errors.New("boom"))
	state(true, nil)
	o.onState(nil, "", 0)(false, errors.New("boom"))
	o.dropped(tunnel.Control{Type: tunnel.ControlRevoked, Hooks: []tunnel.HookInfo{{ID: "h_1"}}}, urlOfTest)
	o.dropped(tunnel.Control{Type: tunnel.ControlAttached, Hooks: []tunnel.HookInfo{{ID: "h_1"}}}, urlOfTest)
	o.finish(tunnel.ErrNoHooks)

	if stdout.String() != testURL+"\n" {
		t.Errorf("stdout = %q, want only the URL", stdout.String())
	}
	for _, want := range []string{
		"Forwarding " + testURL + " → http://localhost:8080",
		"Tunnel lost (boom); reconnecting… calls are held for 30s.",
		"Tunnel restored.",
		"calls fail until it is back",
		"tsrelay: your grants no longer allow serving " + testURL + ".",
		"no hooks left to serve",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr.String())
		}
	}
	if strings.Count(stderr.String(), "tsrelay: your grants") != 1 {
		t.Errorf("attached must not be reported as a drop:\n%s", stderr.String())
	}
}

func TestOutputJSON(t *testing.T) {
	var stdout, stderr syncBuf
	o := newOutput(&stdout, &stderr, true)
	o.url(api.Hook{ID: "h_1", URL: testURL}, false)
	o.onState([]string{testURL}, "http://localhost:8080", time.Second)(true, nil)
	o.call(client.Call{Hook: "h_1", Method: "POST", Path: "/done", Status: 200, Duration: 1500 * time.Microsecond, Sender: "tag:ci"})
	o.dropped(tunnel.Control{Type: tunnel.ControlReplaced, Hooks: []tunnel.HookInfo{{ID: "h_1"}}}, urlOfTest)
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

func TestUpdateSeqs(t *testing.T) {
	s := updateSeqs{}
	if !s.fresh("h_1", 2) {
		t.Fatal("first update must be fresh")
	}
	if s.fresh("h_1", 1) || s.fresh("h_1", 2) {
		t.Fatal("an older or repeated update must not be fresh")
	}
	if !s.fresh("h_2", 1) {
		t.Fatal("sequence numbers are tracked per hook")
	}
	if !s.fresh("h_1", 0) || !s.fresh("h_1", 0) {
		t.Fatal("0 (a relay without sequence numbers) is always fresh")
	}
}

func TestOutputCallLinks(t *testing.T) {
	var stdout, stderr syncBuf
	o := newOutput(&stdout, &stderr, false)
	o.inspectBase = "https://r"
	o.call(client.Call{CallID: "c_7f3a", Method: "POST", Path: "/done", Status: 200, Duration: 12 * time.Millisecond, Sender: "tag:ci"})
	o.call(client.Call{Method: "GET", Path: "/old", Status: 200}) // from a relay that sends no call ID
	lines := strings.Split(strings.TrimSpace(stderr.String()), "\n")
	if len(lines) != 2 || !strings.HasSuffix(lines[0], "POST /done → 200  12ms  tag:ci  https://r/inspect/c_7f3a") || strings.Contains(lines[1], "inspect") {
		t.Fatalf("call lines:\n%s", stderr.String())
	}

	stdout = syncBuf{}
	o = newOutput(&stdout, &stderr, true)
	o.inspectBase = "https://r"
	o.call(client.Call{CallID: "c_7f3a", Method: "POST", Path: "/done", Status: 200})
	var e jsonEvent
	if err := json.Unmarshal([]byte(stdout.String()), &e); err != nil || e.CallID != "c_7f3a" || e.Inspect != "https://r/inspect/c_7f3a" {
		t.Fatalf("call event = %+v, %v", e, err)
	}
}
