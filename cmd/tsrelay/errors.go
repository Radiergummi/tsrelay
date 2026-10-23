package main

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// tsrelay's exit statuses; docs/cli.md lists them. With a command after
// "--", http exits with the command's status, so tsrelay's own failures
// there take the 125 to 127 that shells use for the same.
const (
	exitError            = 1   // an error
	exitUsage            = 2   // tsrelay was called wrongly
	exitHookDeleted      = 3   // the last hook was deleted while serving it
	exitUpdateAvailable  = 4   // update --check found a newer version
	exitTsrelayWithChild = 125 // tsrelay failed, with a command given
	exitNotExecutable    = 126 // the command can't be run
	exitNotFound         = 127 // the command doesn't exist
)

// cliError is an error explained for people: what went wrong (Title), why
// (Reason), and what to do instead (Fixes: commands or actions). Code names
// it for --json: the relay's problem code, "usage", or one of the CLI's
// own. A usage error, a mistake in how tsrelay was called, exits with
// status 2; any other with 1.
type cliError struct {
	Title  string
	Reason string
	Fixes  []string
	Code   string
	usage  bool
	err    error // the cause, if any; reachable with errors.Is and errors.As
}

func (e *cliError) Error() string {
	if e.Reason == "" {
		return e.Title
	}
	return e.Title + ": " + e.Reason
}

func (e *cliError) Unwrap() error { return e.err }

// usage returns a usage error.
func usage(title, reason string, fixes ...string) *cliError {
	return &cliError{Title: title, Reason: reason, Fixes: fixes, Code: "usage", usage: true}
}

// exitCodeError ends a command with that exit status. Whatever went wrong
// has been reported already, so it is never printed.
type exitCodeError int

func (e exitCodeError) Error() string { return fmt.Sprintf("exit status %d", int(e)) }

// exitStatus maps what a command returned to tsrelay's exit status.
func exitStatus(err error) int {
	var ce *cliError
	var ec exitCodeError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &ec):
		return int(ec)
	case errors.As(err, &ce) && ce.usage:
		return exitUsage
	default:
		return exitError
	}
}

// capitalize makes an error message from elsewhere start like a headline.
func capitalize(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	r, n := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + s[n:]
}

// sentence capitalizes s and ends it with a period, so an error message
// from elsewhere reads as a reason.
func sentence(s string) string {
	if s = capitalize(s); s != "" && !strings.HasSuffix(s, ".") {
		s += "."
	}
	return s
}

// examples are cmd's examples as fixes, or a pointer to its help.
func examples(cmd *cobra.Command) []string {
	var fixes []string
	for _, l := range strings.Split(cmd.Example, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			fixes = append(fixes, l)
		}
	}
	if len(fixes) == 0 {
		fixes = []string{cmd.CommandPath() + " --help"}
	}
	return fixes
}

// arity accepts min to max positional arguments (max < 0: no limit). what
// names one argument in plain words, e.g. "hook", for the explanation.
func arity(min, max int, what string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		var takes string
		switch {
		case max == 0:
			takes = "no arguments"
		case min == max:
			takes = "one " + what
		case max < 0:
			takes = "one or more " + what + "s"
		default:
			takes = "at most one " + what
		}
		reason := fmt.Sprintf("%s takes %s.", cmd.CommandPath(), takes)
		switch n := len(args); {
		case n < min:
			return usage("No "+what+" given", reason, examples(cmd)...)
		case max >= 0 && n > max:
			return usage(fmt.Sprintf("Unexpected argument %q", args[max]), reason, examples(cmd)...)
		}
		return nil
	}
}

// subcommand is the argument validator of a command that only groups
// others: an argument names an unknown subcommand, so it suggests the
// closest ones.
func subcommand(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}
	e := usage(fmt.Sprintf("Unknown command %q", args[0]), fmt.Sprintf("%s has no command %q.", cmd.CommandPath(), args[0]))
	for _, s := range cmd.SuggestionsFor(args[0]) {
		e.Fixes = append(e.Fixes, cmd.CommandPath()+" "+s)
	}
	e.Fixes = append(e.Fixes, cmd.CommandPath()+" --help")
	return e
}

// flagSample is an example value for a flag of the given type, with the
// separator before it; empty if there is none.
func flagSample(f *pflag.Flag) string {
	switch f.Value.Type() {
	case "bool":
		return "=false"
	case "duration":
		return " 30s"
	case "int", "int64":
		return " 10"
	case "perSecond/burst":
		return " 100/200"
	case "key=value":
		return " alice@example.com=alice"
	case "strings":
		return " tag:a,tag:b"
	}
	return ""
}

// flagFixes suggests cmd with a sample value for f, then its help.
func flagFixes(cmd *cobra.Command, f *pflag.Flag) []string {
	help := cmd.CommandPath() + " --help"
	if s := flagSample(f); s != "" {
		return []string{cmd.CommandPath() + " --" + f.Name + s, help}
	}
	return []string{help}
}

// flagError is cobra's flag error function: it explains a flag that does
// not exist, lacks its value or got a bad one.
func (c *cli) flagError(cmd *cobra.Command, err error) error {
	help := cmd.CommandPath() + " --help"
	var ne *pflag.NotExistError
	var vr *pflag.ValueRequiredError
	var iv *pflag.InvalidValueError
	var is *pflag.InvalidSyntaxError
	switch {
	case errors.As(err, &ne) && ne.GetSpecifiedShortnames() != "":
		// pflag only reports the rest of the group from the unknown letter on,
		// e.g. "son" of -json, so the argument is found among those given.
		for _, a := range c.args {
			name, ok := strings.CutPrefix(a, "-")
			if ok && len(name) > 1 && !strings.HasPrefix(name, "-") && strings.HasSuffix(name, ne.GetSpecifiedShortnames()) && cmd.Flags().Lookup(name) != nil {
				return usage("Long flags take two dashes",
					fmt.Sprintf("tsrelay reads -%s as the short flags -%s.", name, strings.Join(strings.Split(name, ""), ", -")),
					cmd.CommandPath()+" --"+name)
			}
		}
		return usage("Unknown flag -"+ne.GetSpecifiedName(),
			fmt.Sprintf("%s has no short flag -%s.", cmd.CommandPath(), ne.GetSpecifiedName()), help)
	case errors.As(err, &ne):
		return usage("Unknown flag --"+ne.GetSpecifiedName(),
			fmt.Sprintf("%s has no flag --%s.", cmd.CommandPath(), ne.GetSpecifiedName()), help)
	case errors.As(err, &vr):
		f := vr.GetFlag()
		return usage("--"+f.Name+" needs a value", sentence(f.Usage), flagFixes(cmd, f)...)
	case errors.As(err, &iv):
		f := iv.GetFlag()
		var reason string
		switch f.Value.Type() {
		case "bool":
			reason = "--" + f.Name + " takes true or false."
		case "duration":
			reason = "--" + f.Name + " takes a duration such as 30s, 5m or 1h."
		case "int", "int64":
			reason = "--" + f.Name + " takes a whole number."
		default:
			reason = sentence(errors.Unwrap(iv).Error())
		}
		return usage(fmt.Sprintf("Invalid value %q for --%s", iv.GetValue(), f.Name), reason, flagFixes(cmd, f)...)
	case errors.As(err, &is):
		return usage("Bad flag syntax", fmt.Sprintf("%q is neither a short flag (-x) nor a long one (--name).", is.GetSpecifiedFlag()), help)
	}
	return usage(capitalize(err.Error()), "", help)
}
