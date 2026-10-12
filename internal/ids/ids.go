// Package ids generates short random identifiers such as "h_k3j5..." for hooks.
package ids

import (
	"crypto/rand"
	"encoding/base32"
	"strings"
)

var encoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// New returns prefix followed by 13 random lowercase base32 characters (64 bits).
func New(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b) // crypto/rand.Read never returns an error
	return prefix + strings.ToLower(encoding.EncodeToString(b))
}
