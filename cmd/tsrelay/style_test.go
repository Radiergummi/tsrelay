package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/Radiergummi/tsrelay/internal/problem"
)

// unsetenv removes key for the test and restores it afterwards.
func unsetenv(t *testing.T, key string) {
	t.Helper()
	t.Setenv(key, "")
	os.Unsetenv(key)
}

// forceTerminal makes every stream look like a 256-color terminal.
func forceTerminal(t *testing.T) {
	t.Helper()
	t.Setenv("TTY_FORCE", "1")
	t.Setenv("TERM", "xterm-256color")
	for _, k := range []string{"NO_COLOR", "CLICOLOR", "CLICOLOR_FORCE", "COLORTERM"} {
		unsetenv(t, k)
	}
}

func TestNewStream(t *testing.T) {
	for _, tc := range []struct {
		name              string
		env               map[string]string
		noColor, noLinks  bool
		tty, fancy, links bool
	}{
		{name: "terminal", tty: true, fancy: true, links: true},
		{name: "not a terminal", env: map[string]string{"TTY_FORCE": "0"}},
		{name: "dumb terminal", env: map[string]string{"TERM": "dumb"}},
		{name: "NO_COLOR=1", env: map[string]string{"NO_COLOR": "1"}, tty: true},
		{name: "NO_COLOR=yes", env: map[string]string{"NO_COLOR": "yes"}, tty: true}, // any non-empty value
		{name: "--no-color", noColor: true, tty: true},
		{name: "--no-hyperlinks", noLinks: true, tty: true, fancy: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			forceTerminal(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			s := newStream(&bytes.Buffer{}, tc.noColor, tc.noLinks)
			if s.tty != tc.tty || s.fancy != tc.fancy || s.links != tc.links {
				t.Fatalf("tty, fancy, links = %v, %v, %v; want %v, %v, %v", s.tty, s.fancy, s.links, tc.tty, tc.fancy, tc.links)
			}
			if s.width != 100 {
				t.Fatalf("width = %d, want 100 for a non-file writer", s.width)
			}
		})
	}
}

// Review focus 2: plain output carries no escape sequence of any kind,
// although colorprofile's ASCII profile would keep bold and hyperlinks.
func TestPlainStreamHasNoEscapes(t *testing.T) {
	forceTerminal(t)
	t.Setenv("NO_COLOR", "1")
	var buf bytes.Buffer
	s := newStream(&buf, false, false)
	s.w.Write([]byte(s.glyph("●") + s.style(styleBold, "POST") + " " + s.link("c_1", "https://r/inspect/c_1") + "\n"))
	if strings.Contains(buf.String(), "\x1b") {
		t.Fatalf("plain output contains an escape: %q", buf.String())
	}
	if buf.String() != "POST c_1\n" {
		t.Fatalf("got %q", buf.String())
	}
}

func TestFancyStreamStylesAndLinks(t *testing.T) {
	forceTerminal(t)
	var buf bytes.Buffer
	s := newStream(&buf, false, false)
	s.w.Write([]byte(s.style(styleBad, "502") + s.link("c_1", "https://r/inspect/c_1")))
	for _, want := range []string{"\x1b[", "\x1b]8;;https://r/inspect/c_1"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("fancy output lacks %q: %q", want, buf.String())
		}
	}
}

func TestWriteJSON(t *testing.T) {
	v := map[string]any{"a": 1, "b": []int{2}}
	forceTerminal(t)
	var tty bytes.Buffer
	writeJSON(newStream(&tty, true, false), v) // --no-color must not affect indentation
	if !strings.Contains(tty.String(), "\n  \"a\": 1") {
		t.Fatalf("on a terminal JSON must be indented:\n%s", tty.String())
	}
	t.Setenv("TTY_FORCE", "0")
	var pipe bytes.Buffer
	writeJSON(newStream(&pipe, false, false), v)
	if pipe.String() != `{"a":1,"b":[2]}`+"\n" {
		t.Fatalf("piped JSON = %q, want compact on one line", pipe.String())
	}
}

func TestFormatError(t *testing.T) {
	p := &problem.Problem{Title: "Forbidden", Status: 403, Code: problem.Forbidden, Detail: "you can't serve this hook"}
	t.Setenv("TTY_FORCE", "0")
	plain := formatError(newStream(&bytes.Buffer{}, false, false), p)
	for _, want := range []string{"Forbidden (403)", "you can't serve this hook", "hint: " + problemHint(problem.Forbidden)} {
		if !strings.Contains(plain, want) {
			t.Errorf("plain error lacks %q:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "\x1b") {
		t.Errorf("plain error has escapes: %q", plain)
	}
	if got := formatError(newStream(&bytes.Buffer{}, false, false), errors.New("boom")); got != "tsrelay: boom" {
		t.Errorf("plain non-problem error = %q", got)
	}
	forceTerminal(t)
	if fancy := formatError(newStream(&bytes.Buffer{}, false, false), p); !strings.Contains(fancy, "\x1b[") {
		t.Errorf("fancy error is unstyled: %q", fancy)
	}
}

func TestProblemHints(t *testing.T) {
	for _, code := range []string{problem.Forbidden, problem.HookNotFound, problem.NotFound, problem.MisdirectedRequest, problem.Internal} {
		if problemHint(code) == "" {
			t.Errorf("no hint for %q", code)
		}
	}
	if problemHint(problem.Invalid) != "" {
		t.Error("invalid needs no hint: its detail says what is wrong")
	}
}

func TestNewJSONError(t *testing.T) {
	p := &problem.Problem{Title: "Not Found", Status: 404, Code: problem.HookNotFound, Detail: "no such hook"}
	b, _ := json.Marshal(newJSONError(p))
	if !strings.Contains(string(b), `"error":"no such hook"`) || !strings.Contains(string(b), `"code":"hook_not_found"`) {
		t.Fatalf("got %s", b)
	}
	b, _ = json.Marshal(newJSONError(errors.New("boom")))
	if string(b) != `{"error":"boom"}` {
		t.Fatalf("got %s", b)
	}
}
