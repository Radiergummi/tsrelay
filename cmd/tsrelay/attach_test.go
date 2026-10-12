package main

import (
	"context"
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
	r := e.startCmd("attach", a.ID, "ship/v1", "pay", "--forward-to", e.local.URL, "--relay", e.relayURL)
	waitOutput(t, r.stderr, "Forwarding "+a.URL)
	waitOutput(t, r.stderr, "Forwarding "+b.URL)
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
	if code := waitExit(t, r); code != 1 {
		t.Fatalf("exit with no hooks left = %d, want 1", code)
	}
}

func TestAttachRefusals(t *testing.T) {
	e := newCLIEnv(t)
	if code, _, errOut := e.tsrelay("attach", "nope", "--forward-to", "8080"); code != 1 || !strings.Contains(errOut, `"nope"`) {
		t.Fatalf("unknown hook: exit %d\n%s", code, errOut)
	}
	for _, args := range [][]string{{"attach", "--forward-to", "8080"}, {"attach", "pay"}, {"attach", "pay", "--forward-to", "ftp://x"}} {
		if code := run(context.Background(), args, &syncBuf{}, &syncBuf{}, deps{}); code != 2 {
			t.Errorf("run(%q) = %d, want 2", args, code)
		}
	}
}
