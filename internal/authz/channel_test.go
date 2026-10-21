package authz

import (
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestMatchChannel(t *testing.T) {
	tests := []struct {
		pattern, name string
		want          bool
	}{
		{"*", "prod", true},
		{"*", "prod.eu.west", true},
		{"prod", "prod", true},
		{"prod", "prod.eu", false},
		{"prod.*", "prod.eu", true},
		{"prod.*", "prod.eu.west", true},
		{"prod.*", "prod", false},
		{"*.eu", "prod.eu", true},
		{"*.eu", "prod.eu.west", false},
		{"p*d", "prod", true},
		{"p*d", "prod.eu", false},
		{"**", "x", true},
		{"a*b*c", "axxbyyc", true},
		{"a*b*c", "axxbyy", false},
		{"staging", "prod", false},
	}
	for _, tt := range tests {
		if got := MatchChannel(tt.pattern, tt.name); got != tt.want {
			t.Errorf("MatchChannel(%q, %q) = %v, want %v", tt.pattern, tt.name, got, tt.want)
		}
	}
}

func TestValidChannel(t *testing.T) {
	for name, want := range map[string]bool{
		"default": true, "prod.eu": true, "a-b.c-d": true, "x1": true,
		"Prod": false, "prod..eu": false, ".prod": false, "prod.": false,
		"": false, "pr*d": false, "prod/eu": false, strings.Repeat("a", 65): false,
		// A last segment that is a representation would make the channel's
		// URL name another channel: /channels/a.json is channel a as JSON.
		"billing.events.json": false, "a.html": false, "json": true, "a.jsonx": true, "a.json.b": true,
	} {
		if got := ValidChannel(name); got != want {
			t.Errorf("ValidChannel(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestValidPattern(t *testing.T) {
	for p, want := range map[string]bool{
		"*": true, "prod.*": true, "*.eu": true, "prod": true,
		"Prod": false, "": false, "prod/eu": false, "pr d": false,
	} {
		if got := ValidPattern(p); got != want {
			t.Errorf("ValidPattern(%q) = %v, want %v", p, got, want)
		}
	}
}

// FuzzMatchChannel compares MatchChannel against a regexp-based reference.
func FuzzMatchChannel(f *testing.F) {
	f.Add("prod.*", "prod.eu")
	f.Add("a*b*c", "axxbyyc")
	f.Fuzz(func(t *testing.T, pattern, name string) {
		if !utf8.ValidString(pattern) || !utf8.ValidString(name) {
			t.Skip("the regexp reference needs valid UTF-8; channels are ASCII")
		}
		parts := strings.Split(pattern, "*")
		for i, p := range parts {
			parts[i] = regexp.QuoteMeta(p)
		}
		ref := regexp.MustCompile(`^(?s:` + strings.Join(parts, ".*") + `)$`)
		if got, want := MatchChannel(pattern, name), ref.MatchString(name); got != want {
			t.Fatalf("MatchChannel(%q, %q) = %v, reference says %v", pattern, name, got, want)
		}
	})
}
