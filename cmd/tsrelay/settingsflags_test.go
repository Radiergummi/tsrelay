package main

import (
	"strings"
	"testing"

	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func TestParseRateLimit(t *testing.T) {
	got, err := parseRateLimit("2.5/10")
	if err != nil || got != (hooks.RateLimit{PerSecond: 2.5, Burst: 10}) {
		t.Fatalf("parseRateLimit = %+v, %v", got, err)
	}
	for _, bad := range []string{"", "100", "0/1", "-1/5", "1/0", "x/1", "1/y", "1/2.5", "NaN/1", "Inf/1"} {
		if _, err := parseRateLimit(bad); err == nil {
			t.Errorf("parseRateLimit(%q) should fail", bad)
		}
	}
}

func policyFrom(t *testing.T, args ...string) (hooks.Policy, error) {
	t.Helper()
	fs := pflag.NewFlagSet("t", pflag.ContinueOnError)
	get := registerPolicyFlags(fs)
	if err := fs.Parse(args); err != nil {
		t.Fatal(err)
	}
	return get()
}

func TestPolicyFlagDefaults(t *testing.T) {
	p, err := policyFrom(t)
	if err != nil || p != hooks.DefaultPolicy {
		t.Fatalf("policy = %+v, %v; want hooks.DefaultPolicy", p, err)
	}
}

func TestPolicyFlags(t *testing.T) {
	p, err := policyFrom(t,
		"--hook-hold-timeout-default=0s", "--hook-rate-limit-default=5/10", "--hook-rate-limit-max=50/100",
		"--hook-max-body-bytes-default=4096", "--hook-max-body-bytes-max=1048576",
		"--hook-host-header-default=rewrite", "--hook-attach-policy-default=reject")
	if err != nil {
		t.Fatal(err)
	}
	d := p.Defaults
	if d.HoldTimeout != 0 || d.RateLimit != (hooks.RateLimit{PerSecond: 5, Burst: 10}) || d.MaxBodyBytes != 4096 ||
		d.HostHeader != hooks.HostRewrite || d.AttachPolicy != hooks.AttachReject {
		t.Fatalf("defaults = %+v", d)
	}
	if p.Max.MaxBodyBytes != 1<<20 || p.Max.RateLimit != (hooks.RateLimit{PerSecond: 50, Burst: 100}) ||
		p.Max.HoldTimeout != hooks.DefaultPolicy.Max.HoldTimeout {
		t.Fatalf("maxima = %+v", p.Max)
	}
}

func TestPolicyFlagErrors(t *testing.T) {
	cases := map[string][]string{
		"hook-response-timeout-max": {"--hook-response-timeout-max=1m"}, // below the 2m default
		"hook-idle-timeout-default": {"--hook-idle-timeout-default=-1s"},
		"hook-hold-timeout-max":     {"--hook-hold-timeout-max=-1s"},
		"attachPolicy":              {"--hook-attach-policy-default=steal"},
		"hostHeader":                {"--hook-host-header-default=keep"},
		// An unlimited default body size is not allowed under a maximum.
		"hook-max-body-bytes-default": {"--hook-max-body-bytes-max=1024"},
		"hook-max-body-bytes-max":     {"--hook-max-body-bytes-max=-1"},
	}
	for want, args := range cases {
		if _, err := policyFrom(t, args...); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%v: err = %v, want it to mention %q", args, err, want)
		}
	}
	fs := pflag.NewFlagSet("t", pflag.ContinueOnError)
	fs.SetOutput(new(nopWriter))
	registerPolicyFlags(fs)
	if err := fs.Parse([]string{"--hook-rate-limit-default=fast"}); err == nil {
		t.Error("an unparseable rate limit must fail at parse time")
	}
}

// A default above its maximum is reported by flag name, not hook field.
func TestPolicyFlagDefaultAboveMax(t *testing.T) {
	cases := map[string][]string{
		"--hook-max-body-bytes-default (unlimited) exceeds --hook-max-body-bytes-max 10485760; set the default too": {"--hook-max-body-bytes-max=10485760"},
		"--hook-max-body-bytes-default 4096 exceeds --hook-max-body-bytes-max 1024; lower the default or raise the maximum": {
			"--hook-max-body-bytes-default=4096", "--hook-max-body-bytes-max=1024"},
		"--hook-response-timeout-default 2m0s exceeds --hook-response-timeout-max 1m0s; set the default too":             {"--hook-response-timeout-max=1m"},
		"--hook-hold-timeout-default 10m0s exceeds --hook-hold-timeout-max 5m0s; lower the default or raise the maximum": {"--hook-hold-timeout-default=10m"},
		"--hook-rate-limit-default 100/200 exceeds --hook-rate-limit-max 50/500; set the default too":                    {"--hook-rate-limit-max=50/500"},
	}
	for want, args := range cases {
		if _, err := policyFrom(t, args...); err == nil || err.Error() != want {
			t.Errorf("%v: err = %v, want %q", args, err, want)
		}
	}
}

func TestHookSettingFlags(t *testing.T) {
	fs := pflag.NewFlagSet("t", pflag.ContinueOnError)
	get := registerHookSettingFlags(fs, false)
	if err := fs.Parse([]string{"--hold-timeout=0s", "--rate-limit=5/10", "--host-header=rewrite", "--max-body-bytes=0"}); err != nil {
		t.Fatal(err)
	}
	s, err := get()
	if err != nil {
		t.Fatal(err)
	}
	if s.HoldTimeout == nil || *s.HoldTimeout != 0 || s.MaxBodyBytes == nil || *s.MaxBodyBytes != 0 {
		t.Errorf("explicit zero values must be set: %+v", s)
	}
	if s.RateLimit == nil || *s.RateLimit != (hooks.RateLimit{PerSecond: 5, Burst: 10}) || s.HostHeader == nil || *s.HostHeader != hooks.HostRewrite {
		t.Errorf("settings = %+v", s)
	}
	if s.ResponseTimeout != nil || s.IdleTimeout != nil || s.AttachPolicy != nil {
		t.Errorf("flags that were not given must stay nil: %+v", s)
	}

	fs = pflag.NewFlagSet("t", pflag.ContinueOnError)
	get = registerHookSettingFlags(fs, false)
	fs.Parse([]string{"--idle-timeout=-1s"})
	if _, err := get(); err == nil {
		t.Error("a negative duration must fail")
	}
	fs = pflag.NewFlagSet("t", pflag.ContinueOnError)
	get = registerHookSettingFlags(fs, false)
	fs.Parse(nil)
	if s, err := get(); err != nil || s != (hooks.Settings{}) {
		t.Errorf("no flags = %+v, %v", s, err)
	}
}

func TestCaptureFlags(t *testing.T) {
	fs := pflag.NewFlagSet("t", pflag.ContinueOnError)
	get := registerHookSettingFlags(fs, false)
	fs.Parse([]string{"--capture=false"})
	if s, err := get(); err != nil || s.Capture == nil || *s.Capture {
		t.Fatalf("--capture=false = %+v, %v", s, err)
	}
	p, err := policyFrom(t, "--hook-capture-default=false")
	if err != nil || p.Defaults.Capture {
		t.Fatalf("--hook-capture-default=false = %+v, %v", p.Defaults, err)
	}
}

// Minor 4: help for a hook setting flag must show exactly one default,
// worded for the command it belongs to, not pflag's own DefValue as well.
func TestHookSettingFlagsDefaultWording(t *testing.T) {
	fs := pflag.NewFlagSet("t", pflag.ContinueOnError)
	registerHookSettingFlags(fs, true)
	for _, name := range hookSettingFlagNames {
		f := fs.Lookup(name)
		if f == nil {
			t.Fatalf("no flag %q", name)
		}
		if f.DefValue != "" {
			t.Errorf("%s: DefValue = %q, fang would print it alongside our own default text", name, f.DefValue)
		}
		if !strings.Contains(f.Usage, "the hook's current value") {
			t.Errorf("%s: usage = %q, want it to mention the given default", name, f.Usage)
		}
	}
}

// Minor 3: fang capitalizes a flag description's first word, so a quoted
// value must never be first.
// fang capitalizes the first letter of every flag description, which
// would turn a leading quoted value such as "takeover" into "Takeover".
func TestNoFlagDescriptionStartsWithAQuote(t *testing.T) {
	var walk func(cmd *cobra.Command)
	walk = func(cmd *cobra.Command) {
		cmd.Flags().VisitAll(func(f *pflag.Flag) {
			if strings.HasPrefix(f.Usage, `"`) {
				t.Errorf("%s --%s: usage starts with a quote: %q", cmd.CommandPath(), f.Name, f.Usage)
			}
		})
		for _, sub := range cmd.Commands() {
			walk(sub)
		}
	}
	walk(newRoot(&cli{}))
}
