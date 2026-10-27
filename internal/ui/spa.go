package ui

import (
	"bytes"
	"io"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/Radiergummi/tsrelay/internal/negotiate"
	"github.com/Radiergummi/tsrelay/internal/problem"
)

// placeholder is what every app route shows when the binary was built
// without the UI (a plain `go build`).
var placeholder = []byte(`<!doctype html>
<meta charset="utf-8">
<title>tsrelay</title>
<p>The web UI is not built into this binary; run <code>make ui</code> and rebuild.</p>
`)

// serve answers a path no other route matched: a built file if one
// exists, and otherwise index.html for a client asking for HTML, so the
// app can route it. Hidden files (.gitkeep, Vite's .vite/) are never
// served.
func (u *UI) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		notFound(w)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/")
	if name != "" && name != "index.html" && !hidden(name) {
		if f, err := u.dist.Open(name); err == nil {
			info, err := f.Stat()
			f.Close()
			if err == nil && !info.IsDir() {
				u.serveAsset(w, r, name)
				return
			}
		}
	}
	if t, _ := negotiate.Pick(w, r, negotiate.JSON, negotiate.HTML); t != negotiate.HTML {
		notFound(w)
		return
	}
	u.app(w, r)
}

// app sends the app for a request asking for HTML. A .html extension is
// redirected away first, since the app routes only the bare URL.
func (u *UI) app(w http.ResponseWriter, r *http.Request) {
	if _, ok := negotiate.Forced(r); ok {
		http.Redirect(w, r, r.URL.RequestURI(), http.StatusFound)
		return
	}
	u.writeIndex(w, r, http.StatusOK)
}

func notFound(w http.ResponseWriter) {
	problem.New(http.StatusNotFound, problem.NotFound, "not found").Write(w)
}

func hidden(name string) bool {
	for seg := range strings.SplitSeq(name, "/") {
		if strings.HasPrefix(seg, ".") {
			return true
		}
	}
	return false
}

// writeIndex sends index.html, or the placeholder without a build. It is
// never cached without revalidation, so a new release takes effect on the
// next navigation.
func (u *UI) writeIndex(w http.ResponseWriter, r *http.Request, status int) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-cache")
	body := placeholder
	if u.index != nil {
		body = u.index
		h.Set("ETag", u.etag)
	}
	if status == http.StatusOK {
		http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(body))
		return
	}
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		w.Write(body)
	}
}

// serveAsset serves a built file, choosing a precompressed variant when
// the client accepts one. Files under assets/ have hashed names and never
// change; everything else is revalidated on every use.
func (u *UI) serveAsset(w http.ResponseWriter, r *http.Request, name string) {
	h := w.Header()
	file := name
	if e, ok := u.manifest[name]; ok {
		h.Set("ETag", `"`+e.ETag+`"`)
		if len(e.Variants) > 0 {
			h.Add("Vary", "Accept-Encoding")
			if coding := negotiate.Encoding(r.Header.Get("Accept-Encoding")); coding != "" {
				if v, ok := e.Variants[coding]; ok {
					file = name + suffixes[coding]
					h.Set("ETag", `"`+v.ETag+`"`)
					h.Set("Content-Encoding", coding)
				}
			}
		}
	}
	f, err := u.dist.Open(file)
	if err != nil {
		notFound(w)
		return
	}
	defer f.Close()
	rs, ok := f.(io.ReadSeeker)
	if !ok {
		http.Error(w, "could not read the file", http.StatusInternalServerError)
		return
	}
	ct := mime.TypeByExtension(path.Ext(name))
	if ct == "" {
		ct = "application/octet-stream"
	}
	h.Set("Content-Type", ct)
	if strings.HasPrefix(name, "assets/") {
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		h.Set("Cache-Control", "no-cache")
	}
	http.ServeContent(w, r, name, time.Time{}, rs)
}
