package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/pflag"
)

// envAnnotation marks a flag's pflag.Flag.Changed as having been set from a
// TSRELAY_* variable rather than the command line, so a command that cares
// about the caller's intent (e.g. hooks update) can tell the two apart.
const envAnnotation = "tsrelay-env"

// envSet reports whether f was changed only by envDefaults, not by an
// argument the caller typed.
func envSet(f *pflag.Flag) bool { return len(f.Annotations[envAnnotation]) > 0 }

// visitTyped calls fn for each flag of fs the caller gave on the command
// line, skipping flags set only from a TSRELAY_* variable.
func visitTyped(fs *pflag.FlagSet, fn func(*pflag.Flag)) {
	fs.Visit(func(f *pflag.Flag) {
		if !envSet(f) {
			fn(f)
		}
	})
}

// envFlags are the flags that have a TSRELAY_<FLAG> twin: the ones that
// configure. A flag is not here when it acts (a variable left in the
// environment must not make tsrelay update without asking, stop every
// tunnel, detach, downgrade, or open a hook to the internet) or when it
// names the one thing a command works on (a hook's path).
// A new flag has no twin until it is listed here; a test makes each flag
// decide.
var envFlags = map[string]bool{
	// Global.
	"relay": true, "json": true, "no-color": true, "no-hyperlinks": true,
	// The local target of attach.
	"to": true,
	// Hook settings, on http and hooks create/update.
	"attach-policy": true, "capture": true, "channel": true, "description": true,
	"ephemeral": true, "expires-in": true, "hold-timeout": true,
	"host-header": true, "idle-timeout": true, "max-body-bytes": true, "rate-limit": true,
	"response-timeout": true,
	// serve: every flag.
	"advertise-tags": true, "cap-name": true, "capture-bodies": true,
	"capture-calls": true, "capture-limit-bytes": true, "ephemeral-grace": true,
	"funnel": true, "funnel-max-body-bytes": true, "funnel-port": true,
	"hook-attach-policy-default": true, "hook-capture-default": true,
	"hook-hold-queue": true, "hook-hold-timeout-default": true,
	"hook-hold-timeout-max": true, "hook-host-header-default": true,
	"hook-idle-timeout-default": true, "hook-idle-timeout-max": true,
	"hook-max-body-bytes-default": true, "hook-max-body-bytes-max": true,
	"hook-rate-limit-default": true, "hook-rate-limit-max": true,
	"hook-response-timeout-default": true, "hook-response-timeout-max": true,
	"hostname": true, "log-format": true, "log-level": true, "ops-listen": true,
	"ops-pprof": true, "otel": true, "otel-propagation": true,
	"redact-headers": true, "revalidate-interval": true, "state-dir": true,
	"tls": true, "tunnel-concurrency": true, "update-check": true,
	"user-max-hook-ttl": true, "user-max-hooks": true, "user-max-tunnels": true,
	"user-slug": true, "user-slug-override": true, "verbose-tailscale": true,
}

// reservedEnv are the variables tsrelay sets for the commands it starts or
// supervises. No flag may be named so that its twin is one of them.
var reservedEnv = []string{"TSRELAY_URL", backgroundIDEnv}

// envName is the variable that sets the flag called name.
func envName(name string) string {
	return "TSRELAY_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
}

// envDefaults sets every flag of fs not given on the command line from
// TSRELAY_<NAME>, with dashes turned into underscores (e.g.
// --ephemeral-grace ← TSRELAY_EPHEMERAL_GRACE), for the envFlags only.
// Variables that match no flag are ignored.
func envDefaults(fs *pflag.FlagSet) error {
	var errs, fixes []string
	fs.VisitAll(func(f *pflag.Flag) {
		if f.Changed || !envFlags[f.Name] {
			return
		}
		name := envName(f.Name)
		v, ok := os.LookupEnv(name)
		if !ok {
			return
		}
		if err := fs.Set(f.Name, v); err != nil {
			errs = append(errs, fmt.Sprintf("%s=%q: %v", name, v, err))
			fixes = append(fixes, fmt.Sprintf("Correct or unset %s (it sets --%s)", name, f.Name))
			return
		}
		fs.SetAnnotation(f.Name, envAnnotation, []string{"1"})
	})
	if len(errs) > 0 {
		return usage("Invalid TSRELAY_* variable in the environment", sentence(strings.Join(errs, "; ")), fixes...)
	}
	return nil
}

// listFlag collects comma-separated values. The first explicit value
// replaces the default in Values; repeating the flag appends.
type listFlag struct {
	Values []string
	set    bool
}

func (l *listFlag) String() string { return strings.Join(l.Values, ",") }
func (l *listFlag) Type() string   { return "strings" }

func (l *listFlag) Set(v string) error {
	if !l.set {
		l.Values, l.set = nil, true
	}
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			l.Values = append(l.Values, s)
		}
	}
	return nil
}

// mapFlag collects key=value pairs; repeating the flag adds pairs.
type mapFlag map[string]string

func (m mapFlag) Type() string { return "key=value" }

func (m mapFlag) String() string {
	parts := make([]string, 0, len(m))
	for k, v := range m {
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, ",")
}

func (m mapFlag) Set(v string) error {
	for _, pair := range strings.Split(v, ",") {
		k, val, ok := strings.Cut(strings.TrimSpace(pair), "=")
		if !ok || k == "" || val == "" {
			return fmt.Errorf("%q is not key=value", pair)
		}
		m[k] = val
	}
	return nil
}
