package main

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strings"

	"github.com/Radiergummi/tsrelay/internal/client"
	"github.com/Radiergummi/tsrelay/internal/problem"
)

// explanation says what a relay problem means and what to do about it. In
// fixes, <hook> stands for the hook the command named, where it is known.
type explanation struct {
	title, reason string
	fixes         []string
}

// updateFix installs the relay's own version of tsrelay, as updateCommand
// once the relay's URL is known; a request the relay cannot read usually
// means the two differ.
const updateFix = "Install the relay's version of tsrelay; its web UI shows how"

// problemExplanations explain each problem code the relay returns. The
// relay's detail is added to the reason.
var problemExplanations = map[string]explanation{
	problem.SenderForbidden: {"Not allowed to send calls", "Calling a hook needs the sender role on the relay.",
		[]string{"Ask a relay admin to grant this device the sender role"}},
	problem.HookNotFound: {"No such hook", "No hook answers at this path, or it is in a channel you can't use.",
		[]string{"tsrelay hooks list"}},
	problem.BodyTooLarge: {"The request body is too large", "It is larger than the hook's body limit.",
		[]string{"tsrelay hooks update <hook> --max-body-bytes 10485760"}},
	problem.PathTooLong: {"The path is too long", "The relay refuses paths longer than 8 KiB.",
		[]string{"Send long data in the body"}},
	problem.ShuttingDown: {"The relay is restarting", "It shut down while the call waited for a tunnel.",
		[]string{"Retry in a few seconds"}},
	problem.RateLimited: {"Too many calls", "The hook's rate limit was exceeded.",
		[]string{"Wait a moment, then retry", "tsrelay hooks update <hook> --rate-limit 100/200"}},
	problem.TunnelUnavailable: {"No tunnel is serving the hook", "No tsrelay process attached to the hook within its hold timeout.",
		[]string{"tsrelay attach <hook> --to 3000", "tsrelay hooks update <hook> --hold-timeout 1m"}},
	problem.HoldQueueFull: {"Too many calls are waiting for a tunnel", "No tsrelay process is attached to the hook, and the hold queue for it is full.",
		[]string{"tsrelay attach <hook> --to 3000", "Retry in a moment"}},
	problem.TunnelOverloaded: {"The tunnel is at its concurrency limit", "Too many calls are in flight on it at once.",
		[]string{"Retry in a moment"}},
	problem.TunnelLost: {"The tunnel dropped mid-call", "The tsrelay process serving the hook disconnected while delivering the call.",
		[]string{"Check that tsrelay still runs; it reconnects on its own"}},
	problem.UpstreamUnreachable: {"Your local app isn't reachable", "The tunnel is up, but nothing answered at its target.",
		[]string{"Start your app, or check the port or URL tsrelay forwards to"}},
	problem.UpstreamTimeout: {"Your local app didn't answer in time", "It took longer than the hook's response timeout, or its response stalled past the idle timeout.",
		[]string{"tsrelay hooks update <hook> --response-timeout 1m"}},
	problem.BodyStalled: {"The request body stalled", "The sender paused the body for longer than the hook's idle timeout.",
		[]string{"tsrelay hooks update <hook> --idle-timeout 1m"}},

	problem.Forbidden: {"The relay refused", "Your grants don't allow this.",
		[]string{"Ask a relay admin to grant you the role or channel; the relay's web UI shows them the grant"}},
	problem.NotIdentified: {"The relay couldn't identify you", "It asks Tailscale who is calling, and Tailscale didn't know this device.",
		[]string{"tailscale status", "Reach the relay over Tailscale, not another network"}},
	problem.CrossSite: {"The relay refused a cross-site request", "API writes must come from the relay's own pages or from a tool, not from another site.",
		[]string{"Send the request from the CLI, or from the relay's web UI"}},
	problem.ChannelForbidden: {"Your grants don't cover the hook's channel", "The hook is in a channel your grants no longer match.",
		[]string{"Ask a relay admin to grant you the channel; the relay's web UI shows them the grant"}},
	problem.StreamLimit: {"Too many event streams", "This device already has as many event streams open as the relay allows one device.",
		[]string{"Close a browser tab or a client that watches the relay"}},
	problem.TunnelLimit: {"Too many tunnels", "You already have as many tunnels attached as your grants allow.",
		[]string{"tsrelay ps", "Stop a tunnel you no longer need, or ask a relay admin to raise your limit"}},
	problem.NotFound: {"Not found", "The relay has no such resource for you.",
		[]string{"tsrelay hooks list"}},
	problem.Invalid: {"The relay rejected the values", "Something sent is not valid.",
		[]string{"Correct what the relay named, then retry"}},
	problem.UnsupportedField: {"The relay doesn't support this", "The request names a field this relay doesn't know, so tsrelay is newer than the relay.",
		[]string{"Ask the relay's operator to upgrade the relay"}},
	problem.BadRequest: {"The relay couldn't read the request", "This tsrelay may not speak the relay's API.",
		[]string{updateFix}},
	problem.NotPublic: {"The hook isn't public", "Only a public hook has a public URL to rotate.",
		[]string{"tsrelay hooks update <hook> --public"}},
	problem.Superseded: {"Another tunnel took the hook over", "A newer tunnel replaced this one, and a resumed tunnel may not take its hook back.",
		[]string{"Stop the other tunnel, or attach again to take the hook over"}},
	problem.AttachRejected: {"The hook already has a tunnel", "Its attach policy is reject, so a second tunnel is refused.",
		[]string{"Stop the tunnel that serves the hook", "tsrelay hooks update <hook> --attach-policy takeover"}},
	problem.CallPending: {"The call is still being delivered", "A call can't be replayed before it has an outcome.",
		[]string{"Retry once the call has finished"}},
	problem.CallRefused: {"The call was never delivered", "The relay refused it by channel, so there is nothing to replay.",
		[]string{"Send a new test request instead"}},
	problem.NotCaptured: {"The call wasn't captured in full", "Replay needs the call's body and query string complete, and the hook captured less.",
		[]string{"tsrelay hooks update <hook> --capture"}},
	problem.MisdirectedRequest: {"Wrong host for the relay", "The relay doesn't answer to the host name tsrelay used.",
		[]string{"Check --relay and TSRELAY_RELAY"}},
	problem.UnsupportedMediaType: {"The relay rejected the request's format", "This tsrelay may not speak the relay's API.",
		[]string{updateFix}},
	problem.NotAcceptable: {"The relay can't answer in JSON", "This tsrelay may not speak the relay's API.",
		[]string{updateFix}},
	problem.MethodNotAllowed: {"The relay doesn't do that here", "This tsrelay may not speak the relay's API.",
		[]string{updateFix}},
	problem.Internal: {"The relay failed", "Something went wrong on the relay itself.",
		[]string{"Retry; if it keeps failing, ask the relay's operator to check its logs"}},
}

// versionSeen is a relay version that differs from this CLI's, and how.
type versionSeen struct {
	relay string
	skew  skew
}

// explain turns what a command returned into an explained error.
func (c *cli) explain(err error) *cliError {
	var ce *cliError
	var p *problem.Problem
	var ue *client.UnreachableError
	switch {
	case errors.As(err, &ce):
		return ce
	case errors.As(err, &ue):
		return c.explainUnreachable(ue)
	case errors.As(err, &p):
		ce = c.explainProblem(p)
	default:
		ce = &cliError{Title: capitalize(err.Error()), Code: "error", err: err}
	}
	if v := c.version.Load(); v != nil && v.skew == skewMajor {
		ce.Reason = strings.TrimSpace(ce.Reason + fmt.Sprintf(" This tsrelay (%s) differs from the relay (%s), which may be why.", version, v.relay))
		if update := c.updateCommand(); !slices.Contains(ce.Fixes, update) {
			ce.Fixes = append(ce.Fixes, update)
		}
	}
	return ce
}

// explainProblem explains a relay refusal; the relay's detail says what
// exactly went wrong.
func (c *cli) explainProblem(p *problem.Problem) *cliError {
	x, ok := problemExplanations[p.Code]
	if !ok {
		x = explanation{title: p.Title}
	}
	if p.Detail != "" && p.Code != problem.NotIdentified {
		x.reason = strings.TrimSpace(x.reason + " The relay says: " + sentence(p.Detail))
	}
	e := &cliError{Title: x.title, Reason: x.reason, Code: p.Code, err: p}
	for _, f := range x.fixes {
		if f == updateFix && c.base != "" {
			f = c.updateCommand()
		}
		if c.hook != "" {
			f = strings.ReplaceAll(f, "<hook>", c.hook)
		}
		e.Fixes = append(e.Fixes, f)
	}
	return e
}

// explainUnreachable explains why the relay could not be reached, naming
// the URL tried and where it came from.
func (c *cli) explainUnreachable(ue *client.UnreachableError) *cliError {
	from := c.relayFrom
	if from == "" {
		from = "your tailnet's MagicDNS name"
	}
	cmdPath := c.cmdPath
	if cmdPath == "" {
		cmdPath = "tsrelay"
	}
	relayFix := cmdPath + " --relay https://tsrelay.<your tailnet>.ts.net"
	cause := ue.Err
	var ve *url.Error
	if errors.As(cause, &ve) {
		cause = ve.Err
	}
	var dns *net.DNSError
	var cv *tls.CertificateVerificationError
	switch {
	case errors.As(ue, &dns):
		return &cliError{Title: "Can't find the relay", Code: "relay_dns", err: ue,
			Reason: fmt.Sprintf("%s does not resolve (the URL %s came from %s). Tailscale may be down, or MagicDNS off.", dns.Name, ue.Base, from),
			Fixes:  []string{"tailscale status", relayFix}}
	case errors.As(ue, &cv):
		return &cliError{Title: "The relay's certificate isn't trusted", Code: "relay_tls", err: ue,
			Reason: fmt.Sprintf("Verifying %s (from %s) failed: %s.", ue.Base, from, cv.Err),
			Fixes:  []string{"Check that the URL names the relay's tailnet host", relayFix}}
	case strings.Contains(cause.Error(), "server gave HTTP response to HTTPS client"):
		return &cliError{Title: "The relay doesn't speak HTTPS", Code: "relay_tls", err: ue,
			Reason: fmt.Sprintf("%s answered in plain HTTP (the URL came from %s); it may run with --tls=false.", ue.Base, from),
			Fixes:  []string{cmdPath + " --relay " + strings.Replace(ue.Base, "https://", "http://", 1)}}
	}
	return &cliError{Title: "Can't reach the relay", Code: "relay_unreachable", err: ue,
		Reason: fmt.Sprintf("Tried %s (from %s): %s.", ue.Base, from, strings.TrimSuffix(cause.Error(), ".")),
		Fixes:  []string{"tailscale status", "Check that the relay is running", relayFix}}
}
