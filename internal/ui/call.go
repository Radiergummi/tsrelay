package ui

import (
	"bytes"
	"fmt"
	"maps"
	"mime"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/Radiergummi/tsrelay/internal/api"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/inspect"
)

// callView is the call detail page.
type callView struct {
	V        viewer
	Hook     api.Hook
	Owner    bool
	Call     inspect.Call // metadata only unless Owner
	Request  message
	Attempts []attemptView
	Response *attemptView // the last attempt, whose response the sender got
	// For the owner: NoReplay says why the call cannot be replayed; Curl
	// holds copy-as-curl commands, or NoCurl says why there are none.
	NoReplay, NoCurl string
	Curl             []curlView
}

type attemptView struct {
	inspect.Attempt
	Response message
}

// message is one side of a call: its headers and body.
type message struct {
	Headers  []headerLine
	Redacted []string
	Body     bodyView
}

type headerLine struct{ Name, Value string }

// bodyView is a captured body as the page shows it.
type bodyView struct {
	inspect.Capture
	ID     string // id of the <pre> showing it, for the pretty-print toggle
	Kept   int64  // bytes kept
	Text   string // the kept bytes, if they are UTF-8 text
	Binary bool   // the kept bytes are not UTF-8 text
	JSON   bool   // the Content-Type says JSON
}

type curlView struct{ Label, Command string }

const noCall = "There is no such call. The relay keeps only the most recent calls of each hook, in memory, and forgets them when it restarts."

// callPage shows one call. Admins who do not own the hook see metadata only.
func (u *UI) callPage(w http.ResponseWriter, r *http.Request, v viewer) {
	h, ok := u.api.VisibleHook(hooks.ID(r.PathValue("id")), v.ID)
	c, found := u.api.Inspect.Get(r.PathValue("callID"))
	if !ok || !found || c.HookID != h.ID {
		u.error(w, v, http.StatusNotFound, noCall)
		return
	}
	owner := v.owns(h)
	if !owner {
		c = c.Metadata()
	}
	hook := u.api.HookJSON(h)
	view := callView{V: v, Hook: hook, Owner: owner, Call: c,
		Request: newMessage("request-body", c.RequestHeaders, c.Redacted, c.RequestBody)}
	for i, a := range c.Attempts {
		view.Attempts = append(view.Attempts, attemptView{a,
			newMessage(fmt.Sprintf("response-body-%d", i), a.ResponseHeaders, a.Redacted, a.ResponseBody)})
	}
	if n := len(view.Attempts); n > 0 {
		view.Response = &view.Attempts[n-1]
	}
	if owner {
		switch p := api.ReplayRefusal(c); {
		case p != nil:
			view.NoReplay, view.NoCurl = p.Detail, p.Detail
		case view.Request.Body.Binary:
			view.NoCurl = "The request body is binary, so it is not offered as a curl command. Replaying it works."
		default:
			view.Curl = curls(hook, c)
		}
	}
	u.render(w, http.StatusOK, "call", view)
}

// curls are the copy-as-curl commands for a call (DESIGN.md §9.3): to the
// tunnel's local target, which developers usually want, and to the hook's
// URL, which only nodes with the sender role may call.
func curls(h api.Hook, c inspect.Call) []curlView {
	rest := c.Path
	if c.Query != "" {
		rest += "?" + c.Query
	}
	var out []curlView
	if h.Tunnel != nil && h.Tunnel.Target != "" {
		out = append(out, curlView{"To the local target", curlCommand(c.Method, strings.TrimSuffix(h.Tunnel.Target, "/")+rest, c.RequestHeaders, c.RequestBody.Body)})
	}
	return append(out, curlView{"To the hook URL (needs the sender role)", curlCommand(c.Method, h.URL+rest, c.RequestHeaders, c.RequestBody.Body)})
}

func newMessage(id string, h http.Header, redacted []string, c inspect.Capture) message {
	return message{Headers: headerLines(h), Redacted: redacted, Body: newBody(id, c, h.Get("Content-Type"))}
}

// headerLines lists h one value per line, sorted by name.
func headerLines(h http.Header) []headerLine {
	var out []headerLine
	for _, name := range slices.Sorted(maps.Keys(h)) {
		for _, v := range h[name] {
			out = append(out, headerLine{name, v})
		}
	}
	return out
}

func newBody(id string, c inspect.Capture, contentType string) bodyView {
	b := bodyView{Capture: c, ID: id, Kept: int64(len(c.Body))}
	kept := c.Body
	if c.Truncated {
		kept = trimPartialRune(kept) // the cut may have split a character
	}
	// A NUL byte counts as binary too: a shell argument cannot hold one, so
	// such a body is never offered as a curl command either.
	b.Binary = !utf8.Valid(kept) || bytes.IndexByte(kept, 0) >= 0
	if !b.Binary {
		b.Text = string(kept)
	}
	mt, _, _ := mime.ParseMediaType(contentType)
	b.JSON = mt == "application/json" || strings.HasSuffix(mt, "+json")
	return b
}

// trimPartialRune drops an incomplete UTF-8 sequence at the end of b.
func trimPartialRune(b []byte) []byte {
	for i := 1; i < utf8.UTFMax && i <= len(b); i++ {
		if utf8.RuneStart(b[len(b)-i]) {
			if !utf8.FullRune(b[len(b)-i:]) {
				return b[:len(b)-i]
			}
			break
		}
	}
	return b
}
