package ui

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

const index = `<!doctype html><title>tsrelay</title><script type="module" src="/assets/index-abc.js"></script>`

var built = fstest.MapFS{
	"index.html":              {Data: []byte(index)},
	"assets/index-abc.js":     {Data: []byte("console.log(1)")},
	"assets/index-abc.js.gz":  {Data: []byte("GZ")},
	"assets/index-abc.js.zst": {Data: []byte("ZST")},
	"theme.js":                {Data: []byte("/* theme */")},
	".gitkeep":                {},
	".vite/manifest.json":     {Data: []byte("{}")},
	"assets-manifest.json": {Data: []byte(`{
		"assets/index-abc.js": {"etag": "id1", "variants": {"gzip": {"etag": "gz1"}, "zstd": {"etag": "zs1"}}},
		"theme.js": {"etag": "th1", "variants": {}}
	}`)},
}

// serve sends one request to the UI's fallback. header
// holds name, value pairs.
func serve(fsys fstest.MapFS, method, path string, header ...string) (*http.Response, string) {
	req := httptest.NewRequest(method, path, nil)
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	rec := httptest.NewRecorder()
	New(fsys).Fallback().ServeHTTP(rec, req)
	resp := rec.Result()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func TestIndexForAppRoutes(t *testing.T) {
	for _, path := range []string{"/", "/index.html", "/hooks/h_1", "/hooks/h_1/calls/c_1", "/admin", "/no/such/page", "/.gitkeep", "/.vite/manifest.json"} {
		resp, body := serve(built, "GET", path, "Accept", "text/html,application/xhtml+xml,*/*;q=0.8")
		if resp.StatusCode != 200 || body != index {
			t.Errorf("%s = %d %q, want index.html", path, resp.StatusCode, body)
		}
		if resp.Header.Get("Cache-Control") != "no-cache" || resp.Header.Get("Content-Type") != "text/html; charset=utf-8" || resp.Header.Get("ETag") == "" {
			t.Errorf("%s headers = %v", path, resp.Header)
		}
	}
	resp, _ := serve(built, "GET", "/hooks", "Accept", "text/html")
	etag := resp.Header.Get("ETag")
	if resp, _ := serve(built, "GET", "/hooks", "Accept", "text/html", "If-None-Match", etag); resp.StatusCode != http.StatusNotModified {
		t.Errorf("revalidation = %d, want 304", resp.StatusCode)
	}
	if resp, body := serve(built, "HEAD", "/hooks", "Accept", "text/html"); resp.StatusCode != 200 || body != "" {
		t.Errorf("HEAD = %d %q", resp.StatusCode, body)
	}
}

func TestUnknownPathsWithoutHTML(t *testing.T) {
	for _, tc := range []struct{ method, path, accept string }{
		{"GET", "/no/such/thing", "application/json"},
		{"GET", "/no/such/thing", "text/html;q=0"},
		{"GET", "/no/such/thing", ""}, // no Accept header means JSON
		{"POST", "/", "text/html"},
		{"DELETE", "/hooks/h_1", "text/html"},
	} {
		resp, _ := serve(built, tc.method, tc.path, "Accept", tc.accept)
		if resp.StatusCode != 404 || resp.Header.Get("Content-Type") != "application/problem+json" {
			t.Errorf("%s %s (Accept %s) = %d %s, want a 404 problem", tc.method, tc.path, tc.accept, resp.StatusCode, resp.Header.Get("Content-Type"))
		}
	}
}

func TestAssets(t *testing.T) {
	for _, tc := range []struct{ accept, body, encoding, etag string }{
		{"", "console.log(1)", "", `"id1"`},
		{"gzip", "GZ", "gzip", `"gz1"`},
		{"gzip, deflate, br, zstd", "ZST", "zstd", `"zs1"`},
		{"zstd;q=0, gzip", "GZ", "gzip", `"gz1"`},
	} {
		resp, body := serve(built, "GET", "/assets/index-abc.js", "Accept-Encoding", tc.accept)
		h := resp.Header
		if resp.StatusCode != 200 || body != tc.body || h.Get("Content-Encoding") != tc.encoding || h.Get("ETag") != tc.etag {
			t.Errorf("Accept-Encoding %q = %d %q %q %q", tc.accept, resp.StatusCode, body, h.Get("Content-Encoding"), h.Get("ETag"))
		}
		if h.Get("Vary") != "Accept-Encoding" || h.Get("Cache-Control") != "public, max-age=31536000, immutable" || !strings.HasPrefix(h.Get("Content-Type"), "text/javascript") {
			t.Errorf("Accept-Encoding %q headers = %v", tc.accept, h)
		}
	}
	if resp, _ := serve(built, "GET", "/assets/index-abc.js", "If-None-Match", `"id1"`); resp.StatusCode != http.StatusNotModified {
		t.Errorf("asset revalidation = %d, want 304", resp.StatusCode)
	}
	if resp, body := serve(built, "GET", "/assets/index-abc.js", "Range", "bytes=0-3"); resp.StatusCode != http.StatusPartialContent || body != "cons" {
		t.Errorf("range = %d %q", resp.StatusCode, body)
	}
	resp, body := serve(built, "GET", "/theme.js")
	if body != "/* theme */" || resp.Header.Get("Cache-Control") != "no-cache" || resp.Header.Get("ETag") != `"th1"` {
		t.Errorf("theme.js = %q %v", body, resp.Header)
	}
}

func TestPlaceholderWithoutBuild(t *testing.T) {
	bare := fstest.MapFS{".gitkeep": {}}
	for _, path := range []string{"/", "/hooks/h_1", "/admin"} {
		resp, body := serve(bare, "GET", path, "Accept", "text/html")
		if resp.StatusCode != 200 || !strings.Contains(body, "The web UI is not built into this binary") || resp.Header.Get("Content-Security-Policy") != CSP {
			t.Errorf("%s = %d %q %v", path, resp.StatusCode, body, resp.Header)
		}
	}
}

func TestSecurityHeaders(t *testing.T) {
	for _, path := range []string{"/", "/assets/index-abc.js", "/theme.js", "/no/such/page"} {
		resp, _ := serve(built, "GET", path)
		h := resp.Header
		if h.Get("Content-Security-Policy") != CSP || h.Get("X-Content-Type-Options") != "nosniff" ||
			h.Get("X-Frame-Options") != "DENY" || h.Get("Referrer-Policy") != "same-origin" {
			t.Errorf("%s: security headers = %v", path, h)
		}
	}
}
