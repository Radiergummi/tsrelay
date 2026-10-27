package relay

import (
	"cmp"
	"net"
	"net/http"
	"time"

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

// startRecord adds the call, routed to its hook, to the inspector, with
// its redacted request headers. refused marks a call the sender may not
// make on the hook's channel. It returns nil when the handler has no
// inspector.
func (c *call) startRecord(refused bool) *callRecord {
	h, r, hook, sender := c.h, c.r, c.hook, c.sender
	if h.Inspect == nil {
		return nil
	}
	headers, redacted := h.Inspect.Redact(r.Header)
	path, rawPath := remainder(r.URL, 1+c.segs)
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	query, queryCut := h.Inspect.TruncateQuery(r.URL.RawQuery)
	h.Inspect.Add(inspect.Call{
		ID: c.id, HookID: hook.ID, Owner: hook.Owner, Channel: hook.Channel, ReceivedAt: c.start.UTC(), Kind: c.kind, ReplayOf: c.replayOf, Refused: refused,
		Sender: inspect.Sender{Node: sender.NodeName, Tags: sender.Tags, Login: sender.Login, IP: ip, Public: c.internet},
		Method: r.Method, Path: cmp.Or(rawPath, path), Query: query, QueryTruncated: queryCut,
		RequestHeaders: headers, Redacted: redacted,
		Outcome: inspect.OutcomePending, Attempts: []inspect.Attempt{},
	})
	return &callRecord{in: h.Inspect, id: c.id, reqBuf: h.Inspect.NewBuffer(c.eff.Capture), respBuf: h.Inspect.NewBuffer(c.eff.Capture)}
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
			ID: ids.New("a_"), StartedAt: d.delivery.UTC(),
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
