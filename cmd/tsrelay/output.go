package main

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"sync"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Radiergummi/tsrelay/internal/api"
	"github.com/Radiergummi/tsrelay/internal/client"
	"github.com/Radiergummi/tsrelay/internal/problem"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
	"github.com/charmbracelet/x/ansi"
)

// output reports what the tunnel commands (http, attach) do. As text, hook
// URLs go to stdout and everything else to stderr; with --json, every
// event is one JSON value on stdout.
type output struct {
	json           bool
	out, err       stream    // stdout and stderr
	stdout, stderr io.Writer // the raw writers, for a command started after "--"
	now            func() time.Time
	mu             sync.Mutex
	// inspectBase is the relay's URL. Call lines link to the call's
	// inspector page below it. Empty means no links.
	inspectBase string
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
	CallID     string    `json:"callId,omitempty"`
	Inspect    string    `json:"inspect,omitempty"` // the call's inspector link
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

func newOutput(c *cli) *output {
	return &output{
		json: c.json, stdout: c.stdout, stderr: c.stderr, now: time.Now,
		out: newStream(c.stdout, c.noColor, c.noLinks),
		err: newStream(c.stderr, c.noColor, c.noLinks),
	}
}

func (o *output) emit(e jsonEvent) {
	e.Time = o.now().UTC()
	o.mu.Lock()
	defer o.mu.Unlock()
	writeJSON(o.out, e)
}

// line writes text to s as one line, indented when s is fancy.
func (o *output) line(s stream, text string) {
	if s.fancy {
		text = "  " + text
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	fmt.Fprintln(s.w, text)
}

// status writes a status line: a colored dot when fancy, then text.
func (o *output) status(st lipgloss.Style, text string) {
	o.line(o.err, o.err.glyph(o.err.style(st, "●"))+text)
}

// headline names where calls are delivered to; text only.
func (o *output) headline(target string) {
	if o.json {
		return
	}
	o.line(o.err, o.err.style(styleBrand, "tsrelay")+"  forwarding to "+o.err.style(styleDim, target))
}

// url reports a hook's URL. recreated marks a hook this CLI created again
// after the relay had removed it. A hook with its Path known also gets a
// line saying what kind of hook it is.
func (o *output) url(h api.Hook, recreated bool) {
	switch {
	case o.json:
		o.emit(jsonEvent{Event: "url", Hook: h.ID, URL: h.URL, Recreated: recreated})
	case recreated:
		o.status(styleWarn, "the hook was removed while disconnected; recreated at the same URL")
	default:
		o.line(o.out, o.out.style(styleBold, h.URL))
		if h.Path != "" {
			kind := "persistent"
			if h.Ephemeral {
				kind = "ephemeral"
			}
			o.line(o.err, o.err.style(styleDim, fmt.Sprintf("hook %s  %s  channel %s", h.ID, kind, h.Channel)))
		}
	}
}

// onState returns a tunnel.Client.OnState callback that reports the first
// connection, losses and reconnections. hold is how long the relay holds
// calls while the tunnel is down.
func (o *output) onState(target string, hold time.Duration) func(bool, error) {
	restored := false
	return func(connected bool, err error) {
		switch {
		case o.json && connected:
			o.emit(jsonEvent{Event: "connected", Target: target, Restored: restored})
		case o.json:
			o.emit(jsonEvent{Event: "disconnected", Error: errText(err)})
		case connected && restored:
			o.status(styleOK, "reconnected")
		case connected:
			o.status(styleOK, "connected")
		case hold > 0:
			o.status(styleWarn, fmt.Sprintf("tunnel lost (%v), reconnecting… calls are held for %s", err, hold))
		default:
			o.status(styleWarn, fmt.Sprintf("tunnel lost (%v), reconnecting… calls fail until it is back", err))
		}
		restored = restored || connected
	}
}

func (o *output) call(c client.Call) {
	link := ""
	if o.inspectBase != "" && c.CallID != "" {
		link = o.inspectBase + "/inspect/" + url.PathEscape(c.CallID)
	}
	if o.json {
		o.emit(jsonEvent{Event: "call", Hook: c.Hook, Method: c.Method, Path: c.Path, Status: c.Status,
			DurationMS: float64(c.Duration.Microseconds()) / 1000, Sender: c.Sender, CallID: c.CallID, Inspect: link})
		return
	}
	o.line(o.err, o.callLine(o.err, c, link))
}

// callLine renders one call: today's format when plain, aligned columns
// with a clickable call ID when fancy.
func (o *output) callLine(s stream, c client.Call, link string) string {
	ts := o.now().Format("15:04:05")
	dur := c.Duration.Round(time.Millisecond).String()
	if !s.fancy {
		line := fmt.Sprintf("%s %s %s → %d  %s  %s", ts, c.Method, c.Path, c.Status, dur, c.Sender)
		if link != "" {
			line += "  " + link
		}
		return line
	}
	ref := link
	if link != "" && s.links {
		ref = s.link(c.CallID, link) + " ↗"
	}
	const sep = 2
	fixed := 2 + 8 + sep + 6 + sep + sep + 3 + sep + 7 + sep + ansi.StringWidth(c.Sender) + sep + ansi.StringWidth(ref)
	pathW := min(max(s.width-fixed, 10), 40)
	path := ansi.Truncate(c.Path, pathW, "…")
	path += strings.Repeat(" ", pathW-ansi.StringWidth(path))
	return strings.Join([]string{
		s.style(styleDim, ts),
		s.style(styleBold, fmt.Sprintf("%-6s", c.Method)),
		path,
		s.style(statusStyle(c.Status), fmt.Sprintf("%3d", c.Status)),
		s.style(styleDim, fmt.Sprintf("%7s", dur)),
		s.style(styleDim, c.Sender),
		ref,
	}, "  ")
}

func statusStyle(code int) lipgloss.Style {
	switch {
	case code >= 500:
		return styleBad
	case code >= 400:
		return styleWarn
	case code >= 300:
		return styleInfo
	default:
		return styleOK
	}
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
		o.status(styleWarn, fmt.Sprintf(d.text, urlOf(h.ID)))
	}
}

// upstreamError logs why the local app could not be reached; the sender
// only learns that it is unreachable.
func (o *output) upstreamError(err error) { o.status(styleBad, err.Error()) }

// stopped reports why the command ends and returns its exit code.
func (o *output) stopped(reason string, err error, code int) int {
	switch {
	case o.json:
		o.emit(jsonEvent{Event: "stopped", Reason: reason, Error: errText(err), ExitCode: &code})
	case err != nil:
		o.line(o.err, formatError(o.err, err))
	case reason == "no-hooks":
		o.status(styleWarn, "no hooks left to serve; stopping")
	case reason == "command-exited":
		o.status(styleWarn, fmt.Sprintf("the command exited with status %d; stopping", code))
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

// observe forgets every sequence number when c is attached: a new
// connection starts its own sequence, and attached precedes any
// hook-updated on it. A number remembered from before a reconnect (e.g. a
// relay restart, whose counter starts over at 0) must not suppress an
// update that is actually new.
func (s updateSeqs) observe(c tunnel.Control) {
	if c.Type == tunnel.ControlAttached {
		clear(s)
	}
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
