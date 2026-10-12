package ids

import (
	"regexp"
	"testing"
)

func TestNew(t *testing.T) {
	re := regexp.MustCompile(`^h_[a-z2-7]{13}$`)
	seen := map[string]bool{}
	for range 1000 {
		id := New("h_")
		if !re.MatchString(id) {
			t.Fatalf("New(%q) = %q, want match %s", "h_", id, re)
		}
		if seen[id] {
			t.Fatalf("duplicate id %q", id)
		}
		seen[id] = true
	}
}
