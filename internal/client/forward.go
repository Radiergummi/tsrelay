package client

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"github.com/Radiergummi/tsrelay/internal/problem"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
)

// NewForwarder delivers tunnelled requests to target. Bodies pass through
// unchanged in both directions; Host and the relay's X-Forwarded-* headers
// are kept as the relay set them.
func NewForwarder(target *url.URL) http.Handler {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DisableCompression = true
	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.Host = pr.In.Host
			// Rewrite strips these from Out; the relay set them, so keep them.
			for _, h := range []string{"X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto"} {
				if v := pr.In.Header.Values(h); len(v) > 0 {
					pr.Out.Header[h] = v
				}
			}
			pr.Out.Header.Del(tunnel.HeaderHook)
		},
		Transport:     tr,
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			problem.New(http.StatusBadGateway, problem.UpstreamUnreachable,
				fmt.Sprintf("could not reach %s: %v", target.Redacted(), err)).Write(w)
		},
	}
}

// Call summarizes one forwarded call for the CLI's output.
type Call struct {
	Method   string
	Path     string
	Status   int
	Duration time.Duration
	Sender   string // tags if the sender is tagged, else its node name
}

// LogCalls reports every call handled by next once it completes.
func LogCalls(next http.Handler, log func(Call)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		sender := r.Header.Get("Tsrelay-Sender-Tags")
		if sender == "" {
			sender = r.Header.Get("Tsrelay-Sender-Node")
		}
		log(Call{Method: r.Method, Path: r.URL.RequestURI(), Status: sw.status, Duration: time.Since(start), Sender: sender})
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 && code >= 200 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
