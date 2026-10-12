package hooks

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const (
	maxPathLen     = 128
	maxSegments    = 8
	maxDescription = 280
)

var segmentRE = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// ValidatePath checks a hook path: 1–8 segments of [A-Za-z0-9._-], at most
// 128 characters, no "." or ".." segments.
func ValidatePath(p string) error {
	if p == "" {
		return errors.New("path must not be empty")
	}
	if len(p) > maxPathLen {
		return fmt.Errorf("path must be at most %d characters", maxPathLen)
	}
	segs := strings.Split(p, "/")
	if len(segs) > maxSegments {
		return fmt.Errorf("path must have at most %d segments", maxSegments)
	}
	for _, s := range segs {
		if s == "." || s == ".." {
			return fmt.Errorf("path segment %q is not allowed", s)
		}
		if !segmentRE.MatchString(s) {
			return fmt.Errorf("path segment %q must match [A-Za-z0-9._-]+", s)
		}
	}
	return nil
}

// overlaps reports whether two hooks of one user at paths a and b would make
// routing ambiguous: equal paths, or one nested in the other.
func overlaps(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}
