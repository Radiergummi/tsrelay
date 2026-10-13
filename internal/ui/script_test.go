package ui

import (
	"os/exec"
	"testing"
)

// TestScript runs the script's unit tests with node. They cover the pure
// helpers in static/app.js; the pages are tested through the server.
func TestScript(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	out, err := exec.Command(node, "--test", "testdata/app_test.mjs").CombinedOutput()
	if err != nil {
		t.Fatalf("node --test failed: %v\n%s", err, out)
	}
}
