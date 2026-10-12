package hooks

import (
	_ "embed"
	"math/rand/v2"
	"strings"
)

//go:embed words.txt
var wordsFile string

var words = strings.Fields(wordsFile)

// RandomPath returns three random words joined by dashes, e.g. "amber-otter-lamp".
// Paths are scoped to a user and need to be readable, not unguessable.
func RandomPath() string {
	return words[rand.IntN(len(words))] + "-" + words[rand.IntN(len(words))] + "-" + words[rand.IntN(len(words))]
}
