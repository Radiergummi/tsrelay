// Package authz turns Tailscale application capabilities into tsrelay permissions.
package authz

import (
	"regexp"
	"strings"
)

// DefaultChannel is the channel a hook is created in when none is given.
const DefaultChannel = "default"

const maxChannelLen = 64

var (
	channelRE = regexp.MustCompile(`^[a-z0-9-]+(\.[a-z0-9-]+)*$`)
	patternRE = regexp.MustCompile(`^[a-z0-9.*-]+$`)
)

// ValidChannel reports whether name is a valid concrete channel name:
// dot-separated segments of [a-z0-9-], the last of which is not json or
// html: /channels/a.json is channel a as JSON (DESIGN.md §10.1).
func ValidChannel(name string) bool {
	return len(name) <= maxChannelLen && channelRE.MatchString(name) &&
		!strings.HasSuffix(name, ".json") && !strings.HasSuffix(name, ".html")
}

// ValidPattern reports whether p is a valid channel pattern for use in a grant.
func ValidPattern(p string) bool {
	return len(p) <= maxChannelLen && patternRE.MatchString(p)
}

// MatchChannel reports whether pattern matches the concrete channel name.
// A '*' matches any run of characters, dots included; all other characters
// match literally.
func MatchChannel(pattern, name string) bool {
	p, n := 0, 0
	star, mark := -1, 0
	for n < len(name) {
		switch {
		case p < len(pattern) && pattern[p] == '*':
			star, mark = p, n
			p++
		case p < len(pattern) && pattern[p] == name[n]:
			p++
			n++
		case star >= 0:
			p = star + 1
			mark++
			n = mark
		default:
			return false
		}
	}
	for p < len(pattern) && pattern[p] == '*' {
		p++
	}
	return p == len(pattern)
}
