package hooks

import (
	"fmt"
	"regexp"
	"strings"
)

// SlugMode selects how a user's URL slug is derived from their login.
type SlugMode string

const (
	SlugLocalPart SlugMode = "local-part" // moritz@example.com → moritz
	SlugLogin     SlugMode = "login"      // moritz@example.com → moritz-example.com
)

// SlugConfig derives the "~slug" URL prefix of each user.
type SlugConfig struct {
	Mode SlugMode // empty means SlugLocalPart
	// Overrides maps a login to an explicit slug, to resolve collisions.
	Overrides map[string]string
}

var slugRE = regexp.MustCompile(`^[a-z0-9._-]+$`)

// ValidSlug reports whether s can be used as a URL slug.
func ValidSlug(s string) bool { return slugRE.MatchString(s) && s != "." && s != ".." }

func (c SlugConfig) Slug(login string) (string, error) {
	if s, ok := c.Overrides[login]; ok {
		return s, nil
	}
	s := login
	if c.Mode != SlugLogin {
		if local, _, ok := strings.Cut(login, "@"); ok {
			s = local
		}
	}
	s = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			return r
		}
		return '-'
	}, strings.ToLower(s))
	if !ValidSlug(s) {
		return "", fmt.Errorf("cannot derive a URL slug from login %q; configure a slug override", login)
	}
	return s, nil
}
