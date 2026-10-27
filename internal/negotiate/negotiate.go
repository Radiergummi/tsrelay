// Package negotiate picks the representation of a resource the client asked
// for (DESIGN.md §10.1): a .json or .html extension on the path wins, then
// the Accept header (RFC 9110, with q-values and wildcards), and JSON when
// neither decides.
package negotiate

import (
	"cmp"
	"context"
	"mime"
	"net/http"
	"slices"
	"strconv"
	"strings"
)

// Type is a representation a resource can have.
type Type int

const (
	JSON Type = iota + 1
	HTML
	EventStream
)

func (t Type) String() string {
	switch t {
	case JSON:
		return "application/json"
	case HTML:
		return "text/html"
	case EventStream:
		return "text/event-stream"
	}
	return ""
}

// extensions are the path suffixes that select a representation.
var extensions = map[string]Type{".json": JSON, ".html": HTML}

type forcedKey struct{}

// Extensions strips a .json or .html extension from the last segment of
// the path and remembers it for Pick, so routes match the bare resource.
// A dot before it is part of the name: /channels/team.payments.json is the
// channel team.payments as JSON. The literal paths are file names, such as
// /sbom.cdx.json, and keep their extension.
func Extensions(literal []string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if slices.Contains(literal, r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		for ext, t := range extensions {
			p, ok := strings.CutSuffix(r.URL.Path, ext)
			if !ok || strings.HasSuffix(p, "/") {
				continue
			}
			u := *r.URL
			u.Path, u.RawPath = p, ""
			r = r.WithContext(context.WithValue(r.Context(), forcedKey{}, t))
			r.URL = &u
			break
		}
		next.ServeHTTP(w, r)
	})
}

// MidPath reports whether an extension ends a segment other than the last,
// as in /hooks.json/calls: no resource has such a path.
func MidPath(path string) bool {
	for ext := range extensions {
		if strings.Contains(path, ext+"/") {
			return true
		}
	}
	return false
}

// Forced returns the representation an extension on r's path asked for,
// and false when Extensions stripped none.
func Forced(r *http.Request) (Type, bool) {
	t, ok := r.Context().Value(forcedKey{}).(Type)
	return t, ok
}

// Extension reports whether path ends in an extension Extensions strips.
func Extension(path string) bool {
	for ext := range extensions {
		if strings.HasSuffix(path, ext) {
			return true
		}
	}
	return false
}

// Pick returns the representation of offers that r asks for, and false when
// it asks for none of them. It adds Vary: Accept to the response.
func Pick(w http.ResponseWriter, r *http.Request, offers ...Type) (Type, bool) {
	w.Header().Add("Vary", "Accept")
	if t, ok := Forced(r); ok {
		return t, slices.Contains(offers, t)
	}
	accept := strings.Join(r.Header.Values("Accept"), ",")
	if strings.TrimSpace(accept) == "" {
		accept = "*/*"
	}
	ranges := parse(accept)
	best, bestQ := Type(0), 0.0
	for _, o := range offers {
		if q := quality(ranges, o); q > bestQ {
			best, bestQ = o, q
		}
	}
	return best, best != 0
}

// mediaRange is one entry of an Accept header.
type mediaRange struct {
	typ, sub string
	q        float64
}

func parse(accept string) []mediaRange {
	var out []mediaRange
	for part := range strings.SplitSeq(accept, ",") {
		mt, params, err := mime.ParseMediaType(strings.TrimSpace(part))
		if err != nil {
			continue
		}
		typ, sub, ok := strings.Cut(mt, "/")
		if !ok {
			continue
		}
		q, err := strconv.ParseFloat(cmp.Or(params["q"], "1"), 64)
		if err != nil || q < 0 || q > 1 {
			continue
		}
		out = append(out, mediaRange{typ, sub, q})
	}
	return out
}

// names are the media types each representation answers to. JSON also
// answers to the versioned alias.
var names = map[Type][]string{
	JSON:        {"application/json", "application/vnd.tsrelay.v1+json"},
	HTML:        {"text/html", "application/xhtml+xml"},
	EventStream: {"text/event-stream"},
}

// quality is the q-value ranges give t: that of the most specific range
// matching one of t's names (RFC 9110 §12.5.1). A stream is never chosen
// by a wildcard, only when asked for by name.
func quality(ranges []mediaRange, t Type) float64 {
	q, spec := 0.0, -1
	for _, name := range names[t] {
		typ, sub, _ := strings.Cut(name, "/")
		for _, mr := range ranges {
			s := -1
			switch {
			case mr.typ == typ && mr.sub == sub:
				s = 2
			case t == EventStream:
			case mr.typ == typ && mr.sub == "*":
				s = 1
			case mr.typ == "*" && mr.sub == "*":
				s = 0
			}
			if s > spec || s == spec && s >= 0 && mr.q > q {
				q, spec = mr.q, s
			}
		}
	}
	return q
}
