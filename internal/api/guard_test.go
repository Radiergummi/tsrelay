package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGuard(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(299) })
	g := Guard([]string{"tsrelay.example.ts.net.", "tsrelay", "100.64.0.1", "fd7a::1"}, ok)
	cases := []struct {
		name, method, host, path, ctype, fetchSite, body string
		want                                             int
	}{
		{"known host", "GET", "tsrelay.example.ts.net", "/", "", "", "", 299},
		{"host with port", "GET", "tsrelay:443", "/", "", "", "", 299},
		{"ipv4", "GET", "100.64.0.1", "/", "", "", "", 299},
		{"ipv6", "GET", "[fd7a::1]:80", "/", "", "", "", 299},
		{"uppercase host", "GET", "TSRELAY", "/", "", "", "", 299},
		{"unknown host", "GET", "evil.example", "/", "", "", "", 421},
		{"rebinding on callbacks", "POST", "evil.example", "/~m/x", "", "", "", 421},
		{"json write", "POST", "tsrelay", "/hooks", "application/json; charset=utf-8", "same-origin", "{}", 299},
		{"form write", "POST", "tsrelay", "/hooks", "application/x-www-form-urlencoded", "", "a=b", 415},
		{"cross-site write", "POST", "tsrelay", "/hooks", "application/json", "cross-site", "{}", 403},
		{"cross-site delete", "DELETE", "tsrelay", "/hooks/h_1", "", "same-site", "", 403},
		{"bodyless delete", "DELETE", "tsrelay", "/hooks/h_1", "application/json", "", "", 299},
		{"bodyless delete without type", "DELETE", "tsrelay", "/hooks/h_1", "", "", "", 415},
		{"bodyless delete same-origin", "DELETE", "tsrelay", "/hooks/h_1", "", "same-origin", "", 299},
		{"bodyless rotate without type", "POST", "tsrelay", "/hooks/x/rotate-public-url", "", "", "", 415},
		{"bodyless rotate with type", "POST", "tsrelay", "/hooks/x/rotate-public-url", "application/json", "", "", 299},
		{"cross-site replay", "POST", "tsrelay", "/hooks/h_1/calls/c_1/replay", "", "cross-site", "", 403},
		{"cross-site write anywhere", "POST", "tsrelay", "/whatever", "application/json", "same-site", "{}", 403},
		{"callbacks accept any type", "POST", "tsrelay", "/~m/x", "text/plain", "cross-site", "x", 299},
		{"public callbacks accept any type", "POST", "tsrelay", "/+token/x", "application/x-www-form-urlencoded", "cross-site", "a=b", 299},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(c.method, "http://placeholder"+c.path, strings.NewReader(c.body))
			req.Host = c.host
			if c.ctype != "" {
				req.Header.Set("Content-Type", c.ctype)
			}
			if c.fetchSite != "" {
				req.Header.Set("Sec-Fetch-Site", c.fetchSite)
			}
			rec := httptest.NewRecorder()
			g.ServeHTTP(rec, req)
			if rec.Code != c.want {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, c.want, rec.Body)
			}
		})
	}
}

func TestNoStore(t *testing.T) {
	e := newEnv(t)
	for _, c := range []struct{ name, path string }{{"json", "/hooks"}, {"problem", "/hooks/nope"}} {
		t.Run(c.name, func(t *testing.T) {
			resp := e.call(moritz, "GET", c.path, nil, nil)
			if got := resp.Header.Get("Cache-Control"); got != "no-store" {
				t.Fatalf("status %d, Cache-Control = %q", resp.StatusCode, got)
			}
		})
	}
}
