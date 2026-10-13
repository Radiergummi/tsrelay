package ui

import (
	"net/http"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// Whatever the bytes, a shell reads a quoted string back unchanged.
func TestShellQuoteRoundTrips(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	for _, s := range []string{"", "plain", "it's", "a\nb", "a\r\nb", "\r", "$(touch /tmp/x)", "`id` \\ \" '\"'", "ümlaut"} {
		out, err := exec.Command(sh, "-c", "printf %s "+shellQuote(s)).Output()
		if err != nil || string(out) != s {
			t.Errorf("%q came back as %q (%v)", s, out, err)
		}
	}
}

// A shell running the command passes curl exactly the captured request.
func TestCurlCommand(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	body := "{\"note\": \"it's $(id)\"}\nline two"
	cmd := curlCommand("POST", "http://localhost:8080/done?a=1&b=2", http.Header{
		"Content-Type":   {"application/json"},
		"Content-Length": {"42"}, // curl sets its own
		"X-Note":         {"it's"},
	}, []byte(body))
	// Replace curl with a function that prints its arguments, NUL-separated.
	out, err := exec.Command(sh, "-c", `curl() { for a in "$@"; do printf '%s\0' "$a"; done; }; `+cmd).Output()
	if err != nil {
		t.Fatalf("%v\n%s", err, cmd)
	}
	got := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
	want := []string{"-g", "-X", "POST", "http://localhost:8080/done?a=1&b=2",
		"-H", "Content-Type: application/json", "-H", "X-Note: it's", "--data-binary", body}
	if !slices.Equal(got, want) {
		t.Fatalf("curl got %q\nwant %q\ncommand:\n%s", got, want, cmd)
	}
}

// A body containing CR survives: the command string never contains a raw
// CR byte (an HTML parser would turn one into a line feed on its way to
// the clipboard), yet the shell still passes curl the exact bytes.
func TestCurlCommandCRLF(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	body := "--x\r\nContent-Disposition: form-data\r\n\r\nv\r\n--x--\r\n"
	cmd := curlCommand("POST", "http://localhost/done", http.Header{"Content-Type": {"multipart/form-data"}}, []byte(body))
	if strings.ContainsRune(cmd, '\r') {
		t.Fatalf("curl command contains a raw CR byte:\n%q", cmd)
	}
	out, err := exec.Command(sh, "-c", `curl() { for a in "$@"; do printf '%s\0' "$a"; done; }; `+cmd).Output()
	if err != nil {
		t.Fatalf("%v\n%s", err, cmd)
	}
	args := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
	if got := args[len(args)-1]; got != body {
		t.Fatalf("body came back as %q, want %q\ncommand:\n%s", got, body, cmd)
	}
}
