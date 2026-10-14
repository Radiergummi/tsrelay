package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"charm.land/fang/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/term"

	"github.com/Radiergummi/tsrelay/internal/client"
	"github.com/Radiergummi/tsrelay/internal/problem"
)

// stream is one output stream and how much styling it takes. Plain output
// (not fancy) contains no escape sequences at all.
type stream struct {
	w     io.Writer
	tty   bool // a terminal: JSON is indented
	fancy bool // a terminal with colors on: styles and glyphs
	links bool // fancy, and OSC 8 hyperlinks are wanted
	width int  // columns; 100 when unknown
}

// newStream inspects w. noColor and noLinks are --no-color and
// --no-hyperlinks; NO_COLOR with any non-empty value counts like
// --no-color, as https://no-color.org asks.
func newStream(w io.Writer, noColor, noLinks bool) stream {
	p := colorprofile.Detect(w, os.Environ())
	s := stream{w: w, tty: isTerminal(w), width: 100}
	s.fancy = s.tty && p >= colorprofile.ANSI && !noColor && os.Getenv("NO_COLOR") == ""
	s.links = s.fancy && !noLinks
	if s.fancy {
		s.w = &colorprofile.Writer{Forward: w, Profile: p} // downsamples colors to what the terminal has
	}
	if f, ok := w.(term.File); ok {
		if cols, _, err := term.GetSize(f.Fd()); err == nil && cols > 0 {
			s.width = cols
		}
	}
	return s
}

// isTerminal reports whether w is a real terminal, honoring TTY_FORCE (the
// override colorprofile's own detection understands) so tests can simulate
// one, and TERM=dumb (which colorprofile also treats as no terminal at
// all). Unlike colorprofile.Detect's color profile, this must not go fancy
// just because CLICOLOR_FORCE or FORCE_COLOR is set on a pipe: --json's
// choice between indented and one-object-per-line output depends on it.
func isTerminal(w io.Writer) bool {
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	if v, ok := os.LookupEnv("TTY_FORCE"); ok {
		forced, _ := strconv.ParseBool(v)
		return forced
	}
	f, ok := w.(term.File)
	return ok && term.IsTerminal(f.Fd())
}

// The palette uses the terminal's own 16 colors, so it follows its theme.
var (
	styleBold  = lipgloss.NewStyle().Bold(true)
	styleDim   = lipgloss.NewStyle().Faint(true)
	styleBrand = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Magenta)
	styleOK    = lipgloss.NewStyle().Foreground(lipgloss.Green)
	styleInfo  = lipgloss.NewStyle().Foreground(lipgloss.Cyan)
	styleWarn  = lipgloss.NewStyle().Foreground(lipgloss.Yellow)
	styleBad   = lipgloss.NewStyle().Foreground(lipgloss.Red)
)

func (s stream) style(st lipgloss.Style, text string) string {
	if !s.fancy {
		return text
	}
	return st.Render(text)
}

func (s stream) link(text, url string) string {
	if !s.links {
		return text
	}
	return lipgloss.NewStyle().Hyperlink(url).Render(text)
}

func (s stream) glyph(g string) string {
	if !s.fancy {
		return ""
	}
	return g + " "
}

// writeJSON writes v as one JSON value: indented on a terminal, compact
// on one line otherwise.
func writeJSON(s stream, v any) error {
	var b []byte
	var err error
	if s.tty {
		b, err = json.MarshalIndent(v, "", "  ")
	} else {
		b, err = json.Marshal(v)
	}
	if err != nil {
		return err
	}
	_, err = s.w.Write(append(b, '\n'))
	return err
}

// problemHints say what to do about a relay refusal, by problem code.
// Codes whose detail already says what is wrong have none.
var problemHints = map[string]string{
	problem.Forbidden:          "check your grants for the relay's capability in your tailnet policy",
	problem.HookNotFound:       "tsrelay hooks list shows your hooks",
	problem.NotFound:           "tsrelay hooks list shows your hooks",
	problem.MisdirectedRequest: "check --relay (or TSRELAY_RELAY): it may name the wrong host",
	problem.Internal:           "the relay failed; its logs have the details",
}

func problemHint(code string) string { return problemHints[code] }

// formatError renders err for people: a relay refusal as its title, its
// detail and a hint; anything else on one line, with its own hint if it has
// one (e.g. an unreachable relay).
func formatError(s stream, err error) string {
	var p *problem.Problem
	if !errors.As(err, &p) {
		msg := s.glyph(s.style(styleBad, "✗")) + s.style(styleBad, "tsrelay: ") + err.Error()
		if h := errHint(err); h != "" {
			msg += "\n  " + s.style(styleDim, "hint: "+h)
		}
		return msg
	}
	var b strings.Builder
	b.WriteString(s.glyph(s.style(styleBad, "✗")))
	b.WriteString(s.style(styleBad.Bold(true), fmt.Sprintf("%s (%d)", p.Title, p.Status)))
	if p.Detail != "" {
		b.WriteString("\n  " + p.Detail)
	}
	if h := problemHint(p.Code); h != "" {
		b.WriteString("\n  " + s.style(styleDim, "hint: "+h))
	}
	return b.String()
}

// errHint gives a hint for an error that is not a relay refusal, e.g. one
// that means the relay could not be reached at all.
func errHint(err error) string {
	var ue *client.UnreachableError
	if errors.As(err, &ue) {
		return "check --relay or TSRELAY_RELAY"
	}
	return ""
}

// jsonError is how an error is reported on stderr with --json.
type jsonError struct {
	Error   string           `json:"error"`
	Problem *problem.Problem `json:"problem,omitempty"`
}

func newJSONError(err error) jsonError {
	var p *problem.Problem
	if errors.As(err, &p) {
		msg := p.Detail
		if msg == "" {
			msg = p.Title
		}
		return jsonError{Error: msg, Problem: p}
	}
	return jsonError{Error: err.Error()}
}

// handleError is fang's error handler. Errors already reported
// (exitCodeError) print nothing; with --json the error goes to stderr as
// JSON; otherwise it is formatted for people, with a pointer to --help for
// usage errors.
func (c *cli) handleError(_ io.Writer, _ fang.Styles, err error) {
	var ec exitCodeError
	if errors.As(err, &ec) {
		return
	}
	s := newStream(c.stderr, c.noColor, c.noLinks)
	if c.json {
		writeJSON(s, newJSONError(err))
		return
	}
	fmt.Fprintln(s.w, formatError(s, err))
	if exitStatus(err) == 2 {
		fmt.Fprintln(s.w, s.style(styleDim, "  run with --help for usage"))
	}
}
