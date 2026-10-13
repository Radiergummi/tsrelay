package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/Radiergummi/tsrelay/internal/api"
	"github.com/Radiergummi/tsrelay/internal/client"
	"github.com/Radiergummi/tsrelay/internal/problem"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
)

// output reports what the tunnel commands (http, attach) do. As text, hook
// URLs go to stdout and everything else to stderr, so $(…) captures just
// the URL. With --json, every event is one JSON object per line on stdout.
type output struct {
	json           bool
	stdout, stderr io.Writer
	now            func() time.Time
	mu             sync.Mutex
}

// jsonEvent is one line of --json output.
type jsonEvent struct {
	Event      string    `json:"event"` // url, connected, disconnected, call or stopped
	Time       time.Time `json:"time"`
	Hook       string    `json:"hook,omitempty"`
	URL        string    `json:"url,omitempty"`
	Recreated  bool      `json:"recreated,omitempty"`
	Target     string    `json:"target,omitempty"`
	Restored   bool      `json:"restored,omitempty"`
	Method     string    `json:"method,omitempty"`
	Path       string    `json:"path,omitempty"`
	Status     int       `json:"status,omitempty"`
	DurationMS float64   `json:"durationMs,omitempty"`
	Sender     string    `json:"sender,omitempty"`
	// Reason says why delivery stopped: for one hook "replaced", "revoked"
	// or "deleted"; for the command "interrupted", "no-hooks", "error" or
	// "command-exited".
	Reason   string `json:"reason,omitempty"`
	Error    string `json:"error,omitempty"`
	ExitCode *int   `json:"exitCode,omitempty"`
}

// drops names the control messages that end a hook's delivery here: the
// --json reason, and the text phrase with %s for the hook's URL.
var drops = map[tunnel.ControlType]struct{ reason, text string }{
	tunnel.ControlReplaced:    {"replaced", "%s is now served by another tsrelay process"},
	tunnel.ControlRevoked:     {"revoked", "your grants no longer allow serving %s"},
	tunnel.ControlHookDeleted: {"deleted", "%s was deleted"},
}

func newOutput(stdout, stderr io.Writer, asJSON bool) *output {
	return &output{json: asJSON, stdout: stdout, stderr: stderr, now: time.Now}
}

func (o *output) emit(e jsonEvent) {
	e.Time = o.now().UTC()
	b, _ := json.Marshal(e)
	o.mu.Lock()
	defer o.mu.Unlock()
	o.stdout.Write(append(b, '\n'))
}

func (o *output) text(w io.Writer, format string, args ...any) {
	o.mu.Lock()
	defer o.mu.Unlock()
	fmt.Fprintf(w, format+"\n", args...)
}

// url reports a hook's URL. recreated marks a hook this CLI created again
// after the relay had removed it.
func (o *output) url(h api.Hook, recreated bool) {
	switch {
	case o.json:
		o.emit(jsonEvent{Event: "url", Hook: h.ID, URL: h.URL, Recreated: recreated})
	case recreated:
		o.text(o.stderr, "tsrelay: the hook was removed while disconnected; recreated at the same URL.")
	default:
		o.text(o.stdout, "%s", h.URL)
	}
}

// onState returns a tunnel.Client.OnState callback that reports the first
// connection, losses and reconnections. hold is how long the relay holds
// calls while the tunnel is down.
func (o *output) onState(urls []string, target string, hold time.Duration) func(bool, error) {
	restored := false
	return func(connected bool, err error) {
		switch {
		case o.json && connected:
			o.emit(jsonEvent{Event: "connected", Target: target, Restored: restored})
		case o.json:
			o.emit(jsonEvent{Event: "disconnected", Error: errText(err)})
		case connected && restored:
			o.text(o.stderr, "Tunnel restored.")
		case connected:
			for _, u := range urls {
				o.text(o.stderr, "Forwarding %s → %s", u, target)
			}
		case hold > 0:
			o.text(o.stderr, "Tunnel lost (%v); reconnecting… calls are held for %s.", err, hold)
		default:
			o.text(o.stderr, "Tunnel lost (%v); reconnecting… calls fail until it is back.", err)
		}
		restored = restored || connected
	}
}

func (o *output) call(c client.Call) {
	if o.json {
		o.emit(jsonEvent{Event: "call", Hook: c.Hook, Method: c.Method, Path: c.Path, Status: c.Status,
			DurationMS: float64(c.Duration.Microseconds()) / 1000, Sender: c.Sender})
		return
	}
	o.text(o.stderr, "%s %s %s → %d  %s  %s", o.now().Format("15:04:05"), c.Method, c.Path, c.Status,
		c.Duration.Round(time.Millisecond), c.Sender)
}

// dropped reports the hooks a control message ends delivery of; urlOf
// names them. Other control messages are ignored.
func (o *output) dropped(c tunnel.Control, urlOf func(hookID string) string) {
	d, ok := drops[c.Type]
	if !ok {
		return
	}
	for _, h := range c.Hooks {
		if o.json {
			o.emit(jsonEvent{Event: "stopped", Hook: h.ID, URL: urlOf(h.ID), Reason: d.reason})
			continue
		}
		o.text(o.stderr, "tsrelay: "+d.text+".", urlOf(h.ID))
	}
}

// upstreamError logs why the local app could not be reached; the sender
// only learns that it is unreachable.
func (o *output) upstreamError(err error) { o.text(o.stderr, "tsrelay: %v", err) }

// stopped reports why the command ends and returns its exit code.
func (o *output) stopped(reason string, err error, code int) int {
	switch {
	case o.json:
		o.emit(jsonEvent{Event: "stopped", Reason: reason, Error: errText(err), ExitCode: &code})
	case err != nil:
		o.text(o.stderr, "tsrelay: %s", errText(err))
	case reason == "no-hooks":
		o.text(o.stderr, "tsrelay: no hooks left to serve; stopping.")
	case reason == "command-exited":
		o.text(o.stderr, "tsrelay: the command exited with status %d; stopping.", code)
	}
	return code
}

// fail reports err as the reason the command stops; the exit code is 1.
func (o *output) fail(err error) int { return o.stopped("error", err, 1) }

// finish reports how tunnel.Client.Run ended and returns the exit code.
func (o *output) finish(err error) int {
	switch {
	case err == nil:
		return o.stopped("interrupted", nil, 0)
	case errors.Is(err, tunnel.ErrNoHooks):
		return o.stopped("no-hooks", nil, 1)
	default:
		return o.fail(err)
	}
}

// updateSeqs remembers the newest hook-updated sequence number per hook,
// because control messages can arrive out of order.
type updateSeqs map[string]uint64

// fresh reports whether an update numbered seq for hook is newer than any
// seen so far, and records it. A relay without sequence numbers sends 0,
// which always counts as fresh.
func (s updateSeqs) fresh(hook string, seq uint64) bool {
	if seq == 0 {
		return true
	}
	if seq <= s[hook] {
		return false
	}
	s[hook] = seq
	return true
}

// errText renders err for people; relay refusals carry the relay's reason.
func errText(err error) string {
	var p *problem.Problem
	switch {
	case err == nil:
		return ""
	case errors.As(err, &p):
		return "the relay refused: " + p.Detail
	default:
		return err.Error()
	}
}
