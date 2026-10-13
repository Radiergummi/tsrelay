package inspect

import (
	"net/http"
	"slices"
	"strings"
)

// DefaultRedact lists the headers whose values are never captured
// (DESIGN.md §9.2).
var DefaultRedact = []string{"Authorization", "Proxy-Authorization", "Cookie", "Set-Cookie", "X-Api-Key"}

// Redactor drops the values of a fixed set of headers. Names match without
// regard to case.
type Redactor map[string]bool

func NewRedactor(names []string) Redactor {
	r := Redactor{}
	for _, n := range names {
		if n = strings.TrimSpace(n); n != "" {
			r[http.CanonicalHeaderKey(n)] = true
		}
	}
	return r
}

// Headers returns a copy of h without the redacted headers, with canonical
// names, and the sorted names of the headers it left out. ReverseProxy
// stashes unannounced trailers under keys prefixed with http.TrailerPrefix;
// the prefix is stripped before the redaction lookup, so e.g. a trailer
// named Authorization is redacted like a header of that name, but a kept
// trailer's key is left as the caller gave it.
func (r Redactor) Headers(h http.Header) (kept http.Header, redacted []string) {
	kept = make(http.Header, len(h))
	for k, v := range h {
		ck := http.CanonicalHeaderKey(strings.TrimPrefix(k, http.TrailerPrefix))
		if r[ck] {
			if !slices.Contains(redacted, ck) {
				redacted = append(redacted, ck)
			}
			continue
		}
		kept[k] = append(kept[k], v...)
	}
	slices.Sort(redacted)
	return kept, redacted
}
