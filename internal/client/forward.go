package client

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/problem"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
)

// Options configure NewForwarder.
type Options struct {
	// Insecure skips TLS certificate verification for an https target,
	// e.g. a dev server with a self-signed certificate.
	Insecure bool
	// OnUpstreamError receives the full error when the target cannot be
	// reached. The sender only learns that the local app is unreachable.
	OnUpstreamError func(error)
}

// NewForwarder delivers tunnelled requests to target. Bodies pass through
// unchanged in both directions. Host and the relay's X-Forwarded-* headers
// are kept as the relay set them, except that the relay's host marker
// (tunnel.HeaderTunnelHost: rewrite) sets Host to the target's host.
func NewForwarder(target *url.URL, opts Options) http.Handler {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DisableCompression = true
	if opts.Insecure && target.Scheme == "https" {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target) // also clears Out.Host, so Host becomes the target's
			if pr.In.Header.Get(tunnel.HeaderTunnelHost) != string(hooks.HostRewrite) {
				pr.Out.Host = pr.In.Host
			}
			// Rewrite strips these from Out; the relay set them, so keep them.
			for _, h := range []string{"X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto"} {
				if v := pr.In.Header.Values(h); len(v) > 0 {
					pr.Out.Header[h] = v
				}
			}
			pr.Out.Header.Del(tunnel.HeaderHook)
			pr.Out.Header.Del(tunnel.HeaderTunnelHost)
		},
		Transport:     tr,
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if opts.OnUpstreamError != nil {
				opts.OnUpstreamError(fmt.Errorf("could not reach %s: %w", target.Redacted(), err))
			}
			problem.New(http.StatusBadGateway, problem.UpstreamUnreachable, "the local application is not reachable").Write(w)
		},
	}
}

// Call summarizes one forwarded call for the CLI's output.
type Call struct {
	Hook     string // ID of the hook the relay delivered the call for
	Method   string
	Path     string // without the query string, which may carry secrets
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
		log(Call{Hook: r.Header.Get(tunnel.HeaderHook), Method: r.Method, Path: r.URL.Path, Status: sw.status,
			Duration: time.Since(start), Sender: sender})
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
