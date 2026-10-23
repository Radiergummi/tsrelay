package authz

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
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
	// PublicPatterns are the channels in which the node's user may make hooks
	// reachable from the internet. Only a value that grants the receiver role
	// contributes, and only for its own channels.
	PublicPatterns []string
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

// AllowsPublic reports whether one of the public patterns matches channel.
func (e Effective) AllowsPublic(channel string) bool {
	for _, p := range e.PublicPatterns {
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
	MaxHookTTL *duration `json:"maxHookTtl"`
	Public     bool      `json:"public"`
	// Requires names the features a relay must support to apply this value
	// correctly. A relay that lacks one skips the value rather than apply it
	// with a narrower meaning than the policy author intended.
	Requires []string `json:"requires"`
}

// grantKeys are the keys of a grant value, as grantValue names them.
var grantKeys = []string{"roles", "channels", "maxHooks", "maxTunnels", "maxHookTtl", "public", "requires"}

// grantFeatures are the features of the grant format this relay supports,
// for values to name in "requires". There are none yet.
var grantFeatures = map[string]bool{}

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
// parsed on its own: an invalid value, or one that "requires" a feature this
// relay lacks, is skipped and reported in errs while the others still apply.
// Unknown roles are reported too, but the rest of their value applies. Roles
// and channels are combined by union, per role. An absent "channels" means
// every channel; an empty one matches none. Limits are per user and come from
// values that grant the receiver role only. A limit a value does not set
// counts as the default; the largest limit wins, so grants can raise limits
// above the defaults but never lower them.
func Parse(values []tailcfg.RawMessage, defaults Limits) (eff Effective, errs []error) {
	eff.Patterns = map[Role][]string{}
	eff.Limits = defaults
	for i, rv := range values {
		v, notes, err := parseValue(rv)
		for _, note := range notes {
			errs = append(errs, fmt.Errorf("capability value %d: %w", i, note))
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("capability value %d: %w", i, err))
			continue
		}
		channels := v.Channels
		if channels == nil {
			channels = []string{"*"}
		}
		isReceiver := slices.Contains(v.Roles, RoleReceiver)
		for _, r := range v.Roles {
			for _, c := range channels {
				if !slices.Contains(eff.Patterns[r], c) {
					eff.Patterns[r] = append(eff.Patterns[r], c)
				}
			}
		}
		if isReceiver && v.MaxHooks != nil {
			eff.Limits.MaxHooks = max(eff.Limits.MaxHooks, *v.MaxHooks)
		}
		if isReceiver && v.MaxTunnels != nil {
			eff.Limits.MaxTunnels = max(eff.Limits.MaxTunnels, *v.MaxTunnels)
		}
		if isReceiver && v.MaxHookTTL != nil && eff.Limits.MaxHookTTL != 0 {
			eff.Limits.MaxHookTTL = max(eff.Limits.MaxHookTTL, time.Duration(*v.MaxHookTTL))
		}
		if v.Public && isReceiver {
			for _, c := range channels {
				if !slices.Contains(eff.PublicPatterns, c) {
					eff.PublicPatterns = append(eff.PublicPatterns, c)
				}
			}
		}
	}
	return eff, errs
}

// parseValue decodes one value. notes are non-fatal findings: the value still
// applies, without the offending part.
func parseValue(rv tailcfg.RawMessage) (v grantValue, notes []error, err error) {
	if err := json.Unmarshal([]byte(rv), &v); err != nil {
		return v, nil, err
	}
	// encoding/json ignores keys it doesn't know. One is most likely a
	// typo, and a misspelled "channels" grants every channel, so say so.
	var keys map[string]json.RawMessage
	if json.Unmarshal([]byte(rv), &keys) == nil {
		for _, k := range slices.Sorted(maps.Keys(keys)) {
			switch i := slices.IndexFunc(grantKeys, func(g string) bool { return strings.EqualFold(g, k) }); {
			case i < 0:
				notes = append(notes, fmt.Errorf("unknown key %q, ignored", k))
			case grantKeys[i] != k:
				notes = append(notes, fmt.Errorf("key %q is read as %q; spell it so", k, grantKeys[i]))
			}
		}
	}
	for _, f := range v.Requires {
		if !grantFeatures[f] {
			return v, nil, fmt.Errorf("requires unknown feature %q", f)
		}
	}
	v.Roles = slices.DeleteFunc(v.Roles, func(r Role) bool {
		if r == RoleReceiver || r == RoleSender || r == RoleAdmin {
			return false
		}
		notes = append(notes, fmt.Errorf("unknown role %q", r))
		return true
	})
	if len(v.Roles) == 0 {
		return v, notes, errors.New("no roles")
	}
	for _, c := range v.Channels {
		if !ValidPattern(c) {
			return v, notes, fmt.Errorf("invalid channel pattern %q", c)
		}
	}
	if v.MaxHooks != nil && *v.MaxHooks < 0 {
		return v, notes, errors.New("maxHooks must not be negative")
	}
	if v.MaxTunnels != nil && *v.MaxTunnels < 0 {
		return v, notes, errors.New("maxTunnels must not be negative")
	}
	return v, notes, nil
}
