package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Radiergummi/tsrelay/internal/hooks"
)

func TestAttach(t *testing.T) {
	e := newCLIEnv(t)
	ctx := context.Background()
	a, err := e.api().CreateHook(ctx, hooks.CreateRequest{Path: "pay"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := e.api().CreateHook(ctx, hooks.CreateRequest{Path: "ship/v1"})
	if err != nil {
		t.Fatal(err)
	}
	// By ID and by path; naming a hook twice attaches it once.
	r := e.startCmd("attach", a.ID, "ship/v1", "pay", "--to", e.local.URL, "--relay", e.relayURL)
	waitOutput(t, r.stdout, a.URL)
	waitOutput(t, r.stdout, b.URL)
	waitOutput(t, r.stderr, "connected")
	if out := r.stdout.String(); out != a.URL+"\n"+b.URL+"\n" {
		t.Fatalf("stdout = %q, want both URLs once", out)
	}
	for _, u := range []string{a.URL, b.URL} {
		if code := e.send(u); code != http.StatusAccepted {
			t.Fatalf("send to %s = %d", u, code)
		}
	}

	// Losing one hook keeps the other served.
	if err := e.api().DeleteHook(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	waitOutput(t, r.stderr, a.URL+" was deleted")
	if code := e.send(b.URL); code != http.StatusAccepted {
		t.Fatalf("send to the remaining hook = %d", code)
	}
	if err := e.api().DeleteHook(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	if code := waitExit(t, r); code != exitHookDeleted {
		t.Fatalf("exit with no hooks left = %d, want 3", code)
	}
}

func TestAttachFollowsHookUpdate(t *testing.T) {
	e := newCLIEnv(t)
	ctx := context.Background()
	a, err := e.api().CreateHook(ctx, hooks.CreateRequest{Path: "pay"})
	if err != nil {
		t.Fatal(err)
	}
	r := e.startCmd("attach", a.ID, "--to", e.local.URL, "--relay", e.relayURL)
	waitOutput(t, r.stdout, a.URL)
	waitOutput(t, r.stderr, "connected")
	moved, err := e.api().UpdateHook(ctx, a.ID, hooks.UpdateRequest{Path: hooks.Opt[string]{Set: true, Value: new("billing")}})
	if err != nil {
		t.Fatal(err)
	}
	waitOutput(t, r.stdout, moved.URL)
	// The new URL names the hook from now on.
	if err := e.api().DeleteHook(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	waitOutput(t, r.stderr, moved.URL+" was deleted")
}

// A hook deleted while the tunnel is down is dropped on the redial; the
// others keep being served.
func TestAttachKeepsServingAfterRefusedRedial(t *testing.T) {
	e := newCLIEnv(t)
	ctx := context.Background()
	pay, err := e.api().CreateHook(ctx, hooks.CreateRequest{Path: "pay"})
	if err != nil {
		t.Fatal(err)
	}
	ship, err := e.api().CreateHook(ctx, hooks.CreateRequest{Path: "ship"})
	if err != nil {
		t.Fatal(err)
	}
	r := e.startCmd("attach", "pay", "ship", "--to", e.local.URL, "--relay", e.relayURL)
	waitOutput(t, r.stderr, "connected")
	if code := e.send(ship.URL); code != http.StatusAccepted { // the relay has registered the tunnel
		t.Fatalf("send = %d", code)
	}

	e.offline.Store(true)
	r.hc.CloseIdleConnections()
	e.srv.Close() // drops the tunnel; the CLI cannot redial while offline
	waitOutput(t, r.stderr, "tunnel lost")
	if err := e.api().DeleteHook(ctx, pay.ID); err != nil {
		t.Fatal(err)
	}
	e.offline.Store(false)

	waitOutput(t, r.stderr, pay.URL+" was deleted")
	waitOutput(t, r.stderr, "reconnected")
	if code := e.send(ship.URL); code != http.StatusAccepted {
		t.Fatalf("send to the remaining hook = %d", code)
	}
	select {
	case code := <-r.exit:
		t.Fatalf("CLI exited with %d; stderr:\n%s", code, r.stderr)
	default:
	}
}

func TestAttachRefusals(t *testing.T) {
	e := newCLIEnv(t)
	if code, _, errOut := e.tsrelay("attach", "nope", "--to", "8080"); code != 1 || !strings.Contains(errOut, `"nope"`) {
		t.Fatalf("unknown hook: exit %d\n%s", code, errOut)
	}
}

// Minor 2: with --json, an error must also land on stderr as JSON, not only
// flattened into the stopped event's Error string on stdout.
func TestAttachRefusalsJSON(t *testing.T) {
	e := newCLIEnv(t)
	code, out, errOut := e.tsrelay("attach", "nope", "--to", "8080", "--json")
	if code != 1 {
		t.Fatalf("exit %d, want 1\n%s", code, errOut)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var last jsonEvent
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &last); err != nil || last.Event != "stopped" {
		t.Fatalf("stdout's last line should be the stopped event: %v\n%s", err, out)
	}
	var je jsonError
	if err := json.Unmarshal([]byte(errOut), &je); err != nil || !strings.Contains(je.Error.Title, "nope") {
		t.Fatalf("stderr should be a JSON error mentioning %q: %v\n%s", "nope", err, errOut)
	}
}
