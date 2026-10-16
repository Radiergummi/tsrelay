package relay

import (
	"cmp"
	"net"
	"net/http"
	"time"

	"github.com/Radiergummi/tsrelay/internal/authz"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/ids"
	"github.com/Radiergummi/tsrelay/internal/inspect"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
)

// callRecord feeds one call to the inspector. A nil *callRecord records
// nothing.
type callRecord struct {
	in              *inspect.Inspector
	id              string
	reqBuf, respBuf *inspect.Buffer
}

// startRecord adds a call routed to hook to the inspector, with its
// redacted request headers. internet marks a sender on the internet. segments is how many path segments belong to
// the route, as for remainder. capture is the hook's capture setting. It
// returns nil when the handler has no inspector.
func (h *Handler) startRecord(r *http.Request, callID string, start time.Time, kind inspect.Kind, replayOf string,
	hook hooks.Hook, sender authz.Identity, internet bool, segments int, capture bool) *callRecord {
	if h.Inspect == nil {
		return nil
	}
	headers, redacted := h.Inspect.Redact(r.Header)
	path, rawPath := remainder(r.URL, segments)
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	query, queryCut := h.Inspect.TruncateQuery(r.URL.RawQuery)
	h.Inspect.Add(inspect.Call{
		ID: callID, HookID: hook.ID, Owner: hook.Owner, ReceivedAt: start, Kind: kind, ReplayOf: replayOf,
		Sender: inspect.Sender{Node: sender.NodeName, Tags: sender.Tags, Login: sender.Login, IP: ip, Public: internet},
		Method: r.Method, Path: cmp.Or(rawPath, path), Query: query, QueryTruncated: queryCut,
		RequestHeaders: headers, Redacted: redacted,
		Outcome: inspect.OutcomePending, Attempts: []inspect.Attempt{},
	})
	return &callRecord{in: h.Inspect, id: callID, reqBuf: h.Inspect.NewBuffer(capture), respBuf: h.Inspect.NewBuffer(capture)}
}

// outcome is how a call ended, as the handler saw it.
type outcome struct {
	code     string // Tsrelay-Error or OutcomeSenderCanceled; "" means ok
	status   int
	header   http.Header // the response headers as sent
	start    time.Time   // when the call arrived
	delivery time.Time   // when delivery (holding included) began; zero if it never did
	wrote    time.Time   // when the response status was written; zero if never
	waited   time.Duration
	tunnel   *tunnel.Tunnel // the tunnel the call went down, if any
	bytesIn  int64
	bytesOut int64
	bodyRead bool // the request body was read to the end
	whole    bool // the response was sent in full
}

// finish records how the call ended.
func (rc *callRecord) finish(o outcome) {
	if rc == nil {
		return
	}
	now := time.Now()
	rc.in.Update(rc.id, func(c *inspect.Call) {
		c.Outcome = cmp.Or(o.code, inspect.OutcomeOK)
		c.Status = o.status
		c.Duration = hooks.Duration(now.Sub(o.start))
		c.RequestBody = rc.reqBuf.Capture(o.bodyRead)
		if o.delivery.IsZero() {
			return // refused before delivery: no attempt
		}
		respHeaders, respRedacted := rc.in.Redact(o.header)
		a := inspect.Attempt{
			ID: ids.New("a_"), StartedAt: o.delivery,
			WaitedFor: hooks.Duration(o.waited), Duration: hooks.Duration(now.Sub(o.delivery)),
			Status: o.status, ResponseHeaders: respHeaders, Redacted: respRedacted,
			ResponseBody: rc.respBuf.Capture(o.whole),
			BytesIn:      o.bytesIn, BytesOut: o.bytesOut, Error: o.code,
		}
		if forwarded := o.delivery.Add(o.waited); o.wrote.After(forwarded) {
			a.TTFB = hooks.Duration(o.wrote.Sub(forwarded))
		}
		if o.tunnel != nil {
			a.TunnelID, a.Node = o.tunnel.ID, o.tunnel.Owner.NodeName
		}
		c.Attempts = append(c.Attempts, a)
	})
}
