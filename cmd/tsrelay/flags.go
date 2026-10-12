package main

import (
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"
)

// applyEnv sets every flag not given on the command line from the
// environment variable prefix + NAME, with dashes turned into underscores
// (e.g. --ephemeral-grace ← TSRELAY_EPHEMERAL_GRACE).
func applyEnv(fs *flag.FlagSet, prefix string) error {
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	var errs []string
	fs.VisitAll(func(f *flag.Flag) {
		if set[f.Name] {
			return
		}
		name := prefix + strings.ToUpper(strings.ReplaceAll(f.Name, "-", "_"))
		v, ok := os.LookupEnv(name)
		if !ok {
			return
		}
		if err := fs.Set(f.Name, v); err != nil {
			errs = append(errs, fmt.Sprintf("%s=%q: %v", name, v, err))
		}
	})
	if len(errs) > 0 {
		return fmt.Errorf("invalid environment: %s", strings.Join(errs, "; "))
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

// parseArgs parses flags given anywhere among the positional arguments, as
// people type both "tsrelay http 8080 --name x" and "--name x 8080". It
// returns the positional arguments.
func parseArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	var rest []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return rest, nil
		}
		rest = append(rest, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

// splitCommand separates tsrelay's own arguments from a command given
// after "--".
func splitCommand(args []string) (own, command []string) {
	if i := slices.Index(args, "--"); i >= 0 {
		return args[:i], args[i+1:]
	}
	return args, nil
}
