// Package tunnel implements the tsrelay tunnel protocol (DESIGN.md §6): the
// client sends an HTTP/1.1 Upgrade request, after which the roles reverse:
// the relay acts as HTTP/2 client and the developer machine as HTTP/2 server.
package tunnel

import "time"

const (
	UpgradeToken = "tsrelay-tunnel/1"
	// HeaderHook carries the ID of the hook a forwarded request belongs to.
	// Only the relay sets it; the client removes it before forwarding.
	HeaderHook = "Tsrelay-Tunnel-Hook"
	// HeaderControl marks a request as a control message for the client.
	HeaderControl = "Tsrelay-Control"

	DefaultConcurrency = 32
	pingInterval       = 15 * time.Second
)

type ControlType string

const (
	ControlAttached    ControlType = "attached"
	ControlReplaced    ControlType = "replaced"
	ControlHookDeleted ControlType = "hook-deleted"
)

type HookInfo struct {
	ID  string `json:"id"`
	URL string `json:"url,omitempty"`
}

// Attach describes one dial of a tunnel client. Session is fixed for the
// client's lifetime; Resume marks redials after a lost connection. A resume
// may only displace a tunnel of the same session, so a client that was
// replaced cannot take its hooks back by reconnecting.
type Attach struct {
	Session string
	Resume  bool
}

// Control is a message from the relay to the client about the tunnel itself.
type Control struct {
	Type  ControlType `json:"type"`
	Hooks []HookInfo  `json:"hooks,omitempty"`
}
