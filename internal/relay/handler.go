// Package relay serves callback requests and forwards them down tunnels.
package relay

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
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
	"github.com/Radiergummi/tsrelay/internal/inspect"
	"github.com/Radiergummi/tsrelay/internal/lifecycle"
	"github.com/Radiergummi/tsrelay/internal/problem"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// Headers the relay sets for the local application. Incoming headers with
// the Tsrelay- prefix are always removed first, so senders cannot spoof them.
const (
	HeaderSenderNode   = "Tsrelay-Sender-Node"
	HeaderSenderTags   = "Tsrelay-Sender-Tags"
	HeaderSenderLogin  = "Tsrelay-Sender-Login"
	HeaderSenderPublic = "Tsrelay-Sender-Public"
	HeaderHook         = "Tsrelay-Hook"
	HeaderCallID       = "Tsrelay-Call-Id"
)

// DefaultHoldQueue is how many calls per hook may wait for a tunnel at once.
const DefaultHoldQueue = 100

// reidentifyTimeout bounds identifying a held call's sender again.
const reidentifyTimeout = 5 * time.Second

// OutcomeSenderCanceled is the audit error of a call whose sender went away
// before it completed. Nobody receives it, so it is no problem code.
const OutcomeSenderCanceled = "sender_canceled"

var (
	errHoldTimeout   = errors.New("no tunnel attached within the hold timeout")
	errHoldQueueFull = errors.New("too many calls are waiting for a tunnel")
	errHookExpired   = errors.New("the hook expired while the call was waiting")
	errShuttingDown  = errors.New("the relay is shutting down")
)

// Handler serves callbacks. Each call uses the effective settings of its
// hook at the time the call arrives, so updates apply from the next call.
type Handler struct {
	Identifier identify.Identifier
	Hooks      *hooks.Manager
	Tunnels    *lifecycle.Lifecycle
	Audit      *audit.Logger
	HoldQueue  int                // calls per hook that may wait for a tunnel; 0 means DefaultHoldQueue
	Inspect    *inspect.Inspector // records calls for the inspector; nil records nothing

	PublicMaxBody int64 // body cap of public calls; 0: only the hook's own limit

	// OnCall, if set, is called with every call's result, after its audit
	// record: the relay's call metrics.
	OnCall func(Result)

	Tracer     trace.Tracer                  // nil: tracing off; no span code runs
	Propagator propagation.TextMapPropagator // nil: don't join senders' traces or pass ours on

	limiters  limiters
	drainOnce sync.Once
	draining  context.Context // done once Drain was called
	drained   context.CancelFunc
	heldMu    sync.Mutex
	held      map[hooks.ID]int
	heldTotal atomic.Int64
}

// Held returns how many calls are currently waiting for a tunnel.
func (h *Handler) Held() int64 { return h.heldTotal.Load() }

type publicKey struct{}

// ServePublic serves a call that arrived from the internet through Funnel
// at /+{token}/…: no sender is identified, and the hook's token alone
// routes the call. r.RemoteAddr must be the internet client's address.
func (h *Handler) ServePublic(w http.ResponseWriter, r *http.Request) {
	h.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), publicKey{}, true)))
}

// maxPathBytes caps a call's escaped path, as servers cap a request line,
// so a sender can't make the inspector, the audit log and the call events
// keep megabytes per call.
const maxPathBytes = 8 << 10

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.EscapedPath()) > maxPathBytes {
		problem.New(http.StatusRequestURITooLong, problem.PathTooLong,
			fmt.Sprintf("the path is longer than %d bytes", maxPathBytes)).Write(w)
		return
	}
	c := h.newCall(w, r)
	defer c.end()
	if err := c.deliver(); err != nil {
		c.fail(err)
	}
}

// Drain ends every hold and refuses new ones, for a relay shutting down:
// no tunnel can attach any more, so a held call is better answered now,
// with a Retry-After that reaches the restarted relay. Calls already
// delivered are not affected.
func (h *Handler) Drain() { h.drainCtx(); h.drained() }

// drainCtx is done once Drain was called.
func (h *Handler) drainCtx() context.Context {
	h.drainOnce.Do(func() { h.draining, h.drained = context.WithCancel(context.Background()) })
	return h.draining
}

// hold waits up to timeout for a tunnel to attach to hook, but never past
// the hook's expiry, and only while fewer than HoldQueue calls wait for it.
func (h *Handler) hold(ctx context.Context, hook hooks.Hook, timeout time.Duration) error {
	drain := h.drainCtx()
	if drain.Err() != nil {
		return errShuttingDown
	}
	if timeout <= 0 {
		return errHoldTimeout
	}
	if !h.enqueue(hook.ID) {
		return errHoldQueueFull
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
	ctx, stop := context.WithCancelCause(ctx)
	defer context.AfterFunc(drain, func() { stop(errShuttingDown) })()
	_, err := h.Tunnels.Wait(ctx, hook.ID)
	return err
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

// holdRetryAfter is the Retry-After of tunnel_unavailable and hold_queue_full: the hold
// timeout rounded up to seconds, but at least 5 seconds.
func holdRetryAfter(hold time.Duration) string { return retryAfter(max(5*time.Second, hold)) }

// reroute routes r's original address: a public call's token, or a
// tailnet call's slug and path. It returns the hook and how many path
// segments below the route's first belong to it. A path whose escaped form
// would split into other segments than its decoded one routes nowhere.
func (h *Handler) reroute(r *http.Request, token string) (hooks.Hook, int, bool) {
	if token != "" {
		hook, ok := h.Hooks.RoutePublic(token)
		return hook, 0, ok && !encodedSlashIn(r.URL, 1)
	}
	slug, rest, ok := splitRoute(r.URL.Path)
	if !ok {
		return hooks.Hook{}, 0, false
	}
	hook, segs, ok := h.Hooks.Route(slug, rest)
	return hook, segs, ok && !encodedSlashIn(r.URL, 1+segs)
}

// effective is hook's effective settings for a call, with a public call's
// body capped at PublicMaxBody.
func (h *Handler) effective(hook hooks.Hook, public bool) hooks.Effective {
	eff := h.Hooks.Effective(hook)
	if public && h.PublicMaxBody > 0 && (eff.MaxBodyBytes == 0 || eff.MaxBodyBytes > h.PublicMaxBody) {
		eff.MaxBodyBytes = h.PublicMaxBody
	}
	return eff
}

func (h *Handler) hookExists(id hooks.ID) bool {
	_, ok := h.Hooks.Get(id)
	return ok
}

func (h *Handler) rewrite(pr *httputil.ProxyRequest, hook hooks.Hook, eff hooks.Effective, sender authz.Identity, callID string, segments int, internet bool) {
	// Not pr.SetXForwarded: it appends to a sender-supplied X-Forwarded-For,
	// which would let senders spoof it. These come from the connection only.
	out := pr.Out
	for k := range out.Header {
		// Some frameworks read "_" in a header name as "-" (CGI, WSGI,
		// Rack), so X_Forwarded_For would pass for X-Forwarded-For there.
		name := http.CanonicalHeaderKey(strings.ReplaceAll(k, "_", "-"))
		if strings.HasPrefix(name, "X-Forwarded-") || strings.HasPrefix(name, "Tsrelay-") || name == "X-Real-Ip" || name == "Forwarded" {
			delete(out.Header, k)
		}
	}
	if internet {
		// An internet client may not supply trace context either.
		out.Header.Del("Traceparent")
		out.Header.Del("Tracestate")
		out.Header.Del("Baggage")
	}
	if ip, _, err := net.SplitHostPort(pr.In.RemoteAddr); err == nil {
		out.Header.Set("X-Forwarded-For", ip)
	}
	out.Header.Set("X-Forwarded-Host", pr.In.Host)
	proto := "http"
	if pr.In.TLS != nil {
		proto = "https"
	}
	out.Header.Set("X-Forwarded-Proto", proto)
	// Only the connection may say where a call came from: every
	// X-Forwarded-* a sender supplied is gone above.
	out.Header.Del("X-Real-Ip")
	out.Header.Del("Forwarded")
	stripTsrelay(out.Header)
	stripTsrelay(out.Trailer)
	if eff.HostHeader == hooks.HostRewrite {
		out.Header.Set(tunnel.HeaderTunnelHost, "rewrite")
	}
	prefix := hook.Route()
	if internet && hook.Public {
		// A replayed public call names the public route while the hook
		// has one, as the original did.
		prefix = hook.PublicRoute()
	}
	out.Header.Set("X-Forwarded-Prefix", prefix)
	if internet {
		out.Header.Set(HeaderSenderPublic, "true")
	} else {
		out.Header.Set(HeaderSenderNode, sender.NodeName)
		if len(sender.Tags) > 0 {
			out.Header.Set(HeaderSenderTags, strings.Join(sender.Tags, ","))
		}
		if sender.Login != "" {
			out.Header.Set(HeaderSenderLogin, sender.Login)
		}
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

// trailerStripper drops the Tsrelay- trailers of resp when its body is
// closed, after the transport has filled them in.
type trailerStripper struct {
	io.ReadCloser
	resp *http.Response
}

func (b trailerStripper) Close() error {
	err := b.ReadCloser.Close()
	stripTsrelay(b.resp.Trailer)
	return err
}
