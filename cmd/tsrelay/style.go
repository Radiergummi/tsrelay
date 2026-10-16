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

// formatError renders e for people: a headline saying what went wrong,
// the reason, and the fixes to try.
func formatError(s stream, e *cliError) string {
	var b strings.Builder
	b.WriteString(s.style(styleBad, "✗") + " " + s.style(styleBad.Bold(true), e.Title))
	if e.Reason != "" {
		b.WriteString("\n  " + strings.ReplaceAll(e.Reason, "\n", "\n  "))
	}
	if len(e.Fixes) > 0 {
		b.WriteString("\n\n  " + s.style(styleDim, "Try:"))
		for _, f := range e.Fixes {
			b.WriteString("\n    " + s.style(styleInfo, f))
		}
	}
	return b.String()
}

// jsonError is how an error is reported on stderr with --json.
type jsonError struct {
	Error struct {
		Title  string   `json:"title"`
		Reason string   `json:"reason"`
		Fixes  []string `json:"fixes"`
		Code   string   `json:"code"`
	} `json:"error"`
}

func newJSONError(e *cliError) jsonError {
	var je jsonError
	je.Error.Title, je.Error.Reason, je.Error.Fixes, je.Error.Code = e.Title, e.Reason, e.Fixes, e.Code
	if je.Error.Fixes == nil {
		je.Error.Fixes = []string{}
	}
	return je
}

// handleError is fang's error handler. Errors already reported
// (exitCodeError) print nothing; the others are explained on stderr, as
// JSON with --json.
func (c *cli) handleError(_ io.Writer, _ fang.Styles, err error) {
	var ec exitCodeError
	if errors.As(err, &ec) {
		return
	}
	s := c.stderrStream()
	if c.json {
		writeJSON(s, newJSONError(c.explain(err)))
		return
	}
	fmt.Fprintln(s.w, formatError(s, c.explain(err)))
}
