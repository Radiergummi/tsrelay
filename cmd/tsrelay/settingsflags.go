package main

import (
	"flag"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/Radiergummi/tsrelay/internal/hooks"
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

// wasSet reports whether the flag name was given on the command line or
// applied from the environment by applyEnv.
func wasSet(fs *flag.FlagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) { set = set || f.Name == name })
	return set
}

type durationFlag struct {
	name string
	v    *time.Duration
}

// checkDurations rejects negative durations, which flag.Duration accepts.
func checkDurations(ds []durationFlag) error {
	for _, d := range ds {
		if *d.v < 0 {
			return fmt.Errorf("--%s must not be negative", d.name)
		}
	}
	return nil
}

// registerPolicyFlags registers the relay-wide --hook-<setting>-default and
// --hook-<setting>-max flags (DESIGN.md §12) on fs. The returned function
// builds the policy after parsing and checks that the defaults lie within
// the maxima.
func registerPolicyFlags(fs *flag.FlagSet) func() (hooks.Policy, error) {
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
	attach := fs.String("hook-attach-policy-default", string(d.AttachPolicy), `what a second tunnel for a hook does: "takeover" or "reject"`)
	host := fs.String("hook-host-header-default", string(d.HostHeader), `the Host header the local app sees: "preserve" or "rewrite"`)
	capture := fs.Bool("hook-capture-default", d.Capture, "keep request and response bodies for the inspector (metadata is always recorded)")

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
			return hooks.Policy{}, fmt.Errorf("--hook-max-body-bytes-max must not be negative")
		}
		p := hooks.Policy{
			Defaults: hooks.Effective{
				AttachPolicy:    hooks.AttachPolicy(*attach),
				HostHeader:      hooks.HostHeader(*host),
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
			return hooks.Policy{}, err
		}
		return p, nil
	}
}

// checkDefaultsWithinMaxima reports a default above its maximum by flag
// name; p.Check would only name the hook setting.
func checkDefaultsWithinMaxima(fs *flag.FlagSet, p hooks.Policy) error {
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
		hint := "set the default too"
		if wasSet(fs, "hook-"+c.setting+"-default") {
			hint = "lower the default or raise the maximum"
		}
		return fmt.Errorf("--hook-%s-default %s exceeds --hook-%s-max %s; %s", c.setting, c.def, c.setting, c.max, hint)
	}
	return nil
}

// registerHookSettingFlags registers the per-hook setting flags used by
// commands that create or update hooks. The returned function yields
// Settings in which only the flags given on the command line are set, so
// everything else keeps the relay default (or, on update, its current value).
func registerHookSettingFlags(fs *flag.FlagSet) func() (hooks.Settings, error) {
	hold := fs.Duration("hold-timeout", 0, "how long calls wait for a tunnel to attach; 0 disables holding (default: the relay's)")
	resp := fs.Duration("response-timeout", 0, "how long to wait for the local app's response headers (default: the relay's)")
	idle := fs.Duration("idle-timeout", 0, "longest gap in body traffic, in either direction (default: the relay's)")
	body := fs.Int64("max-body-bytes", 0, "largest request body in bytes; 0 means unlimited (default: the relay's)")
	var rate rateLimitFlag
	fs.Var(&rate, "rate-limit", `calls per second and burst, as "perSecond/burst" (default: the relay's)`)
	attach := fs.String("attach-policy", "", `"takeover" or "reject" (default: the relay's)`)
	host := fs.String("host-header", "", `"preserve" or "rewrite" (default: the relay's)`)
	capture := fs.Bool("capture", true, "keep bodies for the inspector; --capture=false records metadata only (default: the relay's)")

	return func() (hooks.Settings, error) {
		if err := checkDurations([]durationFlag{{"hold-timeout", hold}, {"response-timeout", resp}, {"idle-timeout", idle}}); err != nil {
			return hooks.Settings{}, err
		}
		var s hooks.Settings
		fs.Visit(func(f *flag.Flag) {
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
				s.AttachPolicy = new(hooks.AttachPolicy(*attach))
			case "host-header":
				s.HostHeader = new(hooks.HostHeader(*host))
			case "capture":
				s.Capture = new(*capture)
			}
		})
		return s, nil
	}
}
