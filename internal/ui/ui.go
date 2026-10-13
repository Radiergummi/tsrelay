// Package ui serves the relay's web UI (DESIGN.md §10.2): pages rendered on
// the server from the same state the JSON API reads, and one script that
// writes through that API and keeps pages live over its event stream.
package ui

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/Radiergummi/tsrelay/internal/api"
	"github.com/Radiergummi/tsrelay/internal/authz"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/inspect"
)

//go:embed templates static
var assets embed.FS

// CSP is the policy of every UI response (DESIGN.md §5.7): nothing loads
// from anywhere but the relay, and there is no inline script or style.
// Forms never submit natively; the script sends them to the API.
const CSP = "default-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// UI serves the pages. It reads hooks and calls in-process and shows them
// through the API's own view types.
type UI struct {
	api     *api.API
	hooks   *hooks.Manager
	inspect *inspect.Inspector
	capName string // shown in the no-access page's example grant
	pages   map[string]*template.Template
}

func New(a *api.API, insp *inspect.Inspector, capName string) *UI {
	return &UI{api: a, hooks: a.Hooks, inspect: insp, capName: capName, pages: parsePages()}
}

// Register mounts the UI's routes. They are all GET: changes go through the
// JSON API.
func (u *UI) Register(mux *http.ServeMux) {
	static, _ := fs.Sub(assets, "static")
	files := http.StripPrefix("/static/", http.FileServerFS(static))
	mux.Handle("GET /static/", secure(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		files.ServeHTTP(w, r)
	})))
	mux.Handle("GET /{$}", u.page(u.hooksPage))
	mux.Handle("GET /hooks/new", u.page(u.newHookPage))
	mux.Handle("GET /hooks/{id}/edit", u.page(u.editHookPage))
	mux.Handle("GET /hooks/{id}", u.page(u.hookPage))
	mux.Handle("GET /hooks/{id}/calls/{callID}", u.page(u.callPage))
	mux.Handle("GET /inspect/{callID}", u.page(u.inspectRedirect))
}

// NotFound answers unknown non-API paths with an HTML 404 page.
func (u *UI) NotFound() http.Handler {
	return u.page(func(w http.ResponseWriter, r *http.Request, v viewer) {
		u.error(w, v, http.StatusNotFound, "There is no page here.")
	})
}

// viewer is the caller of a page.
type viewer struct {
	ID authz.Identity
	Me api.Me
}

func (v viewer) Admin() bool    { return v.ID.Grants.Has(authz.RoleAdmin) }
func (v viewer) Receiver() bool { return v.ID.IsUser() && v.ID.Grants.Has(authz.RoleReceiver) }

func (v viewer) owns(h hooks.Hook) bool { return api.Owns(v.ID, h) }

// page identifies the caller and runs h with the security headers set.
func (u *UI) page(h func(http.ResponseWriter, *http.Request, viewer)) http.Handler {
	return secure(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := u.api.Identifier.Identify(r.Context(), r.RemoteAddr)
		if err != nil {
			u.error(w, viewer{}, http.StatusForbidden,
				"The relay could not identify your device on the tailnet. Reload the page; if this keeps happening, the relay may have lost its Tailscale connection.")
			return
		}
		h(w, r, viewer{ID: id, Me: u.api.MeOf(id)})
	}))
}

func secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", CSP)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// visibleHook returns the hook with the given ID if v owns it or is an
// admin, like the API's visibility rule.
func (u *UI) visibleHook(id string, v viewer) (hooks.Hook, bool) {
	h, ok := u.hooks.Get(hooks.ID(id))
	return h, ok && (v.owns(h) || v.Admin())
}

// render executes page into a buffer first, so a template error never
// sends half a page.
func (u *UI) render(w http.ResponseWriter, status int, page string, data any) {
	var buf bytes.Buffer
	if err := u.pages[page].ExecuteTemplate(&buf, "layout", data); err != nil {
		u.api.Audit.Slog().Error("rendering a page failed", "page", page, "error", err)
		http.Error(w, "could not render the page", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	buf.WriteTo(w)
}

func (u *UI) error(w http.ResponseWriter, v viewer, status int, msg string) {
	u.render(w, status, "error", struct {
		V              viewer
		Title, Message string
	}{v, http.StatusText(status), msg})
}

// parsePages parses each page template together with the layout and the
// shared partials.
func parsePages() map[string]*template.Template {
	names, _ := fs.Glob(assets, "templates/*.html")
	pages := map[string]*template.Template{}
	for _, n := range names {
		base := strings.TrimSuffix(path.Base(n), ".html")
		if base == "layout" || base == "partials" {
			continue
		}
		pages[base] = template.Must(template.New(base).Funcs(funcs).ParseFS(assets,
			"templates/layout.html", "templates/partials.html", n))
	}
	return pages
}

var funcs = template.FuncMap{
	"when": func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return t.UTC().Format("2006-01-02 15:04:05Z")
	},
	"iso":  func(t time.Time) string { return t.UTC().Format(time.RFC3339) },
	"dur":  formatDuration,
	"size": formatSize,
	"join": strings.Join,
	// outcomeClass is the CSS class of a call outcome.
	"outcomeClass": func(outcome string) string {
		switch outcome {
		case inspect.OutcomeOK:
			return "ok"
		case inspect.OutcomePending:
			return "pending"
		}
		return "bad"
	},
}

// formatDuration shows d to the millisecond, or to the microsecond below that.
func formatDuration(d hooks.Duration) string {
	std := d.Std()
	if std < time.Millisecond {
		return std.Round(time.Microsecond).String()
	}
	return std.Round(time.Millisecond).String()
}

func formatSize(n int64) string {
	switch {
	case n < 1<<10:
		return fmt.Sprintf("%d B", n)
	case n < 1<<20:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
}

// sortedRoles lists the roles in g, sorted.
func sortedRoles(g authz.Effective) []string {
	var out []string
	for r := range g.Patterns {
		out = append(out, string(r))
	}
	slices.Sort(out)
	return out
}
