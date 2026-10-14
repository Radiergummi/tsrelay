package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestExitStatus(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want int
	}{
		{nil, 0},
		{usagef("bad %s", "flag"), 2},
		{fmt.Errorf("wrapped: %w", usagef("x")), 2},
		{exitCodeError(3), 3},
		{fmt.Errorf("wrapped: %w", exitCodeError(0)), 0},
		{errors.New(`unknown command "bogus" for "tsrelay"`), 2}, // cobra's own, untyped
		{errors.New("boom"), 1},
	} {
		if got := exitStatus(tc.err); got != tc.want {
			t.Errorf("exitStatus(%v) = %d, want %d", tc.err, got, tc.want)
		}
	}
}

func TestRunUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		nil,                         // no command
		{"bogus"},                   // unknown command
		{"serve", "--no-such-flag"}, // unknown flag
		{"serve", "extra"},          // stray argument
		{"serve", "-json"},          // single-dash long flags are gone
		{"serve", "--tls=maybe"},    // bad value
	} {
		var stderr syncBuf
		if code := run(t.Context(), args, new(nopWriter), &stderr, deps{}); code != 2 {
			t.Errorf("run(%q) = %d, want 2; stderr:\n%s", args, code, stderr.String())
		}
	}
}

func TestRunHelp(t *testing.T) {
	var stdout syncBuf
	if code := run(t.Context(), []string{"--help"}, &stdout, new(nopWriter), deps{}); code != 0 {
		t.Fatalf("--help exited %d", code)
	}
	for _, want := range []string{"http", "attach", "hooks", "url", "serve", "completion"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("help lacks %q:\n%s", want, stdout.String())
		}
	}
}

func TestCompletion(t *testing.T) {
	var stdout syncBuf
	if code := run(t.Context(), []string{"completion", "fish"}, &stdout, new(nopWriter), deps{}); code != 0 {
		t.Fatalf("completion fish exited %d", code)
	}
	if !strings.Contains(stdout.String(), "complete -c tsrelay") {
		t.Fatalf("not a fish completion script:\n%.300s", stdout.String())
	}
}
