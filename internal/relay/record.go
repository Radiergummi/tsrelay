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
// redacted request headers. refused marks a call the sender may not make
// on the hook's channel. internet marks a sender on the internet. segments is how many path segments belong to
// the route, as for remainder. capture is the hook's capture setting. It
// returns nil when the handler has no inspector.
func (h *Handler) startRecord(r *http.Request, callID string, start time.Time, kind inspect.Kind, replayOf string,
	hook hooks.Hook, sender authz.Identity, refused, internet bool, segments int, capture bool) *callRecord {
	if h.Inspect == nil {
		return nil
	}
	headers, redacted := h.Inspect.Redact(r.Header)
	path, rawPath := remainder(r.URL, segments)
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	query, queryCut := h.Inspect.TruncateQuery(r.URL.RawQuery)
	h.Inspect.Add(inspect.Call{
		ID: callID, HookID: hook.ID, Owner: hook.Owner, Channel: hook.Channel, ReceivedAt: start, Kind: kind, ReplayOf: replayOf, Refused: refused,
		Sender: inspect.Sender{Node: sender.NodeName, Tags: sender.Tags, Login: sender.Login, IP: ip, Public: internet},
		Method: r.Method, Path: cmp.Or(rawPath, path), Query: query, QueryTruncated: queryCut,
		RequestHeaders: headers, Redacted: redacted,
		Outcome: inspect.OutcomePending, Attempts: []inspect.Attempt{},
	})
	return &callRecord{in: h.Inspect, id: callID, reqBuf: h.Inspect.NewBuffer(capture), respBuf: h.Inspect.NewBuffer(capture)}
}

// delivered is what only the inspector needs of how a call ended.
type delivered struct {
	delivery time.Time   // when delivery (holding included) began; zero if it never did
	header   http.Header // the response headers as sent
	tunnel   *tunnel.Tunnel
	bodyRead bool // the request body was read to the end
	whole    bool // the response was sent in full
}

// finish records how the call ended.
func (rc *callRecord) finish(res Result, d delivered) {
	if rc == nil {
		return
	}
	now := time.Now()
	rc.in.Update(rc.id, func(c *inspect.Call) {
		c.Outcome = res.Outcome()
		c.Status = res.Status
		c.Duration = inspect.Millis(res.Duration)
		c.RequestBody = rc.reqBuf.Capture(d.bodyRead)
		if d.delivery.IsZero() {
			return // refused before delivery: no attempt
		}
		respHeaders, respRedacted := rc.in.Redact(d.header)
		a := inspect.Attempt{
			ID: ids.New("a_"), StartedAt: d.delivery,
			WaitedFor: inspect.Millis(res.Waited), Duration: inspect.Millis(now.Sub(d.delivery)),
			Status: res.Status, ResponseHeaders: respHeaders, Redacted: respRedacted,
			ResponseBody: rc.respBuf.Capture(d.whole),
			BytesIn:      res.BytesIn, BytesOut: res.BytesOut, Error: res.Code,
		}
		if res.HasTTFB {
			a.TTFB = inspect.Millis(res.TTFB)
		}
		if d.tunnel != nil {
			a.TunnelID, a.Node = d.tunnel.ID, d.tunnel.Owner.NodeName
		}
		c.Attempts = append(c.Attempts, a)
	})
}
