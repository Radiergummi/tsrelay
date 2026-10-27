package main

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/spf13/pflag"
)

// parseRateLimit parses "perSecond/burst", e.g. "100/200" or "0.5/1".
func parseRateLimit(s string) (hooks.RateLimit, error) {
	ps, b, ok := strings.Cut(s, "/")
	if !ok {
		return hooks.RateLimit{}, fmt.Errorf(`rate limit %q must be "perSecond/burst", e.g. "100/200"`, s)
	}
	perSecond, err := strconv.ParseFloat(ps, 64)
	if err != nil || !(perSecond > 0) || math.IsInf(perSecond, 0) {
		return hooks.RateLimit{}, fmt.Errorf("rate limit %q: perSecond must be a number greater than zero", s)
	}
	burst, err := strconv.Atoi(b)
	if err != nil || burst < 1 {
		return hooks.RateLimit{}, fmt.Errorf("rate limit %q: burst must be a whole number of at least 1", s)
	}
	return hooks.RateLimit{PerSecond: perSecond, Burst: burst}, nil
}

// rateLimitFlag is a flag.Value for "perSecond/burst". Its zero value
// prints as empty, which flag's help output treats as "no default".
type rateLimitFlag struct{ v hooks.RateLimit }

func (f *rateLimitFlag) String() string {
	if f.v == (hooks.RateLimit{}) {
		return ""
	}
	return strconv.FormatFloat(f.v.PerSecond, 'g', -1, 64) + "/" + strconv.Itoa(f.v.Burst)
}

func (f *rateLimitFlag) Set(s string) error {
	v, err := parseRateLimit(s)
	if err != nil {
		return err
	}
	f.v = v
	return nil
}

func (f *rateLimitFlag) Type() string { return "perSecond/burst" }

// enumFlag is a flag.Value that takes only one of allowed, so a bad value
// is explained like any other invalid flag value.
type enumFlag struct {
	value   string
	allowed []string
}

// enumVar defines an enum flag with the given default and allowed values.
func enumVar(fs *pflag.FlagSet, name, value, usage string, allowed ...string) *enumFlag {
	f := &enumFlag{value: value, allowed: allowed}
	fs.Var(f, name, usage)
	return f
}

func (f *enumFlag) String() string { return f.value }

func (f *enumFlag) Set(s string) error {
	if !slices.Contains(f.allowed, s) {
		return fmt.Errorf("must be %s", f.choices())
	}
	f.value = s
	return nil
}

// choices lists the allowed values: "a" or "b", or "a", "b" or "c".
func (f *enumFlag) choices() string {
	quoted := make([]string, len(f.allowed))
	for i, a := range f.allowed {
		quoted[i] = strconv.Quote(a)
	}
	last := len(quoted) - 1
	if last == 0 {
		return quoted[0]
	}
	return strings.Join(quoted[:last], ", ") + " or " + quoted[last]
}

func (f *enumFlag) Type() string { return "enum" }

type durationFlag struct {
	name string
	v    *time.Duration
}

// checkDurations rejects negative durations, which flag.Duration accepts.
func checkDurations(ds []durationFlag) error {
	for _, d := range ds {
		if *d.v < 0 {
			return usage("--"+d.name+" can't be negative", "Durations are 0 or more.", "Pass a duration such as --"+d.name+" 30s")
		}
	}
	return nil
}

// registerPolicyFlags registers the relay-wide --hook-<setting>-default and
// --hook-<setting>-max flags (DESIGN.md §12) on fs. The returned function
// builds the policy after parsing and checks that the defaults lie within
// the maxima.
func registerPolicyFlags(fs *pflag.FlagSet) func() (hooks.Policy, error) {
	d, m := hooks.DefaultPolicy.Defaults, hooks.DefaultPolicy.Max
	holdDef := fs.Duration("hook-hold-timeout-default", d.HoldTimeout.Std(), "how long a call waits for a tunnel to attach (0 disables holding)")
	holdMax := fs.Duration("hook-hold-timeout-max", m.HoldTimeout.Std(), "largest hold timeout a hook may set (0: no maximum)")
	respDef := fs.Duration("hook-response-timeout-default", d.ResponseTimeout.Std(), "how long to wait for the local app's response headers")
	respMax := fs.Duration("hook-response-timeout-max", m.ResponseTimeout.Std(), "largest response timeout a hook may set (0: no maximum)")
	idleDef := fs.Duration("hook-idle-timeout-default", d.IdleTimeout.Std(), "longest gap in body traffic, in either direction")
	idleMax := fs.Duration("hook-idle-timeout-max", m.IdleTimeout.Std(), "largest idle timeout a hook may set (0: no maximum)")
	bodyDef := fs.Int64("hook-max-body-bytes-default", d.MaxBodyBytes, "largest request body in bytes (0: unlimited)")
	bodyMax := fs.Int64("hook-max-body-bytes-max", m.MaxBodyBytes, "largest body limit a hook may set (0: no maximum)")
	rateDef := &rateLimitFlag{v: d.RateLimit}
	fs.Var(rateDef, "hook-rate-limit-default", `calls per hook as "perSecond/burst"`)
	rateMax := &rateLimitFlag{v: m.RateLimit}
	fs.Var(rateMax, "hook-rate-limit-max", `largest rate limit a hook may set, as "perSecond/burst" (default: no maximum)`)
	attach := enumVar(fs, "hook-attach-policy-default", string(d.AttachPolicy), `what a second tunnel for a hook does: "takeover" or "reject"`,
		string(hooks.AttachTakeover), string(hooks.AttachReject))
	host := enumVar(fs, "hook-host-header-default", string(d.HostHeader), `the Host header the local app sees: "preserve" or "rewrite"`,
		string(hooks.HostPreserve), string(hooks.HostRewrite))
	capture := fs.Bool("hook-capture-default", d.Capture, "keep request and response bodies for the inspector for hooks that don't set capture; off by default (metadata is always recorded)")

	return func() (hooks.Policy, error) {
		err := checkDurations([]durationFlag{
			{"hook-hold-timeout-default", holdDef}, {"hook-hold-timeout-max", holdMax},
			{"hook-response-timeout-default", respDef}, {"hook-response-timeout-max", respMax},
			{"hook-idle-timeout-default", idleDef}, {"hook-idle-timeout-max", idleMax},
		})
		if err != nil {
			return hooks.Policy{}, err
		}
		if *bodyMax < 0 {
			return hooks.Policy{}, usage("--hook-max-body-bytes-max can't be negative", "It is the largest body limit a hook may set; 0 means no maximum.",
				"tsrelay serve --hook-max-body-bytes-max 0")
		}
		p := hooks.Policy{
			Defaults: hooks.Effective{
				AttachPolicy:    hooks.AttachPolicy(attach.value),
				HostHeader:      hooks.HostHeader(host.value),
				HoldTimeout:     hooks.Duration(*holdDef),
				ResponseTimeout: hooks.Duration(*respDef),
				IdleTimeout:     hooks.Duration(*idleDef),
				MaxBodyBytes:    *bodyDef,
				RateLimit:       rateDef.v,
				Capture:         *capture,
			},
			Max: hooks.Maxima{
				HoldTimeout:     hooks.Duration(*holdMax),
				ResponseTimeout: hooks.Duration(*respMax),
				IdleTimeout:     hooks.Duration(*idleMax),
				MaxBodyBytes:    *bodyMax,
				RateLimit:       rateMax.v,
			},
		}
		if err := checkDefaultsWithinMaxima(fs, p); err != nil {
			return hooks.Policy{}, err
		}
		if err := p.Check(); err != nil {
			e := usage("Invalid hook defaults", sentence(err.Error()), "tsrelay serve --help")
			e.err = err
			return hooks.Policy{}, e
		}
		return p, nil
	}
}

// checkDefaultsWithinMaxima reports a default above its maximum by flag
// name; p.Check would only name the hook setting.
func checkDefaultsWithinMaxima(fs *pflag.FlagSet, p hooks.Policy) error {
	d, m := p.Defaults, p.Max
	overDur := func(def, max hooks.Duration) bool { return max > 0 && def > max }
	body := strconv.FormatInt(d.MaxBodyBytes, 10)
	if d.MaxBodyBytes == 0 {
		body = "(unlimited)"
	}
	for _, c := range []struct {
		setting  string
		over     bool
		def, max string
	}{
		{"hold-timeout", overDur(d.HoldTimeout, m.HoldTimeout), d.HoldTimeout.Std().String(), m.HoldTimeout.Std().String()},
		{"response-timeout", overDur(d.ResponseTimeout, m.ResponseTimeout), d.ResponseTimeout.Std().String(), m.ResponseTimeout.Std().String()},
		{"idle-timeout", overDur(d.IdleTimeout, m.IdleTimeout), d.IdleTimeout.Std().String(), m.IdleTimeout.Std().String()},
		{"max-body-bytes", m.MaxBodyBytes > 0 && (d.MaxBodyBytes == 0 || d.MaxBodyBytes > m.MaxBodyBytes), body, strconv.FormatInt(m.MaxBodyBytes, 10)},
		{"rate-limit", m.RateLimit.PerSecond > 0 && d.RateLimit.PerSecond > m.RateLimit.PerSecond ||
			m.RateLimit.Burst > 0 && d.RateLimit.Burst > m.RateLimit.Burst,
			(&rateLimitFlag{d.RateLimit}).String(), (&rateLimitFlag{m.RateLimit}).String()},
	} {
		if !c.over {
			continue
		}
		e := usage(fmt.Sprintf("--hook-%s-default %s exceeds --hook-%s-max %s", c.setting, c.def, c.setting, c.max),
			"Hooks that set nothing get the default, so it must lie within the maximum.",
			fmt.Sprintf("tsrelay serve --hook-%s-default %s", c.setting, c.max))
		if fs.Changed("hook-" + c.setting + "-default") {
			def := c.def
			if def == "(unlimited)" {
				def = "0"
			}
			e.Fixes = append(e.Fixes, fmt.Sprintf("tsrelay serve --hook-%s-max %s", c.setting, def))
		}
		return e
	}
	return nil
}

// hookSettingFlagNames are the flags registerHookSettingFlags registers.
var hookSettingFlagNames = []string{
	"hold-timeout", "response-timeout", "idle-timeout", "max-body-bytes",
	"rate-limit", "attach-policy", "host-header", "capture",
}

// anyHookSettingGivenOnCLI reports whether the caller typed at least one
// hook-setting flag on the command line, as opposed to only a TSRELAY_*
// variable setting it.
func anyHookSettingGivenOnCLI(fs *pflag.FlagSet) bool {
	given := false
	visitTyped(fs, func(f *pflag.Flag) { given = given || slices.Contains(hookSettingFlagNames, f.Name) })
	return given
}

// registerHookSettingFlags registers the per-hook setting flags used by
// commands that create or update hooks. The help text says what an omitted
// flag keeps: the relay's default for a new hook, or the hook's current
// value on an update. The flags' own DefValue is cleared so fang's help
// does not print a second default.
//
// For an update, the returned Settings hold only flags given on the command
// line, so a stray TSRELAY_* variable never patches a hook; for http and
// hooks create, TSRELAY_* still act as defaults.
func registerHookSettingFlags(fs *pflag.FlagSet, forUpdate bool) func() (hooks.Settings, error) {
	unsetDefault := "the relay's"
	if forUpdate {
		unsetDefault = "the hook's current value"
	}
	hold := fs.Duration("hold-timeout", 0, fmt.Sprintf("how long calls wait for a tunnel to attach; 0 disables holding (default: %s)", unsetDefault))
	resp := fs.Duration("response-timeout", 0, fmt.Sprintf("how long to wait for the local app's response headers (default: %s)", unsetDefault))
	idle := fs.Duration("idle-timeout", 0, fmt.Sprintf("longest gap in body traffic, in either direction (default: %s)", unsetDefault))
	body := fs.Int64("max-body-bytes", 0, fmt.Sprintf("largest request body in bytes; 0 means unlimited (default: %s)", unsetDefault))
	var rate rateLimitFlag
	fs.Var(&rate, "rate-limit", fmt.Sprintf(`calls per second and burst, as "perSecond/burst" (default: %s)`, unsetDefault))
	attach := enumVar(fs, "attach-policy", "", fmt.Sprintf(`what a second tunnel does: "takeover" or "reject" (default: %s)`, unsetDefault),
		string(hooks.AttachTakeover), string(hooks.AttachReject))
	host := enumVar(fs, "host-header", "", fmt.Sprintf(`the Host header the local app sees: "preserve" or "rewrite" (default: %s)`, unsetDefault),
		string(hooks.HostPreserve), string(hooks.HostRewrite))
	capture := fs.Bool("capture", true, fmt.Sprintf("keep request and response bodies for the inspector, which relays don't by default; --capture=false records metadata only (default: %s)", unsetDefault))
	for _, name := range hookSettingFlagNames {
		fs.Lookup(name).DefValue = "" // help already states the one true default above
	}

	return func() (hooks.Settings, error) {
		if err := checkDurations([]durationFlag{{"hold-timeout", hold}, {"response-timeout", resp}, {"idle-timeout", idle}}); err != nil {
			return hooks.Settings{}, err
		}
		var s hooks.Settings
		visit := fs.Visit
		if forUpdate {
			visit = func(fn func(*pflag.Flag)) { visitTyped(fs, fn) }
		}
		visit(func(f *pflag.Flag) {
			switch f.Name {
			case "hold-timeout":
				s.HoldTimeout = new(hooks.Duration(*hold))
			case "response-timeout":
				s.ResponseTimeout = new(hooks.Duration(*resp))
			case "idle-timeout":
				s.IdleTimeout = new(hooks.Duration(*idle))
			case "max-body-bytes":
				s.MaxBodyBytes = new(*body)
			case "rate-limit":
				s.RateLimit = new(rate.v)
			case "attach-policy":
				s.AttachPolicy = new(hooks.AttachPolicy(attach.value))
			case "host-header":
				s.HostHeader = new(hooks.HostHeader(host.value))
			case "capture":
				s.Capture = new(*capture)
			}
		})
		return s, nil
	}
}
