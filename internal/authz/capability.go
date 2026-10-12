package authz

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"tailscale.com/tailcfg"
)

// Role is a permission a grant confers on the relay.
type Role string

const (
	RoleReceiver Role = "receiver"
	RoleSender   Role = "sender"
	RoleAdmin    Role = "admin"
)

// Limits apply to receivers.
type Limits struct {
	MaxHooks   int
	MaxTunnels int
	// MaxHookTTL caps how far in the future a hook may expire. Zero means unlimited.
	MaxHookTTL time.Duration
}

// DefaultLimits are the relay-wide defaults when no configuration overrides them.
var DefaultLimits = Limits{MaxHooks: 10, MaxTunnels: 5}

// Effective is the merged result of every capability value granted to a node.
type Effective struct {
	// Patterns maps each granted role to the channel patterns it applies to.
	Patterns map[Role][]string
	Limits   Limits
}

// Has reports whether the role was granted at all.
func (e Effective) Has(r Role) bool {
	_, ok := e.Patterns[r]
	return ok
}

// Allows reports whether one of the role's patterns matches channel.
func (e Effective) Allows(r Role, channel string) bool {
	for _, p := range e.Patterns[r] {
		if MatchChannel(p, channel) {
			return true
		}
	}
	return false
}

type grantValue struct {
	Roles      []Role    `json:"roles"`
	Channels   []string  `json:"channels"`
	MaxHooks   *int      `json:"maxHooks"`
	MaxTunnels *int      `json:"maxTunnels"`
	MaxHookTTL *duration `json:"maxHookTTL"`
}

type duration time.Duration

func (d *duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf(`duration must be a string like "8h": %w`, err)
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	if v <= 0 {
		return fmt.Errorf("duration %q must be positive", s)
	}
	*d = duration(v)
	return nil
}

// Parse merges the raw JSON values of the relay capability. Each value is
// parsed on its own: an invalid value is skipped and reported in errs while
// the others still apply. Roles and channels are combined by union, per role.
// A limit a value does not set counts as the default; the largest limit wins,
// so grants can raise limits above the defaults but never lower them.
func Parse(values []tailcfg.RawMessage, defaults Limits) (eff Effective, errs []error) {
	eff.Patterns = map[Role][]string{}
	eff.Limits = defaults
	for i, rv := range values {
		v, err := parseValue(rv)
		if err != nil {
			errs = append(errs, fmt.Errorf("capability value %d: %w", i, err))
			continue
		}
		channels := v.Channels
		if len(channels) == 0 {
			channels = []string{"*"}
		}
		for _, r := range v.Roles {
			for _, c := range channels {
				if !slices.Contains(eff.Patterns[r], c) {
					eff.Patterns[r] = append(eff.Patterns[r], c)
				}
			}
		}
		if v.MaxHooks != nil {
			eff.Limits.MaxHooks = max(eff.Limits.MaxHooks, *v.MaxHooks)
		}
		if v.MaxTunnels != nil {
			eff.Limits.MaxTunnels = max(eff.Limits.MaxTunnels, *v.MaxTunnels)
		}
		if v.MaxHookTTL != nil && eff.Limits.MaxHookTTL != 0 {
			eff.Limits.MaxHookTTL = max(eff.Limits.MaxHookTTL, time.Duration(*v.MaxHookTTL))
		}
	}
	return eff, errs
}

func parseValue(rv tailcfg.RawMessage) (grantValue, error) {
	var v grantValue
	if err := json.Unmarshal([]byte(rv), &v); err != nil {
		return v, err
	}
	if len(v.Roles) == 0 {
		return v, errors.New("no roles")
	}
	for _, c := range v.Channels {
		if !ValidPattern(c) {
			return v, fmt.Errorf("invalid channel pattern %q", c)
		}
	}
	if v.MaxHooks != nil && *v.MaxHooks < 0 {
		return v, errors.New("maxHooks must not be negative")
	}
	if v.MaxTunnels != nil && *v.MaxTunnels < 0 {
		return v, errors.New("maxTunnels must not be negative")
	}
	return v, nil
}
