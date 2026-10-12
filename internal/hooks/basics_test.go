package hooks

import (
	"regexp"
	"strings"
	"testing"
)

func TestValidatePath(t *testing.T) {
	for p, ok := range map[string]bool{
		"amber-otter-lamp": true, "a/b/c": true, "A.b_c-d": true,
		"": false, "/a": false, "a/": false, "a//b": false, "a/../b": false, ".": false,
		"a b": false, "a?b": false, "~x": false,
		strings.Repeat("a/", 8) + "a": false, strings.Repeat("a", 129): false,
	} {
		if err := ValidatePath(p); (err == nil) != ok {
			t.Errorf("ValidatePath(%q) = %v, want ok=%v", p, err, ok)
		}
	}
}

func TestOverlaps(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"a", "a", true}, {"a", "a/b", true}, {"a/b", "a", true},
		{"ab", "a", false}, {"a/bc", "a/b", false}, {"x", "y", false},
	}
	for _, c := range cases {
		if got := overlaps(c.a, c.b); got != c.want {
			t.Errorf("overlaps(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestSlug(t *testing.T) {
	local := SlugConfig{Mode: SlugLocalPart}
	full := SlugConfig{Mode: SlugLogin}
	over := SlugConfig{Overrides: map[string]string{"alice@b.com": "alice-b"}}
	cases := []struct {
		cfg   SlugConfig
		login string
		want  string
	}{
		{local, "Moritz@Example.com", "moritz"},
		{local, "first.last+tag@x.io", "first.last-tag"},
		{local, "octocat@github", "octocat"},
		{local, "no-at-sign", "no-at-sign"},
		{full, "moritz@example.com", "moritz-example.com"},
		{over, "alice@b.com", "alice-b"},
		{over, "alice@a.com", "alice"},
	}
	for _, c := range cases {
		got, err := c.cfg.Slug(c.login)
		if err != nil || got != c.want {
			t.Errorf("Slug(%q) = %q, %v; want %q", c.login, got, err, c.want)
		}
	}
	if _, err := local.Slug(".@x"); err == nil {
		t.Error(`Slug(".@x") should fail`)
	}
}

func TestRandomPath(t *testing.T) {
	if len(words) < 1000 {
		t.Fatalf("wordlist has %d words, want >= 1000", len(words))
	}
	re := regexp.MustCompile(`^[a-z]+-[a-z]+-[a-z]+$`)
	for range 100 {
		p := RandomPath()
		if !re.MatchString(p) || ValidatePath(p) != nil {
			t.Fatalf("RandomPath() = %q", p)
		}
	}
}

func TestHookRoute(t *testing.T) {
	if got := (Hook{Slug: "moritz", Path: "a/b"}).Route(); got != "/~moritz/a/b" {
		t.Fatalf("Route() = %q", got)
	}
	if !strings.HasPrefix(string(NewID()), "h_") {
		t.Fatal("hook IDs start with h_")
	}
}
