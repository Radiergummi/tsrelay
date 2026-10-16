// Package inspect records calls for the inspector (DESIGN.md §9): their
// metadata, redacted headers and size-limited bodies, kept in memory for
// the most recent calls of each hook.
package inspect

import (
	"net/http"
	"slices"
	"time"

	"github.com/Radiergummi/tsrelay/internal/hooks"
	"tailscale.com/tailcfg"
)

// Kind says where a call came from.
type Kind string

const (
	KindSender Kind = "sender" // a callback from a sender
	KindReplay Kind = "replay" // the owner replayed a captured call
	KindTest   Kind = "test"   // the owner sent a test request
	KindPublic Kind = "public" // a callback from the internet, through Funnel
)

const (
	// OutcomePending marks a call that is still being delivered.
	OutcomePending = "pending"
	// OutcomeOK marks a call delivered to the local app, whatever it answered.
	// Other outcomes are Tsrelay-Error codes or relay.OutcomeSenderCanceled.
	OutcomeOK = "ok"
)

// Capture is one captured body.
type Capture struct {
	Body []byte `json:"body,omitempty"` // the body's first bytes (base64 in JSON)
	Size int64  `json:"size"`           // bytes that passed through
	// Truncated means Body is not the whole body: it was longer than the
	// capture limit, or was not read to the end.
	Truncated bool `json:"truncated,omitempty"`
	// Captured is false when body capture was off for the hook or the relay.
	Captured bool `json:"captured"`
}

// Complete reports whether Body is the whole body, as a replay needs.
func (c Capture) Complete() bool { return !c.Truncated && (c.Captured || c.Size == 0) }

// Sender identifies the node that sent a call.
type Sender struct {
	Node  string   `json:"node,omitempty"`
	Tags  []string `json:"tags,omitempty"`
	Login string   `json:"login,omitempty"`
	IP    string   `json:"ip,omitempty"`
	// Public means the sender is on the internet; only IP is known.
	Public bool `json:"public,omitempty"`
}

// Attempt is one try at delivering a call down a tunnel. v1 makes at most
// one attempt per call; buffered retries (DESIGN.md §17) will add more.
type Attempt struct {
	ID              string         `json:"id"`
	StartedAt       time.Time      `json:"startedAt"`
	TunnelID        string         `json:"tunnelId,omitempty"`
	Node            string         `json:"node,omitempty"` // the tunnel owner's node
	WaitedFor       hooks.Duration `json:"waitedFor"`      // held for a tunnel to attach
	TTFB            hooks.Duration `json:"ttfb"`           // until the response headers
	Duration        hooks.Duration `json:"duration"`
	Status          int            `json:"status,omitempty"`
	ResponseHeaders http.Header    `json:"responseHeaders,omitempty"`
	Redacted        []string       `json:"redacted,omitempty"` // response headers left out
	ResponseBody    Capture        `json:"responseBody"`
	BytesIn         int64          `json:"bytesIn"`
	BytesOut        int64          `json:"bytesOut"`
	Error           string         `json:"error,omitempty"` // Tsrelay-Error code
}

// Call is one request received for a hook (DESIGN.md §9.1).
type Call struct {
	ID         string         `json:"id"`
	HookID     hooks.ID       `json:"hookId"`
	Owner      tailcfg.UserID `json:"-"`
	ReceivedAt time.Time      `json:"receivedAt"`
	Kind       Kind           `json:"kind"`
	ReplayOf   string         `json:"replayOf,omitempty"`
	Sender     Sender         `json:"sender"`
	Method     string         `json:"method"`
	// Path is the escaped path below the hook's route, as the local app
	// sees it, e.g. "/done".
	Path  string `json:"path"`
	Query string `json:"query,omitempty"`
	// QueryTruncated means Query was cut at the capture limit.
	QueryTruncated bool           `json:"queryTruncated,omitempty"`
	RequestHeaders http.Header    `json:"requestHeaders,omitempty"`
	Redacted       []string       `json:"redacted,omitempty"` // request headers left out
	RequestBody    Capture        `json:"requestBody"`
	Outcome        string         `json:"outcome"`
	Status         int            `json:"status,omitempty"` // what the sender received
	Duration       hooks.Duration `json:"duration"`
	Attempts       []Attempt      `json:"attempts"`
}

// Summary is a call without headers and bodies, for lists and events.
type Summary struct {
	ID         string         `json:"id"`
	HookID     hooks.ID       `json:"hookId"`
	ReceivedAt time.Time      `json:"receivedAt"`
	Kind       Kind           `json:"kind"`
	ReplayOf   string         `json:"replayOf,omitempty"`
	Sender     Sender         `json:"sender"`
	Method     string         `json:"method"`
	Path       string         `json:"path"`
	Outcome    string         `json:"outcome"`
	Status     int            `json:"status,omitempty"`
	Duration   hooks.Duration `json:"duration"`
}

func (c *Call) Summary() Summary {
	return Summary{
		ID: c.ID, HookID: c.HookID, ReceivedAt: c.ReceivedAt, Kind: c.Kind, ReplayOf: c.ReplayOf,
		Sender: Sender{Node: c.Sender.Node, Tags: slices.Clone(c.Sender.Tags), Login: c.Sender.Login, IP: c.Sender.IP, Public: c.Sender.Public},
		Method: c.Method, Path: c.Path, Outcome: c.Outcome, Status: c.Status, Duration: c.Duration,
	}
}

// Metadata returns c without what only the hook's owner may see: the query
// string, headers and bodies (DESIGN.md §9.2). Sizes and statuses stay.
func (c Call) Metadata() Call {
	c.Query, c.RequestHeaders, c.Redacted = "", nil, nil
	c.RequestBody.Body = nil
	c.Attempts = slices.Clone(c.Attempts)
	for i := range c.Attempts {
		a := &c.Attempts[i]
		a.ResponseHeaders, a.Redacted, a.ResponseBody.Body = nil, nil, nil
	}
	return c
}

// clone copies c deeply enough that the copy can be changed and read
// without the store's lock. Bodies are never modified, so they are shared.
func (c *Call) clone() Call {
	out := *c
	out.Sender.Tags = slices.Clone(c.Sender.Tags)
	out.RequestHeaders = c.RequestHeaders.Clone()
	out.Redacted = slices.Clone(c.Redacted)
	out.Attempts = slices.Clone(c.Attempts)
	for i, a := range c.Attempts {
		out.Attempts[i].ResponseHeaders = a.ResponseHeaders.Clone()
		out.Attempts[i].Redacted = slices.Clone(a.Redacted)
	}
	return out
}
