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
		{usage("Bad flag", ""), 2},
		{fmt.Errorf("wrapped: %w", usage("x", "")), 2},
		{&cliError{Title: "No hook"}, 1},
		{exitCodeError(3), 3},
		{fmt.Errorf("wrapped: %w", exitCodeError(0)), 0},
		{errors.New("boom"), 1},
	} {
		if got := exitStatus(tc.err); got != tc.want {
			t.Errorf("exitStatus(%v) = %d, want %d", tc.err, got, tc.want)
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

func TestShorthands(t *testing.T) {
	root := newRoot(&cli{stdout: new(nopWriter), stderr: new(nopWriter)})
	for _, tc := range []struct{ cmd, flag, short string }{
		{"http", "channel", "c"}, {"http", "insecure", "k"},
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
