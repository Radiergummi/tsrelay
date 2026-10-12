package main

import (
	"context"
	"strings"
	"testing"
)

func TestVersionCommand(t *testing.T) {
	saved := version
	version = "v1.2.3"
	defer func() { version = saved }()

	var out strings.Builder
	if code := run(context.Background(), []string{"version"}, &out, new(nopWriter), deps{}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if got := out.String(); got != "tsrelay v1.2.3\n" {
		t.Fatalf("output = %q", got)
	}
}

func TestVersionDefaultsToDev(t *testing.T) {
	if version != "dev" {
		t.Fatalf("version = %q, want the unmodified build to say %q", version, "dev")
	}
}
