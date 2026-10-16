package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Radiergummi/tsrelay/internal/client"
	"github.com/Radiergummi/tsrelay/internal/identify"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
	"github.com/spf13/cobra"
	"tailscale.com/ipn/ipnstate"
)

// complete runs cobra's __complete for args, the last of which is the word
// being completed. It returns the candidates (with their help after a tab)
// and the directive. Anything on stderr besides cobra's own trailer fails
// the test: completions must never print to the terminal.
func complete(t *testing.T, d deps, args ...string) ([]string, cobra.ShellCompDirective) {
	t.Helper()
	var stdout, stderr syncBuf
	if code := run(t.Context(), append([]string{cobra.ShellCompRequestCmd}, args...), &stdout, &stderr, d); code != 0 {
		t.Fatalf("__complete %q exited %d\n%s", args, code, stderr.String())
	}
	for _, l := range strings.Split(strings.TrimSpace(stderr.String()), "\n") {
		if l != "" && !strings.HasPrefix(l, "Completion ended with directive:") {
			t.Errorf("__complete %q printed %q on stderr", args, l)
		}
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	last, ok := strings.CutPrefix(lines[len(lines)-1], ":")
	directive, err := strconv.Atoi(last)
	if !ok || err != nil {
		t.Fatalf("__complete %q: no directive in %q", args, stdout.String())
	}
	if len(lines) == 1 {
		return nil, cobra.ShellCompDirective(directive)
	}
	return lines[:len(lines)-1], cobra.ShellCompDirective(directive)
}

// receiverIn is a receiver allowed on the channels prod and staging.*.
var receiverIn = identify.User("laptop", "moritz@example.com", 1, `{"roles":["receiver"],"channels":["prod","staging.*"]}`)

func TestCompleteHooks(t *testing.T) {
	e := newCLIEnv(t)
	d := deps{HTTP: tunnel.NewHTTPClient(e.fake.Dial(receiver)), StatePath: e.statePath}
	e.tsrelay("hooks", "create", "cb", "--description", "payments")
	e.tsrelay("hooks", "create", "sv")
	hs := e.listJSON()
	ids := map[string]string{}
	for _, h := range hs {
		ids[h.Path] = h.ID
	}
	cb := []string{"cb\tpayments", ids["cb"] + "\tpayments"}
	sv := []string{"sv\tchannel default", ids["sv"] + "\tchannel default"}
	r := "--relay=" + e.relayURL
	for _, tc := range []struct {
		args []string
		want []string
	}{
		{[]string{"hooks", "show", r, ""}, slices.Concat(cb, sv)},
		{[]string{"hooks", "show", r, "c"}, cb[:1]},
		{[]string{"hooks", "show", r, "cb", ""}, nil}, // show takes one hook
		{[]string{"hooks", "update", r, ""}, slices.Concat(cb, sv)},
		{[]string{"hooks", "update", r, "sv", ""}, nil},
		{[]string{"hooks", "delete", r, ""}, slices.Concat(cb, sv)},
		{[]string{"hooks", "delete", r, "cb", ""}, sv},      // named by path
		{[]string{"hooks", "delete", r, ids["sv"], ""}, cb}, // named by ID
		{[]string{"attach", r, "/cb/", ""}, sv},             // slashes around a path still name it
		{[]string{"attach", r, "cb", "sv", ""}, nil},        // all named
		{[]string{"attach", r, "--to", "8080", ""}, slices.Concat(cb, sv)},
	} {
		got, dir := complete(t, d, tc.args...)
		slices.Sort(got)
		slices.Sort(tc.want)
		if !slices.Equal(got, tc.want) || dir != cobra.ShellCompDirectiveNoFileComp {
			t.Errorf("%q: got %q, %d; want %q, %d", tc.args, got, dir, tc.want, cobra.ShellCompDirectiveNoFileComp)
		}
	}
}

func TestCompleteChannels(t *testing.T) {
	e := newCLIEnv(t)
	d := deps{HTTP: tunnel.NewHTTPClient(e.fake.Dial(receiverIn)), StatePath: e.statePath}
	if code := run(t.Context(), []string{"hooks", "create", "eu", "--channel", "staging.eu", "--relay", e.relayURL}, new(nopWriter), new(nopWriter), d); code != 0 {
		t.Fatalf("hooks create exited %d", code)
	}
	r := "--relay=" + e.relayURL
	for _, args := range [][]string{
		{"http", r, "8080", "--channel", ""},
		{"http", r, "8080", "-c", ""},
		{"hooks", "create", r, "--channel", ""},
		{"hooks", "update", r, "eu", "--channel", ""},
	} {
		// "staging.*" is a pattern, not a channel; staging.eu is the hook's.
		if got, dir := complete(t, d, args...); !slices.Equal(got, []string{"prod", "staging.eu"}) || dir != cobra.ShellCompDirectiveNoFileComp {
			t.Errorf("%q: got %q, %d", args, got, dir)
		}
	}
	if got, _ := complete(t, d, "http", r, "8080", "--channel", "st"); !slices.Equal(got, []string{"staging.eu"}) {
		t.Errorf("--channel st: got %q", got)
	}
}

func TestCompleteTargets(t *testing.T) {
	e := newCLIEnv(t)
	d := deps{HTTP: tunnel.NewHTTPClient(e.fake.Dial(receiver)), StatePath: e.statePath}
	st, _ := client.LoadState(e.statePath, "")
	cwd, _ := os.Getwd()
	for _, tc := range []struct{ target, url string }{{"3000", e.relayURL + "/~moritz/a"}, {"https://localhost:8443", e.relayURL + "/~moritz/b"}} {
		u, _ := client.ParseTarget(tc.target)
		st.Remember(client.StateKey(e.relayURL, cwd, u), "x", tc.url)
	}
	// Another relay's and another directory's targets are not offered.
	other, _ := client.ParseTarget("9999")
	st.Remember(client.StateKey("https://other", cwd, other), "x", "https://other/~moritz/x")
	st.Remember(client.StateKey(e.relayURL, "/elsewhere", other), "x", e.relayURL+"/~moritz/x")

	want := []string{"http://localhost:3000\t" + e.relayURL + "/~moritz/a", "https://localhost:8443\t" + e.relayURL + "/~moritz/b"}
	r := "--relay=" + e.relayURL
	for _, args := range [][]string{
		{"http", r, ""},
		{"url", r, ""},
		{"attach", r, "cb", "--to", ""},
		{"attach", r, "cb", "-t", ""},
	} {
		if got, dir := complete(t, d, args...); !slices.Equal(got, want) || dir != cobra.ShellCompDirectiveNoFileComp {
			t.Errorf("%q: got %q, %d; want %q", args, got, dir, want)
		}
	}
	if got, _ := complete(t, d, "http", r, "https"); !slices.Equal(got, want[1:]) {
		t.Errorf("http https: got %q", got)
	}
	if got, dir := complete(t, d, "http", r, "3000", ""); got != nil || dir != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("a second target: got %q, %d", got, dir)
	}
	if _, dir := complete(t, d, "http", r, "3000", "--", ""); dir != cobra.ShellCompDirectiveDefault {
		t.Errorf("after --, files complete: got directive %d", dir)
	}
}

func TestCompleteEnumFlags(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want []string
	}{
		{[]string{"http", "8080", "--attach-policy", ""}, []string{"takeover", "reject"}},
		{[]string{"hooks", "create", "--host-header", ""}, []string{"preserve", "rewrite"}},
		{[]string{"hooks", "update", "cb", "--attach-policy", ""}, []string{"takeover", "reject"}},
		{[]string{"serve", "--log-format", ""}, []string{"json", "text"}},
		{[]string{"serve", "--log-level", ""}, []string{"debug", "info", "warn", "error"}},
		{[]string{"serve", "--user-slug", ""}, []string{"local-part", "login"}},
		{[]string{"serve", "--hook-attach-policy-default", ""}, []string{"takeover", "reject"}},
		{[]string{"serve", "--hook-host-header-default", ""}, []string{"preserve", "rewrite"}},
	} {
		if got, dir := complete(t, deps{}, tc.args...); !slices.Equal(got, tc.want) || dir != cobra.ShellCompDirectiveNoFileComp {
			t.Errorf("%q: got %q, %d; want %q", tc.args, got, dir, tc.want)
		}
	}
}

// Whatever goes wrong, a completion offers nothing, no files either, and
// prints nothing (complete checks stderr).
func TestCompleteFailsSilently(t *testing.T) {
	unsetenv(t, "TSRELAY_RELAY")
	e := newCLIEnv(t)
	d := deps{HTTP: tunnel.NewHTTPClient(e.fake.Dial(receiver)), StatePath: e.statePath}
	noTailscale := deps{Status: func(context.Context) (*ipnstate.Status, error) { return nil, errors.New("connection refused") }}
	// extra goes after each command's name.
	words := [][]string{{"hooks", "show"}, {"http"}, {"attach"}, {"url"}}
	rest := [][]string{{""}, {"8080", "--channel", ""}, {"cb", "--to", ""}, {""}}
	check := func(name string, d deps, extra ...string) {
		t.Helper()
		for i, w := range words {
			args := slices.Concat(w, extra, rest[i])
			if got, dir := complete(t, d, args...); got != nil || dir != cobra.ShellCompDirectiveNoFileComp {
				t.Errorf("%s, %q: got %q, %d", name, args, got, dir)
			}
		}
	}
	check("no relay configured", noTailscale)
	check("unreachable", deps{StatePath: e.statePath}, "--relay=http://127.0.0.1:1")
	t.Setenv("TSRELAY_RELAY", "http://127.0.0.1:1")
	check("unreachable from the environment", deps{StatePath: e.statePath})
	unsetenv(t, "TSRELAY_RELAY")
	e.fake.Fail(errors.New("whois failed"))
	check("not identified", d, "--relay="+e.relayURL)
	e.fake.Fail(nil)

	// A bad TSRELAY_* variable fails the commands, but not completions.
	t.Setenv("TSRELAY_JSON", "maybe")
	if got, _ := complete(t, deps{}, "serve", "--log-format", ""); !slices.Equal(got, []string{"json", "text"}) {
		t.Errorf("with a bad TSRELAY_JSON: got %q", got)
	}
	unsetenv(t, "TSRELAY_JSON")

	// A relay of another version answers, but the warning stays off the
	// terminal.
	e.restartRelayAt("v9.9.9")
	setVersion(t, "v0.1.0")
	e.tsrelay("hooks", "create", "cb")
	if got, _ := complete(t, d, "hooks", "show", "--relay="+e.relayURL, "c"); len(got) != 1 {
		t.Errorf("got %q, want cb", got)
	}
}

// A relay that never answers gives up after completionTimeout, 2 s.
func TestCompleteTimesOut(t *testing.T) {
	if completionTimeout != 2*time.Second {
		t.Fatalf("completionTimeout = %v, want 2s", completionTimeout)
	}
	hung := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
	}))
	defer hung.Close()
	start := time.Now()
	got, dir := complete(t, deps{}, "hooks", "show", "--relay="+hung.URL, "")
	if took := time.Since(start); took < completionTimeout || took > completionTimeout+2*time.Second {
		t.Errorf("took %v, want about %v", took, completionTimeout)
	}
	if got != nil || dir != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("got %q, %d", got, dir)
	}
}
