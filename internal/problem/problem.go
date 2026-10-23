// Package problem writes and reads RFC 9457 problem responses that carry a
// Tsrelay-Error code, so relay errors can be told apart from upstream ones.
package problem

import (
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
)

// HeaderError carries the error code on every relay-originated error response.
const HeaderError = "Tsrelay-Error"

const typeBase = "https://tsrelay.mazetti.me/errors/"

// Error codes. The first group is visible to callback senders (DESIGN.md §7.4).
const (
	SenderForbidden     = "sender_forbidden"
	HookNotFound        = "hook_not_found"
	BodyTooLarge        = "body_too_large"
	RateLimited         = "rate_limited"
	TunnelUnavailable   = "tunnel_unavailable"
	TunnelOverloaded    = "tunnel_overloaded"
	UpstreamUnreachable = "upstream_unreachable"
	TunnelLost          = "tunnel_lost"
	UpstreamTimeout     = "upstream_timeout"
	BodyStalled         = "body_stalled"
	HoldQueueFull       = "hold_queue_full"

	Forbidden            = "forbidden"
	NotIdentified        = "not_identified"
	CrossSite            = "cross_site"
	ChannelForbidden     = "channel_forbidden"
	TunnelLimit          = "tunnel_limit"
	NotFound             = "not_found"
	Invalid              = "invalid"
	UnsupportedField     = "unsupported_field"
	BadRequest           = "bad_request"
	NotPublic            = "not_public"
	Superseded           = "superseded"
	AttachRejected       = "attach_rejected"
	CallPending          = "call_pending"
	CallRefused          = "call_refused"
	NotCaptured          = "not_captured"
	MisdirectedRequest   = "misdirected_request"
	UnsupportedMediaType = "unsupported_media_type"
	NotAcceptable        = "not_acceptable"
	MethodNotAllowed     = "method_not_allowed"
	Internal             = "internal"
)

// Statuses of tunnel and transport failures, in a range of their own so a
// sender can tell them from the receiving app's 502, 503 or 504. Generic
// clients treat unknown 5xx codes as 500 (RFC 9110 §15).
//
// net/http writes "status code 561" as the HTTP/1.1 reason phrase of codes it
// doesn't know, and offers no way to set one short of hijacking the
// connection, so the phrases appear only in problem titles. HTTP/2 carries no
// reason phrase at all.
const (
	StatusTunnelUnavailable   = 561
	StatusTunnelOverloaded    = 562
	StatusTunnelLost          = 563
	StatusUpstreamUnreachable = 564
	StatusUpstreamTimeout     = 565
)

var statusText = map[int]string{
	StatusTunnelUnavailable:   "Tunnel Unavailable",
	StatusTunnelOverloaded:    "Tunnel Overloaded",
	StatusTunnelLost:          "Tunnel Lost",
	StatusUpstreamUnreachable: "Upstream Unreachable",
	StatusUpstreamTimeout:     "Upstream Timeout",
}

// StatusText is http.StatusText, extended by the tunnel statuses.
func StatusText(code int) string {
	if t, ok := statusText[code]; ok {
		return t
	}
	return http.StatusText(code)
}

// Problem is an RFC 9457 problem document.
type Problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail,omitempty"`
	Code   string `json:"code"`
	Errors any    `json:"errors,omitempty"`
	Hook   string `json:"hook,omitempty"` // for a refused tunnel attach, the hook it was refused for
}

func New(status int, code, detail string) *Problem {
	return &Problem{Type: typeBase + code, Title: StatusText(status), Status: status, Detail: detail, Code: code}
}

func (p *Problem) Error() string { return p.Code + ": " + p.Detail }

// Write sends p. Callers set extra headers such as Retry-After beforehand.
func (p *Problem) Write(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set(HeaderError, p.Code)
	w.WriteHeader(p.Status)
	_ = json.NewEncoder(w).Encode(p)
}

// Decode reads an error response. It returns a *Problem if the body is a
// problem document, and a plain error with status and body excerpt otherwise.
// It does not close resp.Body.
func Decode(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if mt, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type")); mt == "application/problem+json" {
		var p Problem
		if err := json.Unmarshal(body, &p); err == nil && p.Code != "" {
			return &p
		}
	}
	return fmt.Errorf("relay returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
}
