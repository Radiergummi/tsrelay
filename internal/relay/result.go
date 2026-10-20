package relay

import (
	"cmp"
	"time"

	"github.com/Radiergummi/tsrelay/internal/authz"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/inspect"
)

// Result is how a call ended: what its audit record, the metrics, its span
// and the inspector all report.
type Result struct {
	CallID   string
	Kind     inspect.Kind
	HookID   hooks.ID // "" if the call reached no hook
	Sender   authz.Identity
	Method   string
	Path     string // as logged: a public call's token is replaced
	ClientIP string // a public call's internet client; "" otherwise
	Status   int    // 0 if nothing was written
	Code     string // the relay's error code, or OutcomeSenderCanceled; "" means ok

	BytesIn, BytesOut int64
	Waited            time.Duration // held for a tunnel
	Duration          time.Duration // from arrival to the end
	TTFB              time.Duration // from forwarding to the app's response headers; valid if HasTTFB
	HasTTFB           bool
}

// Outcome is the call's outcome label: its Code, or "ok".
func (r Result) Outcome() string { return cmp.Or(r.Code, inspect.OutcomeOK) }
