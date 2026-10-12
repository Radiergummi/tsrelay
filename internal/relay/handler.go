// Package relay serves callback requests and forwards them down tunnels.
package relay

import (
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/Radiergummi/tsrelay/internal/audit"
	"github.com/Radiergummi/tsrelay/internal/authz"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/identify"
	"github.com/Radiergummi/tsrelay/internal/ids"
	"github.com/Radiergummi/tsrelay/internal/problem"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
)

// Headers the relay sets for the local application. Incoming headers with
// the Tsrelay- prefix are always removed first, so senders cannot spoof them.
const (
	HeaderSenderNode  = "Tsrelay-Sender-Node"
	HeaderSenderTags  = "Tsrelay-Sender-Tags"
	HeaderSenderLogin = "Tsrelay-Sender-Login"
	HeaderHook        = "Tsrelay-Hook"
	HeaderCallID      = "Tsrelay-Call-Id"

	DefaultResponseTimeout = 2 * time.Minute
)

type Handler struct {
	Identifier      identify.Identifier
	Hooks           *hooks.Manager
	Tunnels         *tunnel.Registry
	Audit           *audit.Logger
	ResponseTimeout time.Duration
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	callID := ids.New("c_")
	w.Header().Set(HeaderCallID, callID)
	rec := &recorder{ResponseWriter: w}
	body := &countingReader{ReadCloser: r.Body}
	r.Body = body
	var hook hooks.Hook
	var sender authz.Identity
	aborted := false
	defer func() {
		p := recover()
		if p != nil {
			aborted = true // the tunnel failed while streaming the response
		}
		code := rec.Header().Get(problem.HeaderError)
		if aborted && code == "" {
			code = problem.TunnelLost
		}
		h.Audit.Log(r.Context(), audit.Call,
			slog.String("callID", callID), slog.String("kind", "sender"),
			slog.String("hookID", string(hook.ID)), audit.Actor(sender),
			slog.String("method", r.Method), slog.String("path", r.URL.Path),
			slog.Int("status", rec.status), slog.String("error", code),
			slog.Int64("bytesIn", body.n), slog.Int64("bytesOut", rec.bytes),
			slog.Duration("duration", time.Since(start)))
		if p != nil {
			panic(p)
		}
	}()

	slug, rest, ok := splitRoute(r.URL.Path)
	if !ok {
		problem.New(http.StatusNotFound, problem.HookNotFound, "no hook at this path").Write(rec)
		return
	}
	sender, err := h.Identifier.Identify(r.Context(), r.RemoteAddr)
	if err != nil || !sender.Grants.Has(authz.RoleSender) {
		h.Audit.Log(r.Context(), audit.AuthzDeny, audit.Actor(sender),
			slog.String("action", "call"), slog.String("reason", "no sender role"))
		problem.New(http.StatusForbidden, problem.SenderForbidden, "this node has no sender role on the relay").Write(rec)
		return
	}
	hook, segs, found := h.Hooks.Route(slug, rest)
	if found && encodedSlashIn(r.URL, 1+segs) {
		found = false // the decoded and escaped paths would split differently
	}
	if !found || !sender.Grants.Allows(authz.RoleSender, hook.Channel) {
		if found {
			h.Audit.Log(r.Context(), audit.AuthzDeny, audit.Actor(sender), audit.Hook(hook),
				slog.String("action", "call"), slog.String("reason", "channel not allowed"))
			hook = hooks.Hook{} // do not reveal the hook in the call record either
		}
		problem.New(http.StatusNotFound, problem.HookNotFound, "no hook at this path").Write(rec)
		return
	}
	tun, attached := h.Tunnels.Lookup(string(hook.ID))
	if !attached {
		rec.Header().Set("Retry-After", "5")
		problem.New(http.StatusServiceUnavailable, problem.TunnelUnavailable, "no tunnel is attached to this hook").Write(rec)
		return
	}

	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			h.rewrite(pr, hook, sender, callID, 1+segs)
		},
		Transport:     timeoutTransport{rt: tun, timeout: h.responseTimeout()},
		FlushInterval: -1,
		ErrorHandler:  proxyError,
	}
	proxy.ServeHTTP(rec, r)
}

func (h *Handler) rewrite(pr *httputil.ProxyRequest, hook hooks.Hook, sender authz.Identity, callID string, segments int) {
	// Not pr.SetXForwarded: it appends to a sender-supplied X-Forwarded-For,
	// which would let senders spoof it. These come from the connection only.
	out := pr.Out
	if ip, _, err := net.SplitHostPort(pr.In.RemoteAddr); err == nil {
		out.Header.Set("X-Forwarded-For", ip)
	}
	out.Header.Set("X-Forwarded-Host", pr.In.Host)
	proto := "http"
	if pr.In.TLS != nil {
		proto = "https"
	}
	out.Header.Set("X-Forwarded-Proto", proto)
	for k := range out.Header {
		if strings.HasPrefix(http.CanonicalHeaderKey(k), "Tsrelay-") {
			out.Header.Del(k)
		}
	}
	out.Header.Set("X-Forwarded-Prefix", hook.Route())
	out.Header.Set(HeaderSenderNode, sender.NodeName)
	if len(sender.Tags) > 0 {
		out.Header.Set(HeaderSenderTags, strings.Join(sender.Tags, ","))
	}
	if sender.Login != "" {
		out.Header.Set(HeaderSenderLogin, sender.Login)
	}
	out.Header.Set(HeaderHook, hook.Slug+"/"+hook.Path)
	out.Header.Set(HeaderCallID, callID)
	out.Header.Set(tunnel.HeaderHook, string(hook.ID))
	path, rawPath := remainder(pr.In.URL, segments)
	out.URL = &url.URL{Scheme: "http", Host: "tunnel", Path: path, RawPath: rawPath, RawQuery: pr.In.URL.RawQuery}
	out.Host = pr.In.Host
}

func proxyError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, tunnel.ErrOverloaded):
		w.Header().Set("Retry-After", "1")
		problem.New(http.StatusServiceUnavailable, problem.TunnelOverloaded, "the tunnel is at its concurrency limit").Write(w)
	case errors.Is(err, errResponseTimeout):
		problem.New(http.StatusGatewayTimeout, problem.UpstreamTimeout, "the local application did not respond in time").Write(w)
	case r.Context().Err() != nil:
		// The sender went away; nobody is left to answer.
	default:
		problem.New(http.StatusBadGateway, problem.TunnelLost, "the tunnel was lost while delivering the call").Write(w)
	}
}

func (h *Handler) responseTimeout() time.Duration {
	if h.ResponseTimeout > 0 {
		return h.ResponseTimeout
	}
	return DefaultResponseTimeout
}
