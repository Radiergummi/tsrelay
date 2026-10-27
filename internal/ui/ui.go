// Package ui serves the relay's web UI (DESIGN.md §10.2): a single-page
// app built from web/ and embedded in the binary. Every app route asked
// for as HTML gets index.html, including the API's resources; the app
// reads and writes them as JSON, so the UI has no endpoints of its own
// except the redirect for the CLI's links, whose target the API resolves.
package ui

import (
	"io/fs"
	"net/http"
)

// CSP is the policy of every UI response (DESIGN.md §5.7): nothing loads
// from anywhere but the relay, and there is no inline script or style.
const CSP = "default-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// UI serves the built app from dist.
type UI struct {
	dist     fs.FS
	index    []byte // nil when the UI is not built into this binary
	etag     string // index's
	manifest map[string]assetEntry
}

func New(dist fs.FS) *UI {
	u := &UI{dist: dist, manifest: loadManifest(dist)}
	if b, err := fs.ReadFile(dist, "index.html"); err == nil {
		u.index, u.etag = b, `"`+etagOf(b)+`"`
	}
	return u
}

// Index serves the app, for the API's resources asked for as HTML.
func (u *UI) Index() http.Handler {
	return secure(http.HandlerFunc(u.app))
}

// Inspect serves the inspect links the CLI prints for each call (DESIGN.md
// §15): it redirects to the page target names for the request, and when
// there is none (a call that is gone, or that the caller may not see) it
// serves the app with a 404 status; its /inspect/ route says the call has
// expired.
func (u *UI) Inspect(target func(*http.Request) (string, bool)) http.Handler {
	return secure(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if to, ok := target(r); ok {
			http.Redirect(w, r, to, http.StatusSeeOther)
			return
		}
		u.writeIndex(w, r, http.StatusNotFound)
	}))
}

// Fallback serves every GET no route claims: the app and its assets.
func (u *UI) Fallback() http.Handler {
	return secure(http.HandlerFunc(u.serve))
}

func secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", CSP)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}
