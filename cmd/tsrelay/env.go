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

// actionFlags are the flags that act rather than configure. A variable
// left in the environment must not make tsrelay update without asking,
// stop every tunnel, detach, or downgrade, so they have no TSRELAY_* twin.
var actionFlags = map[string]bool{"all": true, "yes": true, "check": true, "follow": true, "background": true, "force": true}

// envDefaults sets every flag of fs not given on the command line from
// TSRELAY_<NAME>, with dashes turned into underscores (e.g.
// --ephemeral-grace ← TSRELAY_EPHEMERAL_GRACE), except actionFlags.
// Variables that match no flag are ignored.
func envDefaults(fs *pflag.FlagSet) error {
	var errs, fixes []string
	fs.VisitAll(func(f *pflag.Flag) {
		if f.Changed || actionFlags[f.Name] {
			return
		}
		name := "TSRELAY_" + strings.ToUpper(strings.ReplaceAll(f.Name, "-", "_"))
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
