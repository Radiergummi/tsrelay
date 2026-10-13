// Package relay serves callback requests and forwards them down tunnels.
package relay

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Radiergummi/tsrelay/internal/audit"
	"github.com/Radiergummi/tsrelay/internal/authz"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/identify"
	"github.com/Radiergummi/tsrelay/internal/ids"
	"github.com/Radiergummi/tsrelay/internal/inspect"
	"github.com/Radiergummi/tsrelay/internal/problem"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// Headers the relay sets for the local application. Incoming headers with
// the Tsrelay- prefix are always removed first, so senders cannot spoof them.
const (
	HeaderSenderNode  = "Tsrelay-Sender-Node"
	HeaderSenderTags  = "Tsrelay-Sender-Tags"
	HeaderSenderLogin = "Tsrelay-Sender-Login"
	HeaderHook        = "Tsrelay-Hook"
	HeaderCallID      = "Tsrelay-Call-Id"
)

// DefaultHoldQueue is how many calls per hook may wait for a tunnel at once.
const DefaultHoldQueue = 100

// OutcomeSenderCanceled is the audit error of a call whose sender went away
// before it completed. Nobody receives it, so it is no problem code.
const OutcomeSenderCanceled = "sender_canceled"

var (
	errHoldTimeout   = errors.New("no tunnel attached within the hold timeout")
	errHoldQueueFull = errors.New("too many calls are waiting for a tunnel")
	errHookExpired   = errors.New("the hook expired while the call was waiting")
)

// Handler serves callbacks. Each call uses the effective settings of its
// hook at the time the call arrives, so updates apply from the next call.
type Handler struct {
	Identifier identify.Identifier
	Hooks      *hooks.Manager
	Tunnels    *tunnel.Registry
	Audit      *audit.Logger
	HoldQueue  int                // calls per hook that may wait for a tunnel; 0 means DefaultHoldQueue
	Inspect    *inspect.Inspector // records calls for the inspector; nil records nothing

	Tracer     trace.Tracer                  // nil: tracing off; no span code runs
	Propagator propagation.TextMapPropagator // nil: don't join senders' traces or pass ours on

	limiters  limiters
	heldMu    sync.Mutex
	held      map[hooks.ID]int
	heldTotal atomic.Int64
}

// Held returns how many calls are currently waiting for a tunnel.
func (h *Handler) Held() int64 { return h.heldTotal.Load() }

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	callID := ids.New("c_")
	w.Header().Set(HeaderCallID, callID)
	inj, _ := r.Context().Value(injectKey{}).(*injection) // nil for sender calls
	ct := h.startCall(r.Context(), r.Header, inj != nil)
	rec := &recorder{ResponseWriter: w}
	parent := r.Context() // ends when the sender goes away
	// Limits cancel the call with a cause that selects the error reported.
	ctx, cancel := context.WithCancelCause(ct.ctx)
	defer cancel(nil)
	r = r.WithContext(ctx)
	body := &countingReader{ReadCloser: r.Body}
	r.Body = body
	var hook hooks.Hook
	var sender authz.Identity
	var waited time.Duration
	kind := inspect.KindSender
	replayOf := ""
	if inj != nil {
		inj.callID, kind, replayOf = callID, inj.kind, inj.replayOf
	}
	var record *callRecord    // set once the call is routed to a hook
	var delivery time.Time    // when delivery began, holding included
	var headersAt time.Time   // when the app's response headers arrived; zero if they never did
	var served *tunnel.Tunnel // the tunnel the call went down
	aborted := false
	defer func() {
		p := recover()
		if p != nil {
			aborted = true // the tunnel failed while streaming the response
		}
		code := rec.Header().Get(problem.HeaderError)
		// A relay-forced abort (idle timeout, an oversized body) sets its own
		// cause directly on ctx, so it is identified before falling back to
		// the sender's own context: SetReadDeadline, used to unblock a
		// stalled read, also cancels ctx's parent as a side effect, and must
		// not be mistaken for the sender leaving.
		switch cause := context.Cause(ctx); {
		case code != "":
		case aborted && cause == errIdleTimeout:
			code = problem.UpstreamTimeout
		case aborted && cause == errBodyTooLarge:
			code = problem.BodyTooLarge
		case parent.Err() != nil:
			code = OutcomeSenderCanceled
		case aborted:
			code = problem.TunnelLost
		}
		attrs := []slog.Attr{
			slog.String("callID", callID), slog.String("kind", string(kind)),
			slog.String("hookID", string(hook.ID)), audit.Actor(sender),
			slog.String("method", r.Method), slog.String("path", r.URL.Path),
			slog.Int("status", rec.status), slog.String("error", code),
			slog.Int64("bytesIn", body.n), slog.Int64("bytesOut", rec.bytes),
			slog.Duration("waited", waited), slog.Duration("duration", time.Since(start)),
		}
		if !headersAt.IsZero() {
			attrs = append(attrs, slog.Duration("ttfb", headersAt.Sub(delivery.Add(waited))))
		}
		h.Audit.Log(r.Context(), audit.Call, attrs...)
		ct.finish(callAttrs{
			callID: callID, kind: string(kind), hookID: string(hook.ID), method: r.Method, path: r.URL.Path,
			code: code, senderNode: sender.NodeName, senderTags: sender.Tags, status: rec.status,
			bytesIn: body.n, bytesOut: rec.bytes, waited: waited,
		})
		record.finish(outcome{
			code: code, status: rec.status, header: rec.Header(),
			start: start, delivery: delivery, wrote: rec.wrote, waited: waited, tunnel: served,
			bytesIn: body.n, bytesOut: rec.bytes,
			bodyRead: body.eof || r.ContentLength == 0,
			whole:    !aborted && parent.Err() == nil,
		})
		if p != nil {
			panic(p)
		}
	}()

	slug, rest, ok := splitRoute(r.URL.Path)
	if !ok {
		problem.New(http.StatusNotFound, problem.HookNotFound, "no hook at this path").Write(rec)
		return
	}
	var err error
	if inj != nil {
		sender = inj.sender // the owner's replay or test: no sender authorization (DESIGN.md §9.3)
	} else {
		at := ct.child(h.Tracer, "tsrelay.authorize")
		sender, err = h.Identifier.Identify(at.ctx, r.RemoteAddr)
		at.end(attribute.KeyValue{}, false)
		if err != nil || !sender.Grants.Has(authz.RoleSender) {
			h.Audit.Log(r.Context(), audit.AuthzDeny, audit.Actor(sender),
				slog.String("action", "call"), slog.String("reason", "no sender role"))
			problem.New(http.StatusForbidden, problem.SenderForbidden, "this node has no sender role on the relay").Write(rec)
			return
		}
	}
	hook, segs, found := h.Hooks.Route(slug, rest)
	if found && encodedSlashIn(r.URL, 1+segs) {
		found = false // the decoded and escaped paths would split differently
	}
	if inj != nil && found && hook.ID != inj.hook {
		found = false // the path now belongs to another hook
	}
	var eff hooks.Effective
	if found {
		// Calls refused by channel are recorded too, so the owner can see
		// them (DESIGN.md §9.2). The sender cannot tell: it gets the same
		// 404 as for a missing hook.
		eff = h.Hooks.Effective(hook)
		record = h.startRecord(r, callID, start, kind, replayOf, hook, sender, 1+segs, eff.Capture)
		ct.event("routed")
		if record != nil {
			body.capture, rec.capture = record.reqBuf, record.respBuf
		}
	}
	if !found || (inj == nil && !sender.Grants.Allows(authz.RoleSender, hook.Channel)) {
		if found {
			ct.event("channel_denied")
			h.Audit.Log(r.Context(), audit.AuthzDeny, audit.Actor(sender), audit.Hook(hook),
				slog.String("action", "call"), slog.String("reason", "channel not allowed"))
			hook = hooks.Hook{} // do not reveal the hook in the audit record either
		}
		problem.New(http.StatusNotFound, problem.HookNotFound, "no hook at this path").Write(rec)
		return
	}
	if eff.MaxBodyBytes > 0 && r.ContentLength > eff.MaxBodyBytes {
		ct.event("body_too_large")
		problem.New(http.StatusRequestEntityTooLarge, problem.BodyTooLarge,
			fmt.Sprintf("the request body is larger than this hook's limit of %d bytes", eff.MaxBodyBytes)).Write(rec)
		return
	}
	if wait, ok := h.limiters.allow(hook.ID, eff.RateLimit, time.Now(), h.hookExists); !ok {
		ct.event("rate_limited")
		rec.Header().Set("Retry-After", retryAfter(wait))
		problem.New(http.StatusTooManyRequests, problem.RateLimited,
			fmt.Sprintf("this hook accepts %g calls per second (burst %d)", eff.RateLimit.PerSecond, eff.RateLimit.Burst)).Write(rec)
		return
	}
	delivery = time.Now()
	tun, attached := h.Tunnels.Lookup(string(hook.ID))
	if !attached {
		// Nothing has been read from the body yet, so holding is safe.
		holdStart := time.Now()
		ht := ct.child(h.Tracer, "tsrelay.hold")
		tun, err = h.hold(ctx, hook, eff.HoldTimeout.Std())
		waited = time.Since(holdStart)
		result := "attached"
		switch {
		case err != nil && ctx.Err() != nil:
			result = "canceled"
		case errors.Is(err, tunnel.ErrHookGone), errors.Is(err, errHookExpired):
			result = "hook_gone"
		case errors.Is(err, errHoldQueueFull):
			result = "queue_full"
		case err != nil:
			result = "timeout"
		}
		ht.end(attribute.String("tsrelay.hold.result", result), true)
		switch {
		case err != nil && ctx.Err() != nil:
			return // the sender went away; nobody is left to answer (as in proxyError)
		case errors.Is(err, tunnel.ErrHookGone), errors.Is(err, errHookExpired):
			problem.New(http.StatusNotFound, problem.HookNotFound, "no hook at this path").Write(rec)
			return
		case errors.Is(err, errHoldQueueFull):
			rec.Header().Set("Retry-After", holdRetryAfter(eff.HoldTimeout.Std()))
			problem.New(http.StatusServiceUnavailable, problem.TunnelUnavailable, "no tunnel is attached to this hook, and too many calls are waiting for one").Write(rec)
			return
		case err != nil:
			rec.Header().Set("Retry-After", holdRetryAfter(eff.HoldTimeout.Std()))
			problem.New(http.StatusServiceUnavailable, problem.TunnelUnavailable, "no tunnel is attached to this hook").Write(rec)
			return
		}
	}

	served = tun
	idle := newIdleWatch(eff.IdleTimeout.Std(), func() {
		cancel(errIdleTimeout)
		// Unblock a pending read from a stalled sender, so the error can be sent.
		_ = http.NewResponseController(rec).SetReadDeadline(time.Now())
	})
	defer idle.stop()
	r.Body = &callBody{ReadCloser: r.Body, max: eff.MaxBodyBytes, idle: idle, cancel: cancel}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			h.rewrite(pr, hook, eff, sender, callID, 1+segs)
		},
		// The relay's call ID replaces any the local app set.
		ModifyResponse: func(resp *http.Response) error {
			resp.Header.Del(HeaderCallID)
			return nil
		},
		Transport: forwardTransport{
			rt: timeoutTransport{rt: tun, timeout: eff.ResponseTimeout.Std()}, headers: &headersAt,
			tracer: h.Tracer, prop: h.Propagator,
		},
		FlushInterval: -1,
		ErrorHandler:  proxyError,
	}
	proxy.ServeHTTP(&idleWriter{ResponseWriter: rec, idle: idle}, r)
}

// hold waits up to timeout for a tunnel to attach to hook, but never past
// the hook's expiry, and only while fewer than HoldQueue calls wait for it.
func (h *Handler) hold(ctx context.Context, hook hooks.Hook, timeout time.Duration) (*tunnel.Tunnel, error) {
	if timeout <= 0 {
		return nil, errHoldTimeout
	}
	if !h.enqueue(hook.ID) {
		return nil, errHoldQueueFull
	}
	defer h.dequeue(hook.ID)
	cause := errHoldTimeout
	if hook.ExpiresAt != nil {
		if left := time.Until(*hook.ExpiresAt); left < timeout {
			timeout, cause = left, errHookExpired
		}
	}
	ctx, cancel := context.WithTimeoutCause(ctx, timeout, cause)
	defer cancel()
	return h.Tunnels.Wait(ctx, string(hook.ID), func() bool { return h.hookExists(hook.ID) })
}

func (h *Handler) enqueue(id hooks.ID) bool {
	h.heldMu.Lock()
	defer h.heldMu.Unlock()
	if h.held[id] >= cmp.Or(h.HoldQueue, DefaultHoldQueue) {
		return false
	}
	if h.held == nil {
		h.held = map[hooks.ID]int{}
	}
	h.held[id]++
	h.heldTotal.Add(1)
	return true
}

func (h *Handler) dequeue(id hooks.ID) {
	h.heldMu.Lock()
	defer h.heldMu.Unlock()
	if h.held[id]--; h.held[id] == 0 {
		delete(h.held, id)
	}
	h.heldTotal.Add(-1)
}

// holdRetryAfter is the Retry-After of tunnel_unavailable: the hold
// timeout rounded up to seconds, but at least 5 seconds.
func holdRetryAfter(hold time.Duration) string { return retryAfter(max(5*time.Second, hold)) }

func (h *Handler) hookExists(id hooks.ID) bool {
	_, ok := h.Hooks.Get(id)
	return ok
}

func (h *Handler) rewrite(pr *httputil.ProxyRequest, hook hooks.Hook, eff hooks.Effective, sender authz.Identity, callID string, segments int) {
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
	// Only the connection may say where a call came from.
	out.Header.Del("X-Real-Ip")
	out.Header.Del("X-Forwarded-Port")
	stripTsrelay(out.Header)
	stripTsrelay(out.Trailer)
	if eff.HostHeader == hooks.HostRewrite {
		out.Header.Set(tunnel.HeaderTunnelHost, "rewrite")
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

// stripTsrelay removes the headers with the Tsrelay- prefix from h.
func stripTsrelay(h http.Header) {
	for k := range h {
		if strings.HasPrefix(http.CanonicalHeaderKey(k), "Tsrelay-") {
			delete(h, k)
		}
	}
}

func proxyError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, tunnel.ErrOverloaded):
		w.Header().Set("Retry-After", "1")
		problem.New(http.StatusServiceUnavailable, problem.TunnelOverloaded, "the tunnel is at its concurrency limit").Write(w)
	case errors.Is(err, errResponseTimeout):
		problem.New(http.StatusGatewayTimeout, problem.UpstreamTimeout, "the local application did not respond in time").Write(w)
	case context.Cause(r.Context()) == errIdleTimeout:
		problem.New(http.StatusGatewayTimeout, problem.UpstreamTimeout, "the request body stalled for longer than the hook's idle timeout").Write(w)
	case context.Cause(r.Context()) == errBodyTooLarge:
		problem.New(http.StatusRequestEntityTooLarge, problem.BodyTooLarge, "the request body is larger than this hook's limit").Write(w)
	case r.Context().Err() != nil:
		// The sender went away; nobody is left to answer.
	default:
		problem.New(http.StatusBadGateway, problem.TunnelLost, "the tunnel was lost while delivering the call").Write(w)
	}
}
