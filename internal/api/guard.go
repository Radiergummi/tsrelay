package api

import (
	"mime"
	"net"
	"net/http"
	"strings"

	"github.com/Radiergummi/tsrelay/internal/problem"
)

// Guard protects the relay against DNS rebinding (unknown Host headers) and
// the API against cross-site writes from browsers (DESIGN.md §5.7). Network
// identity works like an ambient cookie, so both checks are required.
func Guard(allowedHosts []string, next http.Handler) http.Handler {
	allowed := map[string]bool{}
	for _, h := range allowedHosts {
		allowed[normalizeHost(h)] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !allowed[normalizeHost(r.Host)] {
			problem.New(http.StatusMisdirectedRequest, problem.MisdirectedRequest, "this relay does not serve host "+r.Host).Write(w)
			return
		}
		// Every write but a callback's is an API write.
		if !strings.HasPrefix(r.URL.Path, "/~") && !safeMethod(r.Method) {
			if s := r.Header.Get("Sec-Fetch-Site"); s != "" && s != "same-origin" && s != "none" {
				problem.New(http.StatusForbidden, problem.Forbidden, "cross-site requests to the API are not allowed").Write(w)
				return
			}
			if r.ContentLength != 0 {
				if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
					problem.New(http.StatusUnsupportedMediaType, problem.UnsupportedMediaType, "API requests must be application/json").Write(w)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func normalizeHost(h string) string {
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	return strings.TrimSuffix(strings.Trim(strings.ToLower(h), "[]"), ".")
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}
