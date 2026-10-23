package main

import (
	"errors"
	"flag"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func TestEnvDefaults(t *testing.T) {
	fs := pflag.NewFlagSet("x", pflag.ContinueOnError)
	grace := fs.Duration("ephemeral-grace", time.Minute, "")
	name := fs.String("relay", "", "")
	fs.Bool("ephemeral", false, "")
	if err := fs.Parse([]string{"--relay", "flag"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TSRELAY_EPHEMERAL_GRACE", "5m")
	t.Setenv("TSRELAY_RELAY", "env")      // the command line wins
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

func TestEnvDefaultsSkipFlagsWithoutTwin(t *testing.T) {
	fs := pflag.NewFlagSet("x", pflag.ContinueOnError)
	yes := fs.Bool("yes", false, "")
	all := fs.Bool("all", false, "")
	t.Setenv("TSRELAY_YES", "true")
	t.Setenv("TSRELAY_ALL", "true")
	if err := envDefaults(fs); err != nil {
		t.Fatal(err)
	}
	if *yes || *all {
		t.Fatal("a flag without a twin was set from the environment")
	}
}

func TestEnvDefaultsInvalid(t *testing.T) {
	fs := pflag.NewFlagSet("x", pflag.ContinueOnError)
	fs.Bool("ephemeral", false, "")
	t.Setenv("TSRELAY_EPHEMERAL", "maybe")
	err := envDefaults(fs)
	var ce *cliError
	if !errors.As(err, &ce) || !ce.usage || !strings.Contains(err.Error(), "TSRELAY_EPHEMERAL") {
		t.Fatalf("err = %v, want a usage error naming TSRELAY_EPHEMERAL", err)
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

// everyFlag calls fn for each flag of every command, persistent ones once.
func everyFlag(fn func(cmd *cobra.Command, f *pflag.Flag)) {
	var walk func(*cobra.Command)
	walk = func(cmd *cobra.Command) {
		cmd.NonInheritedFlags().VisitAll(func(f *pflag.Flag) { fn(cmd, f) })
		for _, sub := range cmd.Commands() {
			walk(sub)
		}
	}
	walk(newRoot(&cli{}))
}

func TestEveryFlagDecidesItsEnvTwin(t *testing.T) {
	// Flags without a twin: they act, or name the one thing a command
	// works on.
	noEnv := map[string]bool{
		"all": true, "yes": true, "check": true, "follow": true, "background": true,
		"force": true, "public": true, "insecure": true, "help": true,
		"path": true,
	}
	seen := map[string]bool{}
	everyFlag(func(cmd *cobra.Command, f *pflag.Flag) {
		seen[f.Name] = true
		if envFlags[f.Name] == noEnv[f.Name] {
			t.Errorf("--%s (%s): list it in exactly one of envFlags and this test's noEnv", f.Name, cmd.CommandPath())
		}
	})
	for name := range envFlags {
		if !seen[name] {
			t.Errorf("envFlags lists --%s, which no command has", name)
		}
	}
	for name := range noEnv {
		if !seen[name] && name != "help" {
			t.Errorf("noEnv lists --%s, which no command has", name)
		}
	}
}

func TestNoTwinForPublicAndInsecure(t *testing.T) {
	if envFlags["public"] || envFlags["insecure"] {
		t.Fatal("public and insecure act rather than configure: no twin")
	}
	t.Setenv("TSRELAY_PUBLIC", "true")
	t.Setenv("TSRELAY_INSECURE", "true")
	fs := pflag.NewFlagSet("x", pflag.ContinueOnError)
	public := fs.Bool("public", false, "")
	insecure := fs.Bool("insecure", false, "")
	if err := envDefaults(fs); err != nil {
		t.Fatal(err)
	}
	if *public || *insecure {
		t.Fatalf("public = %v, insecure = %v: a flag that opens something was set from the environment", *public, *insecure)
	}
}

func TestNoFlagClaimsReservedEnv(t *testing.T) {
	everyFlag(func(cmd *cobra.Command, f *pflag.Flag) {
		if slices.Contains(reservedEnv, envName(f.Name)) {
			t.Errorf("--%s (%s) would have the twin %s, which tsrelay sets itself", f.Name, cmd.CommandPath(), envName(f.Name))
		}
	})
}
