package main

import (
	"errors"
	"flag"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spf13/pflag"
)

func TestEnvDefaults(t *testing.T) {
	fs := pflag.NewFlagSet("x", pflag.ContinueOnError)
	grace := fs.Duration("ephemeral-grace", time.Minute, "")
	name := fs.String("name", "", "")
	fs.Bool("persistent", false, "")
	if err := fs.Parse([]string{"--name", "flag"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TSRELAY_EPHEMERAL_GRACE", "5m")
	t.Setenv("TSRELAY_NAME", "env")       // the command line wins
	t.Setenv("TSRELAY_NO_SUCH_FLAG", "x") // ignored
	if err := envDefaults(fs); err != nil {
		t.Fatal(err)
	}
	if *grace != 5*time.Minute || *name != "flag" {
		t.Fatalf("grace = %v, name = %q", *grace, *name)
	}
	if !fs.Changed("ephemeral-grace") {
		t.Fatal("a flag set from the environment must count as Changed")
	}
}

func TestEnvDefaultsInvalid(t *testing.T) {
	fs := pflag.NewFlagSet("x", pflag.ContinueOnError)
	fs.Bool("persistent", false, "")
	t.Setenv("TSRELAY_PERSISTENT", "maybe")
	err := envDefaults(fs)
	var ce *cliError
	if !errors.As(err, &ce) || !ce.usage || !strings.Contains(err.Error(), "TSRELAY_PERSISTENT") {
		t.Fatalf("err = %v, want a usage error naming TSRELAY_PERSISTENT", err)
	}
}

func TestEnvAppliesToServe(t *testing.T) {
	t.Setenv("TSRELAY_USER_SLUG", "bogus")
	var stderr syncBuf
	if code := run(t.Context(), []string{"serve"}, new(nopWriter), &stderr, deps{}); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "--user-slug") {
		t.Fatalf("stderr = %q", stderr.String())
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

type nopWriter struct{}

func (*nopWriter) Write(p []byte) (int, error) { return len(p), nil }

// A list flag's default is replaced, not extended, by the first explicit value.
func TestListFlagReplacesDefault(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	tags := listFlag{Values: []string{"tag:tsrelay"}}
	fs.Var(&tags, "advertise-tags", "")
	if err := fs.Parse([]string{"--advertise-tags", "tag:relay-prod", "--advertise-tags", "tag:extra"}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(tags.Values, []string{"tag:relay-prod", "tag:extra"}) {
		t.Fatalf("tags = %v, want the default replaced", tags.Values)
	}
	untouched := listFlag{Values: []string{"tag:tsrelay"}}
	if untouched.String() != "tag:tsrelay" {
		t.Fatalf("default = %q", untouched.String())
	}
}
