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
// A segment with another extension before it, as in /sbom.cdx.json, names
// a file and is kept.
func Extensions(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for ext, t := range extensions {
			p, ok := strings.CutSuffix(r.URL.Path, ext)
			if !ok || strings.HasSuffix(p, "/") || strings.Contains(p[strings.LastIndexByte(p, '/')+1:], ".") {
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

// Stripped reports whether Extensions removed an extension from r's path.
func Stripped(r *http.Request) bool {
	_, ok := r.Context().Value(forcedKey{}).(Type)
	return ok
}

// Pick returns the representation of offers that r asks for, and false when
// it asks for none of them. It adds Vary: Accept to the response.
func Pick(w http.ResponseWriter, r *http.Request, offers ...Type) (Type, bool) {
	w.Header().Add("Vary", "Accept")
	if t, ok := r.Context().Value(forcedKey{}).(Type); ok {
		for _, o := range offers {
			if o == t {
				return t, true
			}
		}
		return t, false
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
