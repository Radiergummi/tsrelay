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
	// HeaderTunnelHost, set to "rewrite", asks the client to set Host to
	// its local target's host. Only the relay sets it; the client removes it.
	HeaderTunnelHost = "Tsrelay-Tunnel-Host"

	DefaultConcurrency = 32
	pingInterval       = 15 * time.Second
	// ControlTimeout bounds how long delivering a control message may take.
	ControlTimeout = 5 * time.Second
	// clientMaxStreams is the CLI's SETTINGS_MAX_CONCURRENT_STREAMS. It is
	// far above any sensible --tunnel-concurrency: the relay's semaphore is
	// the real limit, and control messages, which bypass it, never starve.
	clientMaxStreams = 1000
)

type ControlType string

const (
	ControlAttached    ControlType = "attached"
	ControlReplaced    ControlType = "replaced"
	ControlHookDeleted ControlType = "hook-deleted"
	// ControlHookUpdated carries a hook's URL after an update.
	ControlHookUpdated ControlType = "hook-updated"
	// ControlRevoked means the owner's grants no longer cover the hooks.
	ControlRevoked ControlType = "revoked"
)

type HookInfo struct {
	ID        string `json:"id"`
	URL       string `json:"url,omitempty"`
	Public    bool   `json:"public,omitempty"`
	PublicURL string `json:"publicUrl,omitempty"` // set while the hook is public and the relay serves Funnel
}

// Attach describes one dial of a tunnel client. Session is fixed for the
// client's lifetime; Resume marks redials after a lost connection. A resume
// may only displace a tunnel of the same session, so a client that was
// replaced cannot take its hooks back by reconnecting.
type Attach struct {
	Session string
	Resume  bool
	// Target is the client's local target, reported for display.
	Target string
}

// Control is a message from the relay to the client about the tunnel itself.
type Control struct {
	Type  ControlType `json:"type"`
	Hooks []HookInfo  `json:"hooks,omitempty"`
	// Seq orders hook-updated messages, which can arrive out of order. A
	// client ignores one whose Seq is not above the last it saw for the hook.
	Seq uint64 `json:"seq,omitempty"`
}
