package main

import (
	"flag"
	"slices"
	"testing"
	"time"
)

func TestApplyEnv(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	host := fs.String("hostname", "tsrelay", "")
	grace := fs.Duration("ephemeral-grace", time.Minute, "")
	tlsOn := fs.Bool("tls", true, "")
	var tags listFlag
	fs.Var(&tags, "advertise-tags", "")

	t.Setenv("TSRELAY_HOSTNAME", "from-env")
	t.Setenv("TSRELAY_EPHEMERAL_GRACE", "5m")
	t.Setenv("TSRELAY_TLS", "false")
	t.Setenv("TSRELAY_ADVERTISE_TAGS", "tag:a,tag:b")
	if err := fs.Parse([]string{"--hostname=from-flag"}); err != nil {
		t.Fatal(err)
	}
	if err := applyEnv(fs, "TSRELAY_"); err != nil {
		t.Fatal(err)
	}
	if *host != "from-flag" {
		t.Errorf("hostname = %q, flags must win over env", *host)
	}
	if *grace != 5*time.Minute || *tlsOn || !slices.Equal(tags, []string{"tag:a", "tag:b"}) {
		t.Errorf("grace=%v tls=%v tags=%v", *grace, *tlsOn, tags)
	}
}

func TestApplyEnvInvalid(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.Duration("ephemeral-grace", time.Minute, "")
	t.Setenv("TSRELAY_EPHEMERAL_GRACE", "soon")
	fs.Parse(nil)
	if err := applyEnv(fs, "TSRELAY_"); err == nil {
		t.Fatal("invalid env value must be an error")
	}
}

func TestMapFlag(t *testing.T) {
	m := mapFlag{}
	if err := m.Set("alice@b.com=alice-b"); err != nil || m["alice@b.com"] != "alice-b" {
		t.Fatalf("Set: %v %v", err, m)
	}
	if err := m.Set("no-equals"); err == nil {
		t.Fatal("missing '=' must fail")
	}
}

func TestUsage(t *testing.T) {
	if code := run(t.Context(), nil, new(nopWriter), new(nopWriter), deps{}); code != 2 {
		t.Fatalf("no subcommand: exit %d, want 2", code)
	}
	if code := run(t.Context(), []string{"bogus"}, new(nopWriter), new(nopWriter), deps{}); code != 2 {
		t.Fatalf("unknown subcommand: exit %d, want 2", code)
	}
	if code := run(t.Context(), []string{"serve", "--no-such-flag"}, new(nopWriter), new(nopWriter), deps{}); code != 2 {
		t.Fatalf("unknown flag: exit %d, want 2", code)
	}
}

type nopWriter struct{}

func (*nopWriter) Write(p []byte) (int, error) { return len(p), nil }
