package hooks

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Duration is a time.Duration that reads and writes JSON as "30s".
type Duration time.Duration

func (d Duration) Std() time.Duration { return time.Duration(d) }

func (d Duration) MarshalJSON() ([]byte, error) { return json.Marshal(time.Duration(d).String()) }

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf(`duration must be a string like "30s": %w`, err)
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	if v < 0 {
		return fmt.Errorf("duration %q must not be negative", s)
	}
	*d = Duration(v)
	return nil
}

type AttachPolicy string

const (
	AttachTakeover AttachPolicy = "takeover" // a newer tunnel replaces the current one
	AttachReject   AttachPolicy = "reject"   // a newer tunnel is refused while one is attached
)

type HostHeader string

const (
	HostPreserve HostHeader = "preserve" // the local app sees the relay's host
	HostRewrite  HostHeader = "rewrite"  // the local app sees its own host
)

type RateLimit struct {
	PerSecond float64 `json:"perSecond"`
	Burst     int     `json:"burst"`
}

// Settings are a hook's overrides of the relay defaults (DESIGN.md §8).
// A nil field means "use the relay default".
type Settings struct {
	AttachPolicy    *AttachPolicy `json:"attachPolicy,omitzero"`
	HostHeader      *HostHeader   `json:"hostHeader,omitzero"`
	HoldTimeout     *Duration     `json:"holdTimeout,omitzero"`
	ResponseTimeout *Duration     `json:"responseTimeout,omitzero"`
	IdleTimeout     *Duration     `json:"idleTimeout,omitzero"`
	MaxBodyBytes    *int64        `json:"maxBodyBytes,omitzero"` // 0 means unlimited
	RateLimit       *RateLimit    `json:"rateLimit,omitzero"`
	Capture         *bool         `json:"capture,omitzero"` // keep bodies for the inspector
}

// Effective are a hook's settings with every default filled in.
type Effective struct {
	AttachPolicy    AttachPolicy `json:"attachPolicy"`
	HostHeader      HostHeader   `json:"hostHeader"`
	HoldTimeout     Duration     `json:"holdTimeout"`
	ResponseTimeout Duration     `json:"responseTimeout"`
	IdleTimeout     Duration     `json:"idleTimeout"`
	MaxBodyBytes    int64        `json:"maxBodyBytes"`
	RateLimit       RateLimit    `json:"rateLimit"`
	Capture         bool         `json:"capture"`
}

// Maxima cap what a hook may set. A zero field means "no maximum".
type Maxima struct {
	HoldTimeout     Duration  `json:"holdTimeout,omitempty"`
	ResponseTimeout Duration  `json:"responseTimeout,omitempty"`
	IdleTimeout     Duration  `json:"idleTimeout,omitempty"`
	MaxBodyBytes    int64     `json:"maxBodyBytes,omitempty"`
	RateLimit       RateLimit `json:"rateLimit,omitzero"`
}

// Policy holds the relay-wide defaults and maxima for hook settings.
type Policy struct {
	Defaults Effective
	Max      Maxima
}

// DefaultPolicy is DESIGN.md §8. The maxima keep a hook from holding
// connections open indefinitely; operators can raise them.
var DefaultPolicy = Policy{
	Defaults: Effective{
		AttachPolicy:    AttachTakeover,
		HostHeader:      HostPreserve,
		HoldTimeout:     Duration(30 * time.Second),
		ResponseTimeout: Duration(2 * time.Minute),
		IdleTimeout:     Duration(60 * time.Second),
		RateLimit:       RateLimit{PerSecond: 100, Burst: 200},
		Capture:         false, // bodies are the most sensitive part of a call
	},
	Max: Maxima{
		HoldTimeout:     Duration(5 * time.Minute),
		ResponseTimeout: Duration(30 * time.Minute),
		IdleTimeout:     Duration(30 * time.Minute),
	},
}

func (p Policy) Resolve(s Settings) Effective {
	e := p.Defaults
	if s.AttachPolicy != nil {
		e.AttachPolicy = *s.AttachPolicy
	}
	if s.HostHeader != nil {
		e.HostHeader = *s.HostHeader
	}
	if s.HoldTimeout != nil {
		e.HoldTimeout = *s.HoldTimeout
	}
	if s.ResponseTimeout != nil {
		e.ResponseTimeout = *s.ResponseTimeout
	}
	if s.IdleTimeout != nil {
		e.IdleTimeout = *s.IdleTimeout
	}
	if s.MaxBodyBytes != nil {
		e.MaxBodyBytes = *s.MaxBodyBytes
	}
	if s.RateLimit != nil {
		e.RateLimit = *s.RateLimit
	}
	if s.Capture != nil {
		e.Capture = *s.Capture
	}
	// Clamp to the maxima: a hook stored before a maximum was lowered must
	// not exceed it. A zero maximum means none.
	clamp := func(v *Duration, max Duration) {
		if max > 0 && *v > max {
			*v = max
		}
	}
	clamp(&e.HoldTimeout, p.Max.HoldTimeout)
	clamp(&e.ResponseTimeout, p.Max.ResponseTimeout)
	clamp(&e.IdleTimeout, p.Max.IdleTimeout)
	if max := p.Max.MaxBodyBytes; max > 0 && (e.MaxBodyBytes == 0 || e.MaxBodyBytes > max) {
		e.MaxBodyBytes = max // 0 is unlimited, which exceeds any maximum
	}
	if max := p.Max.RateLimit.PerSecond; max > 0 && e.RateLimit.PerSecond > max {
		e.RateLimit.PerSecond = max
	}
	if max := p.Max.RateLimit.Burst; max > 0 && e.RateLimit.Burst > max {
		e.RateLimit.Burst = max
	}
	return e
}

// Validate checks a hook's overrides against the policy's maxima.
func (p Policy) Validate(s Settings) ValidationError {
	var errs ValidationError
	add := func(field, format string, args ...any) {
		errs = append(errs, FieldError{Field: "settings." + field, Message: fmt.Sprintf(format, args...)})
	}
	if v := s.AttachPolicy; v != nil && *v != AttachTakeover && *v != AttachReject {
		add("attachPolicy", "must be %q or %q", AttachTakeover, AttachReject)
	}
	if v := s.HostHeader; v != nil && *v != HostPreserve && *v != HostRewrite {
		add("hostHeader", "must be %q or %q", HostPreserve, HostRewrite)
	}
	checkDur := func(field string, v *Duration, max Duration, allowZero bool) {
		switch {
		case v == nil:
		case *v == 0 && !allowZero:
			add(field, "must be greater than zero")
		case max > 0 && *v > max:
			add(field, "must be at most %s on this relay", max.Std())
		}
	}
	checkDur("holdTimeout", s.HoldTimeout, p.Max.HoldTimeout, true)
	checkDur("responseTimeout", s.ResponseTimeout, p.Max.ResponseTimeout, false)
	checkDur("idleTimeout", s.IdleTimeout, p.Max.IdleTimeout, false)
	if v := s.MaxBodyBytes; v != nil {
		switch {
		case *v < 0:
			add("maxBodyBytes", "must not be negative")
		case p.Max.MaxBodyBytes > 0 && (*v == 0 || *v > p.Max.MaxBodyBytes):
			add("maxBodyBytes", "must be between 1 and %d bytes on this relay", p.Max.MaxBodyBytes)
		}
	}
	if v := s.RateLimit; v != nil {
		switch {
		case v.PerSecond <= 0 || v.Burst < 1:
			add("rateLimit", "perSecond must be greater than zero and burst at least 1")
		case p.Max.RateLimit.PerSecond > 0 && v.PerSecond > p.Max.RateLimit.PerSecond,
			p.Max.RateLimit.Burst > 0 && v.Burst > p.Max.RateLimit.Burst:
			add("rateLimit", "must be at most %g/s with burst %d on this relay", p.Max.RateLimit.PerSecond, p.Max.RateLimit.Burst)
		}
	}
	return errs
}

// Check reports whether the defaults themselves are valid under the maxima,
// so a misconfigured relay fails at startup instead of on every hook.
func (p Policy) Check() error {
	d := p.Defaults
	if errs := p.Validate(Settings{
		AttachPolicy: &d.AttachPolicy, HostHeader: &d.HostHeader,
		HoldTimeout: &d.HoldTimeout, ResponseTimeout: &d.ResponseTimeout, IdleTimeout: &d.IdleTimeout,
		MaxBodyBytes: &d.MaxBodyBytes, RateLimit: &d.RateLimit, Capture: &d.Capture,
	}); len(errs) > 0 {
		return errors.New("hook defaults conflict with the maxima: " + errs.Error())
	}
	return nil
}
