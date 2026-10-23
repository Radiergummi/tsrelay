package authz

import (
	"slices"
	"strings"
	"testing"
	"time"

	"tailscale.com/tailcfg"
)

func raw(vs ...string) []tailcfg.RawMessage {
	out := make([]tailcfg.RawMessage, len(vs))
	for i, v := range vs {
		out[i] = tailcfg.RawMessage(v)
	}
	return out
}

func TestParseEmpty(t *testing.T) {
	eff, errs := Parse(nil, DefaultLimits)
	if len(errs) != 0 || eff.Has(RoleReceiver) || eff.Has(RoleSender) || eff.Has(RoleAdmin) {
		t.Fatalf("Parse(nil) = %+v, %v; want no roles, no errors", eff, errs)
	}
	if eff.Limits != DefaultLimits {
		t.Fatalf("limits = %+v, want defaults %+v", eff.Limits, DefaultLimits)
	}
}

func TestParseDefaultChannelsIsWildcard(t *testing.T) {
	eff, _ := Parse(raw(`{"roles":["receiver"]}`), DefaultLimits)
	if got := eff.Patterns[RoleReceiver]; !slices.Equal(got, []string{"*"}) {
		t.Fatalf("receiver patterns = %v, want [*]", got)
	}
	if !eff.Allows(RoleReceiver, "anything.at.all") {
		t.Fatal("wildcard should allow any channel")
	}
}

func TestParseUnionPerRole(t *testing.T) {
	eff, errs := Parse(raw(
		`{"roles":["receiver"],"channels":["prod"]}`,
		`{"roles":["receiver"],"channels":["staging.*","prod"]}`,
		`{"roles":["sender"],"channels":["billing"]}`,
	), DefaultLimits)
	if len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
	if got := eff.Patterns[RoleReceiver]; !slices.Equal(got, []string{"prod", "staging.*"}) {
		t.Fatalf("receiver patterns = %v", got)
	}
	if eff.Allows(RoleReceiver, "billing") {
		t.Fatal("sender channels must not widen receiver permissions")
	}
	if !eff.Allows(RoleSender, "billing") || eff.Allows(RoleSender, "prod") {
		t.Fatal("sender patterns wrong")
	}
}

func TestParseSkipsInvalidValues(t *testing.T) {
	eff, errs := Parse(raw(
		`not json`,
		`{"roles":[]}`,
		`{"roles":["sender"],"channels":["Bad/Pattern"]}`,
		`{"roles":["receiver"],"maxHooks":-1}`,
		`{"roles":["receiver"],"maxHookTtl":"-1h"}`,
		`{"roles":["receiver"],"channels":["prod"],"futureField":true}`,
	), DefaultLimits)
	if len(errs) != 6 { // five skipped values, and a note on futureField, whose value applies
		t.Fatalf("got %d errors (%v), want 6", len(errs), errs)
	}
	if eff.Has(RoleSender) {
		t.Fatal("invalid sender value must be ignored entirely")
	}
	if !eff.Allows(RoleReceiver, "prod") {
		t.Fatal("valid value with unknown field must still apply")
	}
}

func TestParseLimitsTakeMaximum(t *testing.T) {
	defaults := Limits{MaxHooks: 10, MaxTunnels: 5, MaxHookTTL: 24 * time.Hour}
	eff, _ := Parse(raw(
		`{"roles":["receiver"],"maxHooks":3}`,
		`{"roles":["receiver"],"maxTunnels":8,"maxHookTtl":"720h"}`,
	), defaults)
	want := Limits{MaxHooks: 10, MaxTunnels: 8, MaxHookTTL: 720 * time.Hour}
	if eff.Limits != want {
		t.Fatalf("limits = %+v, want %+v", eff.Limits, want)
	}
}

func TestParseUnlimitedTTLStaysUnlimited(t *testing.T) {
	eff, _ := Parse(raw(`{"roles":["receiver"],"maxHookTtl":"1h"}`), Limits{MaxHooks: 1})
	if eff.Limits.MaxHookTTL != 0 {
		t.Fatalf("MaxHookTTL = %v, want 0 (unlimited default wins)", eff.Limits.MaxHookTTL)
	}
}

func TestIdentityIsUser(t *testing.T) {
	if (Identity{UserID: 1}).IsUser() != true {
		t.Fatal("user node should be a user")
	}
	if (Identity{UserID: 1, Tags: []string{"tag:x"}}).IsUser() {
		t.Fatal("tagged node must not be a user")
	}
	if (Identity{}).IsUser() {
		t.Fatal("zero identity must not be a user")
	}
}

func TestParsePublic(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values []string
		want   bool
	}{
		{"absent", []string{`{"roles":["receiver"]}`}, false},
		{"receiver", []string{`{"roles":["receiver"],"public":true}`}, true},
		{"sender only", []string{`{"roles":["sender"],"public":true}`}, false},
		{"any grant", []string{`{"roles":["receiver"]}`, `{"roles":["receiver"],"public":true}`}, true},
		{"false", []string{`{"roles":["receiver"],"public":false}`}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			eff, errs := Parse(raw(tc.values...), DefaultLimits)
			if got := len(eff.PublicPatterns) > 0; len(errs) != 0 || got != tc.want {
				t.Fatalf("public = %v (errs %v), want %v", got, errs, tc.want)
			}
		})
	}
}

func TestParsePublicScoped(t *testing.T) {
	eff, errs := Parse(raw(
		`{"roles":["receiver"],"channels":["dev"],"public":true}`,
		`{"roles":["receiver"],"channels":["prod"]}`,
	), DefaultLimits)
	if len(errs) != 0 || !eff.AllowsPublic("dev") || eff.AllowsPublic("prod") {
		t.Fatalf("dev=%v prod=%v (errs %v), want true, false", eff.AllowsPublic("dev"), eff.AllowsPublic("prod"), errs)
	}
	if eff, _ := Parse(raw(`{"roles":["receiver"],"public":true}`), DefaultLimits); !eff.AllowsPublic("anything") {
		t.Fatal("public without channels must apply to every channel")
	}
	if eff, _ := Parse(raw(`{"roles":["sender"],"public":true}`), DefaultLimits); len(eff.PublicPatterns) != 0 {
		t.Fatalf("PublicPatterns = %v, want none", eff.PublicPatterns)
	}
}

func FuzzParse(f *testing.F) {
	f.Add(`{"roles":["receiver"],"channels":["prod.*"],"maxHooks":3,"maxHookTtl":"8h"}`)
	f.Fuzz(func(t *testing.T, v string) {
		eff, _ := Parse(raw(v), DefaultLimits)
		for _, ps := range eff.Patterns {
			for _, p := range ps {
				if !ValidPattern(p) {
					t.Fatalf("Parse accepted invalid pattern %q", p)
				}
			}
		}
	})
}

func TestParseEmptyChannelsMatchesNothing(t *testing.T) {
	eff, _ := Parse(raw(`{"roles":["receiver"],"channels":[],"public":true}`), DefaultLimits)
	if eff.Allows(RoleReceiver, "default") || eff.AllowsPublic("default") {
		t.Fatal(`"channels": [] must match nothing`)
	}
}

func TestParseLimitsOnlyFromReceiverValues(t *testing.T) {
	eff, _ := Parse(raw(`{"roles":["sender"],"maxHooks":50,"maxTunnels":50,"maxHookTtl":"720h"}`), Limits{MaxHooks: 10, MaxTunnels: 5, MaxHookTTL: time.Hour})
	if eff.Limits != (Limits{MaxHooks: 10, MaxTunnels: 5, MaxHookTTL: time.Hour}) {
		t.Fatalf("limits = %+v, a sender-only value must not raise them", eff.Limits)
	}
}

func TestParseSkipsValueRequiringUnknownFeature(t *testing.T) {
	eff, errs := Parse(raw(`{"roles":["receiver"],"requires":["expiry"]}`, `{"roles":["sender"]}`), DefaultLimits)
	if eff.Has(RoleReceiver) || !eff.Has(RoleSender) || len(errs) != 1 {
		t.Fatalf("receiver=%v sender=%v errs=%v", eff.Has(RoleReceiver), eff.Has(RoleSender), errs)
	}
}

func TestParseReportsUnknownRoles(t *testing.T) {
	eff, errs := Parse(raw(`{"roles":["receiver","recevier"]}`), DefaultLimits)
	if !eff.Has(RoleReceiver) || len(errs) != 1 || !strings.Contains(errs[0].Error(), "recevier") {
		t.Fatalf("errs = %v; unknown role must be reported, value still applied", errs)
	}
}

// A misspelled key is reported: "channel" for "channels" leaves channels
// absent, which grants every channel. The value still applies as written,
// so a key a newer relay adds doesn't break an older one.
func TestParseReportsUnknownKeys(t *testing.T) {
	eff, errs := Parse(raw(`{"roles":["sender"],"channel":["staging"],"maxHooks":3}`), DefaultLimits)
	if !eff.Has(RoleSender) || len(errs) != 1 || !strings.Contains(errs[0].Error(), `unknown key "channel"`) {
		t.Fatalf("errs = %v", errs)
	}
}

// encoding/json reads "Channels" as "channels", so it isn't reported as
// ignored, but its spelling is.
func TestParseReportsMiscasedKeys(t *testing.T) {
	eff, errs := Parse(raw(`{"roles":["sender"],"Channels":["staging"]}`), DefaultLimits)
	if !eff.Allows(RoleSender, "staging") || eff.Allows(RoleSender, "prod") || len(errs) != 1 || !strings.Contains(errs[0].Error(), `read as "channels"`) {
		t.Fatalf("errs = %v", errs)
	}
}
