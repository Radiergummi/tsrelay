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

	Forbidden            = "forbidden"
	NotFound             = "not_found"
	Invalid              = "invalid"
	BadRequest           = "bad_request"
	Conflict             = "conflict"
	MisdirectedRequest   = "misdirected_request"
	UnsupportedMediaType = "unsupported_media_type"
	NotAcceptable        = "not_acceptable"
	Internal             = "internal"
)

// Problem is an RFC 9457 problem document.
type Problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail,omitempty"`
	Code   string `json:"code"`
	Errors any    `json:"errors,omitempty"`
}

func New(status int, code, detail string) *Problem {
	return &Problem{Type: typeBase + code, Title: http.StatusText(status), Status: status, Detail: detail, Code: code}
}

func (p *Problem) Error() string { return p.Code + ": " + p.Detail }

// Write sends p. Callers set extra headers such as Retry-After beforehand.
func (p *Problem) Write(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/problem+json")
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
