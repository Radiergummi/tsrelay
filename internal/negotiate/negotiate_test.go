package negotiate

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

const browser = "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"

func TestPick(t *testing.T) {
	all := []Type{JSON, HTML, EventStream}
	for _, tc := range []struct {
		name   string
		path   string
		accept string
		offers []Type
		want   Type
		ok     bool
	}{
		{"no Accept is JSON", "/hooks", "", all, JSON, true},
		{"*/* is JSON", "/hooks", "*/*", all, JSON, true},
		{"a browser gets HTML", "/hooks", browser, all, HTML, true},
		{"JSON by name", "/hooks", "application/json", all, JSON, true},
		{"versioned alias is JSON", "/hooks", "application/vnd.tsrelay.v1+json", all, JSON, true},
		{"q-values decide", "/hooks", "text/html;q=0.5, application/json;q=0.9", all, JSON, true},
		{"text/* is HTML", "/hooks", "text/*", all, HTML, true},
		{"application/* is JSON", "/hooks", "application/*", all, JSON, true},
		{"the stream by name", "/hooks", "text/event-stream", all, EventStream, true},
		{"a wildcard never picks the stream", "/hooks", "text/*", []Type{EventStream}, 0, false},
		{"q=0 refuses", "/hooks", "application/json;q=0", []Type{JSON}, 0, false},
		{"the most specific range wins", "/hooks", "text/html;q=0, */*", []Type{HTML}, 0, false},
		{"nothing acceptable", "/hooks", "image/png", all, 0, false},
		{"no stream offered", "/hooks", "text/event-stream", []Type{JSON, HTML}, 0, false},
		{".json wins over Accept", "/hooks.json", browser, all, JSON, true},
		{".html wins over Accept", "/hooks.html", "application/json", all, HTML, true},
		{"an extension not offered", "/hooks.html", "", []Type{JSON}, HTML, false},
		{"malformed ranges are skipped", "/hooks", "garbage, application/json;q=x, text/html", all, HTML, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got Type
			var ok bool
			var path string
			h := Extensions(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path = r.URL.Path
				got, ok = Pick(w, r, tc.offers...)
			}))
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			if tc.accept != "" {
				req.Header.Set("Accept", tc.accept)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if got != tc.want || ok != tc.ok {
				t.Errorf("Pick = %v, %v; want %v, %v", got, ok, tc.want, tc.ok)
			}
			if path != "/hooks" {
				t.Errorf("path = %q, want the extension stripped", path)
			}
			if v := rec.Header().Get("Vary"); v != "Accept" {
				t.Errorf("Vary = %q, want Accept", v)
			}
		})
	}
}

func TestExtensionsKeepsOtherPaths(t *testing.T) {
	for _, p := range []string{"/", "/assets/index-abc.js", "/hooks/.json", "/openapi.yaml", "/hooks/h_1"} {
		var got string
		h := Extensions(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = r.URL.Path }))
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, p, nil))
		if got != p {
			t.Errorf("%s became %s", p, got)
		}
	}
}
