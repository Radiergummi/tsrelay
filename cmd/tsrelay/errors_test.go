package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/spf13/pflag"
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

func TestClientUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{"http"},                      // no target
		{"http", "ftp://example.com"}, // not http(s)
		{"http", "8080", "extra"},     // two targets
		{"http", "8080", "--persistent=maybe"},
		{"attach", "--to", "8080"}, // no hook
		{"attach", "pay"},          // no --to
		{"attach", "pay", "--to", "ftp://x"},
		{"hooks"}, // no subcommand
		{"hooks", "bogus"},
		{"hooks", "update"},       // no hook
		{"hooks", "update", "cb"}, // nothing to change
		{"hooks", "delete"},
		{"hooks", "list", "extra"},
		{"hooks", "create", "a", "b"},
		{"hooks", "create", "--hold-timeout", "-1s"},
		{"url", "a", "b"},
		{"http", "8080", "-n"}, // -n needs a value
	} {
		var stderr syncBuf
		if code := run(t.Context(), args, new(nopWriter), &stderr, deps{}); code != 2 {
			t.Errorf("run(%q) = %d, want 2; stderr:\n%s", args, code, stderr.String())
		}
	}
}

func TestShorthands(t *testing.T) {
	root := newRoot(&cli{stdout: new(nopWriter), stderr: new(nopWriter)})
	for _, tc := range []struct{ cmd, flag, short string }{
		{"http", "name", "n"}, {"http", "channel", "c"}, {"http", "persistent", "p"}, {"http", "insecure", "k"},
		{"attach", "to", "t"}, {"attach", "insecure", "k"},
		{"tsrelay", "json", "j"},
	} {
		cmd := root
		if tc.cmd != "tsrelay" {
			cmd, _, _ = root.Find([]string{tc.cmd})
		}
		f := cmd.Flags().Lookup(tc.flag)
		if f == nil {
			f = cmd.PersistentFlags().Lookup(tc.flag)
		}
		if f == nil || f.Shorthand != tc.short {
			t.Errorf("%s --%s: shorthand = %v, want -%s", tc.cmd, tc.flag, f, tc.short)
		}
	}
	serve, _, _ := root.Find([]string{"serve"})
	serve.LocalFlags().VisitAll(func(f *pflag.Flag) {
		if f.Shorthand != "" {
			t.Errorf("serve --%s has shorthand -%s; serve has none", f.Name, f.Shorthand)
		}
	})
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
