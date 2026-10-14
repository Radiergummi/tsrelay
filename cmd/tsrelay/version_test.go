package main

import (
	"strings"
	"testing"
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
