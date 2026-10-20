package relay

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/Radiergummi/tsrelay/internal/access"
	"github.com/Radiergummi/tsrelay/internal/audit"
	"github.com/Radiergummi/tsrelay/internal/authz"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/ids"
	"github.com/Radiergummi/tsrelay/internal/inspect"
	"github.com/Radiergummi/tsrelay/internal/lifecycle"
	"github.com/Radiergummi/tsrelay/internal/problem"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
)

// call is one callback on its way through the relay. ServeHTTP creates it,
// delivers it step by step, and ends it.
type call struct {
	h        *Handler
	r        *http.Request   // the sender's request, with ctx and the counting body
	parent   context.Context // the sender's context; ends when the sender goes away
	ctx      context.Context // the call's: carries its span; limits cancel it with a cause
	cancel   context.CancelCauseFunc
	start    time.Time
	id       string
	kind     inspect.Kind
	replayOf string
	inj      *injection // nil for sender and public calls
	public   bool       // arrived through ServePublic
	internet bool       // the sender, or the replayed call's, is on the internet
	token    string     // a public call's token
	logPath  string
	trace    callTrace
	rec      *recorder
	body     *countingReader
	sender   authz.Identity
	hook     hooks.Hook
	segs     int // path segments below the route's first that belong to the hook
	eff      hooks.Effective
	record   *callRecord    // set once the call is routed to a hook
	delivery time.Time      // when delivery began, holding included; zero if it never did
	headers  time.Time      // when the app's response headers arrived; zero if they never did
	waited   time.Duration  // held for a tunnel
	tunnel   *tunnel.Tunnel // the tunnel the call goes down
	aborted  bool           // the tunnel failed while streaming the response
	bytesIn  int64          // the request body bytes read, as of the end
	bodyEOF  bool           // the request body was read to the end, as of the end
}

// refusal is a step's answer that ends the call before it reaches the app.
type refusal struct {
	status     int
	code       string // a problem code
	detail     string
	retryAfter string // "" means no Retry-After header
}

func (r *refusal) Error() string { return r.code + ": " + r.detail }

// errNoHook is the answer to anything that must look like a missing hook.
var errNoHook = &refusal{status: http.StatusNotFound, code: problem.HookNotFound, detail: "no hook at this path"}

// errSenderGone ends a call whose sender went away: nobody is left to answer.
var errSenderGone = errors.New("the sender went away")

// holdResult is how waiting for a tunnel ended, as the hold span records it.
type holdResult string

const (
	holdAttached  holdResult = "attached"
	holdCanceled  holdResult = "canceled"
	holdHookGone  holdResult = "hook_gone"
	holdQueueFull holdResult = "queue_full"
	holdTimeout   holdResult = "timeout"
)

func (h *Handler) newCall(w http.ResponseWriter, r *http.Request) *call {
	c := &call{h: h, start: time.Now(), id: ids.New("c_"), kind: inspect.KindSender, parent: r.Context()}
	w.Header().Set(HeaderCallID, c.id)
	c.inj, _ = r.Context().Value(injectKey{}).(*injection) // nil for sender calls
	c.public = r.Context().Value(publicKey{}) != nil       // through ServePublic
	c.internet = c.public || c.inj != nil && c.inj.public
	c.logPath = r.URL.Path
	if c.public {
		c.logPath = publicLogPath(r.URL)
	}
	c.trace = h.startCall(r.Context(), r.Header, c.inj != nil || c.public)
	c.rec = &recorder{ResponseWriter: w}
	// Limits cancel the call with a cause that selects the error reported.
	c.ctx, c.cancel = context.WithCancelCause(c.trace.ctx)
	c.r = r.WithContext(c.ctx)
	c.body = &countingReader{ReadCloser: c.r.Body}
	c.r.Body = c.body
	if c.inj != nil {
		c.inj.callID, c.kind, c.replayOf = c.id, c.inj.kind, c.inj.replayOf
	}
	if c.public {
		c.kind = inspect.KindPublic
	}
	return c
}

// deliver takes the call through its checks and, if it passes them all,
// to the app.
func (c *call) deliver() error {
	if err := c.identify(); err != nil {
		return err
	}
	if err := c.route(); err != nil {
		return err
	}
	if err := c.limit(); err != nil {
		return err
	}
	if err := c.attach(); err != nil {
		return err
	}
	if err := c.admit(); err != nil {
		return err
	}
	c.forward()
	return nil
}

// fail answers the sender with the error that ended the call.
func (c *call) fail(err error) {
	rf, ok := err.(*refusal)
	if !ok {
		return // errSenderGone: nobody is left to answer
	}
	if rf.retryAfter != "" {
		c.rec.Header().Set("Retry-After", rf.retryAfter)
	}
	problem.New(rf.status, rf.code, rf.detail).Write(c.rec)
}

// identify checks the call's address and who sent it. An internet client
// has no tailnet identity: its sender stays empty, and the hook's token is
// all that routes the call.
func (c *call) identify() error {
	if c.public {
		token, ok := splitPublicRoute(c.r.URL.Path)
		if !ok {
			return errNoHook
		}
		c.token = token
		return nil
	}
	if _, _, ok := splitRoute(c.r.URL.Path); !ok {
		return errNoHook
	}
	if c.inj != nil {
		c.sender = c.inj.sender // the owner's replay or test: no sender authorization (DESIGN.md §9.3)
		return nil
	}
	at := c.trace.child(c.h.Tracer, "tsrelay.authorize")
	sender, err := c.h.Identifier.Identify(at.ctx, c.r.RemoteAddr)
	at.end(attribute.KeyValue{}, false)
	c.sender = sender
	if err != nil || !access.MaySend(sender) {
		c.h.Audit.Log(c.r.Context(), audit.AuthzDeny, audit.Actor(sender),
			slog.String("action", "call"), slog.String("reason", "no sender role"))
		return &refusal{status: http.StatusForbidden, code: problem.SenderForbidden, detail: "this node has no sender role on the relay"}
	}
	return nil
}

// route finds the call's hook and checks that the sender may call it.
func (c *call) route() error {
	hook, segs, found := c.h.reroute(c.r, c.token)
	if c.inj != nil && found && hook.ID != c.inj.hook {
		found = false // the path now belongs to another hook
	}
	c.hook, c.segs = hook, segs
	refused := found && c.inj == nil && !c.public && !access.Send(c.sender, hook.Access())
	if found {
		// Calls refused by channel are recorded too, so the owner can see
		// them (DESIGN.md §9.2), but marked, so they cannot crowd out
		// delivered ones. The sender cannot tell: it gets the same 404 as
		// for a missing hook.
		c.eff = c.h.effective(hook, c.public)
		c.record = c.h.startRecord(c.r, c.id, c.start, c.kind, c.replayOf, hook, c.sender, refused, c.internet, 1+segs, c.eff.Capture)
		c.trace.event("routed")
		if c.record != nil {
			c.body.capture, c.rec.capture = c.record.reqBuf, c.record.respBuf
		}
	}
	if !found || refused {
		if refused {
			c.trace.event("channel_denied")
			c.h.Audit.Log(c.r.Context(), audit.AuthzDeny, audit.Actor(c.sender), audit.Hook(hook),
				slog.String("action", "call"), slog.String("reason", "channel not allowed"))
			c.hook = hooks.Hook{} // do not reveal the hook in the audit record either
		}
		return errNoHook
	}
	return nil
}

// limit applies the hook's body size and rate limits.
func (c *call) limit() error {
	if c.eff.MaxBodyBytes > 0 && c.r.ContentLength > c.eff.MaxBodyBytes {
		c.trace.event("body_too_large")
		return &refusal{status: http.StatusRequestEntityTooLarge, code: problem.BodyTooLarge,
			detail: fmt.Sprintf("the request body is larger than this hook's limit of %d bytes", c.eff.MaxBodyBytes)}
	}
	if wait, ok := c.h.limiters.allow(c.hook.ID, c.eff.RateLimit, time.Now(), c.h.hookExists); !ok {
		c.trace.event("rate_limited")
		return &refusal{status: http.StatusTooManyRequests, code: problem.RateLimited, retryAfter: retryAfter(wait),
			detail: fmt.Sprintf("this hook accepts %g calls per second (burst %d)", c.eff.RateLimit.PerSecond, c.eff.RateLimit.Burst)}
	}
	return nil
}

// attach finds the tunnel attached to the hook, holding the call until
// one attaches if none is.
func (c *call) attach() error {
	c.delivery = time.Now()
	if tun, attached := c.h.Tunnels.Lookup(c.hook.ID); attached {
		c.tunnel = tun
		return nil
	}
	// Nothing has been read from the body yet, so holding is safe.
	if err := c.hold(); err != nil {
		return err
	}
	return c.recheck()
}

// tunnelUnavailable is the answer when no tunnel is attached to the hook.
func (c *call) tunnelUnavailable(detail string) *refusal {
	return &refusal{status: problem.StatusTunnelUnavailable, code: problem.TunnelUnavailable, detail: detail,
		retryAfter: holdRetryAfter(c.eff.HoldTimeout.Std())}
}

// hold waits for a tunnel to attach to the hook.
func (c *call) hold() error {
	holdStart := time.Now()
	ht := c.trace.child(c.h.Tracer, "tsrelay.hold")
	_, err := c.h.hold(c.ctx, c.hook, c.eff.HoldTimeout.Std())
	c.waited = time.Since(holdStart)
	result := holdAttached
	switch {
	case err != nil && c.ctx.Err() != nil:
		result = holdCanceled
	case errors.Is(err, lifecycle.ErrHookGone), errors.Is(err, errHookExpired):
		result = holdHookGone
	case errors.Is(err, errHoldQueueFull):
		result = holdQueueFull
	case err != nil:
		result = holdTimeout
	}
	ht.end(attribute.String("tsrelay.hold.result", string(result)), true)
	switch result {
	case holdCanceled:
		return errSenderGone // as in proxyError
	case holdHookGone:
		return errNoHook
	case holdQueueFull:
		return c.tunnelUnavailable("no tunnel is attached to this hook, and too many calls are waiting for one")
	case holdTimeout:
		return c.tunnelUnavailable("no tunnel is attached to this hook")
	}
	return nil
}

// recheck gives a held call the answer it would get if it arrived now: its
// sender is identified again, and its address must still route to the
// same hook, whose channel that sender must still be allowed. The hook is
// routed after the identification, which may be slow.
func (c *call) recheck() error {
	again, idErr := c.sender, error(nil)
	if c.inj == nil && !c.public {
		ictx, cancel := context.WithTimeout(c.ctx, reidentifyTimeout)
		again, idErr = c.h.Identifier.Identify(ictx, c.r.RemoteAddr)
		cancel()
		if idErr != nil && c.ctx.Err() != nil {
			return errSenderGone // as in the hold's canceled case
		}
	}
	now, nowSegs, ok := c.h.reroute(c.r, c.token)
	reason := "the hook changed while the call was held"
	if ok && now.ID == c.hook.ID && c.inj == nil && !c.public {
		if idErr != nil || !access.Send(again, now.Access()) {
			ok, reason = false, "the sender lost access while the call was held"
		} else {
			c.sender = again
		}
	}
	if !ok || now.ID != c.hook.ID {
		c.delivery = time.Time{}
		c.h.Audit.Log(c.r.Context(), audit.AuthzDeny, audit.Actor(c.sender), audit.Hook(c.hook),
			slog.String("action", "call"), slog.String("reason", reason))
		return errNoHook
	}
	// The hook may have changed while still routing the call: forward it
	// with the hook's current path and settings.
	c.hook, c.segs, c.eff = now, nowSegs, c.h.effective(now, c.public)
	// The tunnel may have been revoked or replaced meanwhile.
	tun, attached := c.h.Tunnels.Lookup(c.hook.ID)
	if !attached {
		return c.tunnelUnavailable("no tunnel is attached to this hook")
	}
	c.tunnel = tun
	return nil
}

// admit checks that the tunnel may receive the call.
func (c *call) admit() error {
	if err := access.Serve(c.tunnel.Identity(), c.hook.Access()); err != nil {
		// A channel the node may not receive is a revocation in progress.
		// The lifecycle attaches only the owner's tunnels, so another
		// user's is a broken invariant; neither gets the call.
		c.delivery = time.Time{}
		if errors.Is(err, access.ErrNotUser) || errors.Is(err, access.ErrOtherUser) {
			c.h.Audit.Slog().Error("tunnel owner does not own the hook", "hookID", string(c.hook.ID), "tunnel", c.tunnel.ID)
		}
		return errNoHook
	}
	if c.internet && access.ServeInternet(c.tunnel.Identity(), c.hook.Access()) != nil {
		// The owner's grants no longer allow public hooks. The caller gets
		// the same answer as for an unknown token; the owner sees the call.
		c.delivery = time.Time{}
		c.h.Audit.Log(c.r.Context(), audit.AuthzDeny, audit.Hook(c.hook),
			slog.String("action", "call"), slog.String("reason", "the owner may not receive public calls"))
		return errNoHook
	}
	return nil
}

// forward sends the call down its tunnel and streams the app's response
// back, under the hook's idle, body and response limits.
func (c *call) forward() {
	idle := newIdleWatch(c.eff.IdleTimeout.Std(), func() {
		c.cancel(errIdleTimeout)
		// Unblock a pending read from a stalled sender, so the error can be sent.
		_ = http.NewResponseController(c.rec).SetReadDeadline(time.Now())
	})
	defer idle.stop()
	c.r.Body = &callBody{ReadCloser: c.r.Body, max: c.eff.MaxBodyBytes, idle: idle, cancel: c.cancel}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			c.h.rewrite(pr, c.hook, c.eff, c.sender, c.id, 1+c.segs, c.internet)
		},
		// The relay's call ID replaces any the local app set. A Tsrelay-Error
		// the app set is dropped, so its own answers are never taken for the
		// relay's; the one problem the CLI forwarder writes, when the app is
		// unreachable, is kept. Tsrelay- trailers are dropped too, both as
		// announced and once they arrive, which the proxy reads after
		// closing the body.
		ModifyResponse: func(resp *http.Response) error {
			resp.Header.Del(HeaderCallID)
			if resp.StatusCode != problem.StatusUpstreamUnreachable || resp.Header.Get(problem.HeaderError) != problem.UpstreamUnreachable {
				resp.Header.Del(problem.HeaderError)
			}
			stripTsrelay(resp.Trailer)
			resp.Body = trailerStripper{resp.Body, resp}
			return nil
		},
		Transport: forwardTransport{
			rt: timeoutTransport{rt: c.tunnel, timeout: c.eff.ResponseTimeout.Std()}, headers: &c.headers,
			tracer: c.h.Tracer, prop: c.h.Propagator,
		},
		FlushInterval: -1,
		BufferPool:    tunnel.Buffers,
		ErrorHandler:  proxyError,
	}
	proxy.ServeHTTP(&idleWriter{ResponseWriter: c.rec, idle: idle}, c.r)
}

// end records how the call ended. Deferred by ServeHTTP, it also sees a
// panic from a tunnel that failed while streaming the response, and passes
// it on.
func (c *call) end() {
	defer c.cancel(nil)
	p := recover()
	if p != nil {
		c.aborted = true // the tunnel failed while streaming the response
	}
	// One reading of both: the transport may still be reading the body.
	c.bytesIn, c.bodyEOF = c.body.counts()
	c.finish(c.result())
	if p != nil {
		panic(p)
	}
}

// result is how the call ended.
func (c *call) result() Result {
	code := c.rec.Header().Get(problem.HeaderError)
	// A relay-forced abort (idle timeout, an oversized body) sets its own
	// cause directly on ctx, so it is identified before falling back to
	// the sender's own context: SetReadDeadline, used to unblock a
	// stalled read, also cancels the sender's context (parent) as a side
	// effect, and must not be mistaken for the sender leaving.
	switch cause := context.Cause(c.ctx); {
	case code != "":
	case c.aborted && cause == errIdleTimeout:
		code = problem.UpstreamTimeout
	case c.aborted && cause == errBodyTooLarge:
		code = problem.BodyTooLarge
	case c.parent.Err() != nil:
		code = OutcomeSenderCanceled
	case c.aborted:
		code = problem.TunnelLost
	}
	res := Result{
		CallID: c.id, Kind: c.kind, HookID: c.hook.ID, Sender: c.sender,
		Method: c.r.Method, Path: c.logPath, Status: c.rec.status, Code: code,
		BytesIn: c.bytesIn, BytesOut: c.rec.bytes, Waited: c.waited, Duration: time.Since(c.start),
	}
	if c.public {
		res.ClientIP, _, _ = net.SplitHostPort(c.r.RemoteAddr)
	}
	if !c.headers.IsZero() {
		res.TTFB, res.HasTTFB = c.headers.Sub(c.delivery.Add(c.waited)), true
	}
	return res
}

// finish reports res to the audit log, the metrics, the call's span and
// the inspector, in that order.
func (c *call) finish(res Result) {
	attrs := make([]slog.Attr, 0, 13)
	attrs = append(attrs,
		slog.String("callID", res.CallID), slog.String("kind", string(res.Kind)),
		slog.String("hookID", string(res.HookID)), audit.Actor(res.Sender),
		slog.String("method", res.Method), slog.String("path", res.Path),
		slog.Int("status", res.Status), slog.String("error", res.Code),
		slog.Int64("bytesIn", res.BytesIn), slog.Int64("bytesOut", res.BytesOut),
		slog.Duration("waited", res.Waited), slog.Duration("duration", res.Duration),
	)
	if c.public {
		attrs = append(attrs, slog.String("clientIP", res.ClientIP))
	}
	if res.HasTTFB {
		attrs = append(attrs, slog.Duration("ttfb", res.TTFB))
	}
	c.h.Audit.Log(c.r.Context(), audit.Call, attrs...)
	if c.h.OnCall != nil {
		c.h.OnCall(res)
	}
	c.trace.finish(res)
	c.record.finish(res, delivered{
		start: c.start, delivery: c.delivery, wrote: c.rec.wrote, header: c.rec.Header(), tunnel: c.tunnel,
		bodyRead: c.bodyEOF || c.r.ContentLength == 0,
		whole:    !c.aborted && c.parent.Err() == nil,
	})
}
