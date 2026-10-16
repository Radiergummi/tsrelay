package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Radiergummi/tsrelay/internal/client"
	"github.com/Radiergummi/tsrelay/internal/problem"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
	"github.com/charmbracelet/x/ansi"
	"tailscale.com/ipn/ipnstate"
)

// errorCase is a command line and the explained error it must end with.
type errorCase struct {
	args          []string
	env           map[string]string
	status        int
	title, reason string // a reason ending in "…" only has to start with what precedes it
	fixes         []string
	code          string
}

// check runs --json, then extra (e.g. --relay), then tc.args, and compares
// the JSON error on stderr's last line with tc.
func (tc errorCase) check(t *testing.T, d deps, extra ...string) {
	t.Helper()
	for k, v := range tc.env {
		t.Setenv(k, v)
	}
	var stderr syncBuf
	args := slices.Concat([]string{"--json"}, extra, tc.args)
	code := run(t.Context(), args, new(nopWriter), &stderr, d)
	lines := strings.Split(strings.TrimSpace(stderr.String()), "\n")
	var je jsonError
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &je); err != nil {
		t.Fatalf("%q: stderr does not end with a JSON error: %v\n%s", tc.args, err, stderr.String())
	}
	got := je.Error
	reasonOK := got.Reason == tc.reason
	if want, ok := strings.CutSuffix(tc.reason, "…"); ok {
		reasonOK = strings.HasPrefix(got.Reason, want)
	}
	if code != tc.status || got.Title != tc.title || !reasonOK || !slices.Equal(got.Fixes, tc.fixes) || got.Code != tc.code {
		t.Errorf("%q: exit %d, got\n  title  %q\n  reason %q\n  fixes  %q\n  code   %q\nwant exit %d,\n  title  %q\n  reason %q\n  fixes  %q\n  code   %q",
			tc.args, code, got.Title, got.Reason, got.Fixes, got.Code, tc.status, tc.title, tc.reason, tc.fixes, tc.code)
	}
}

// Every mistake tsrelay spots without a relay, cobra's and pflag's
// included, is explained with a headline, a reason and fixes.
func TestUsageErrorSites(t *testing.T) {
	unsetenv(t, "TSRELAY_RELAY")
	for _, tc := range []errorCase{
		// cobra: commands and arity
		{args: []string{"hoks"}, title: `Unknown command "hoks"`, reason: `tsrelay has no command "hoks".`,
			fixes: []string{"tsrelay hooks", "tsrelay logs", "tsrelay --help"}},
		{args: []string{"hooks", "lst"}, title: `Unknown command "lst"`, reason: `tsrelay hooks has no command "lst".`,
			fixes: []string{"tsrelay hooks list", "tsrelay hooks --help"}},
		{args: []string{"attach", "--to", "8080"}, title: "No hook given", reason: "tsrelay attach takes one or more hooks.",
			fixes: []string{"tsrelay attach payments ship/v1 --to 8080"}},
		{args: []string{"hooks", "show"}, title: "No hook given", reason: "tsrelay hooks show takes one hook.",
			fixes: []string{"tsrelay hooks show --help"}},
		{args: []string{"hooks", "update"}, title: "No hook given", reason: "tsrelay hooks update takes one hook.",
			fixes: []string{"tsrelay hooks update --help"}},
		{args: []string{"hooks", "delete"}, title: "No hook given", reason: "tsrelay hooks delete takes one or more hooks.",
			fixes: []string{"tsrelay hooks delete --help"}},
		{args: []string{"hooks", "list", "extra"}, title: `Unexpected argument "extra"`, reason: "tsrelay hooks list takes no arguments.",
			fixes: []string{"tsrelay hooks list --help"}},
		{args: []string{"hooks", "create", "a", "b"}, title: `Unexpected argument "b"`, reason: "tsrelay hooks create takes at most one path.",
			fixes: []string{"tsrelay hooks create --help"}},
		{args: []string{"url", "a", "b"}, title: `Unexpected argument "b"`, reason: "tsrelay url takes at most one port or URL.",
			fixes: []string{"tsrelay url --help"}},
		{args: []string{"serve", "extra"}, title: `Unexpected argument "extra"`, reason: "tsrelay serve takes no arguments.",
			fixes: []string{"tsrelay serve --help"}},
		{args: []string{"http"}, title: "No target to forward calls to", reason: "http needs to know where your local app listens.",
			fixes: []string{"tsrelay http 3000", "tsrelay http http://localhost:8080"}},
		{args: []string{"http", "8080", "npm", "start"}, title: `Unexpected argument "npm"`,
			reason: "http forwards to one local app; a command to start goes after --.", fixes: []string{"tsrelay http 8080 -- npm start"}},

		// pflag: flags and their values
		{args: []string{"serve", "--no-such-flag"}, title: "Unknown flag --no-such-flag", reason: "tsrelay serve has no flag --no-such-flag.",
			fixes: []string{"tsrelay serve --help"}},
		{args: []string{"http", "8080", "-x"}, title: "Unknown flag -x", reason: "tsrelay http has no short flag -x.",
			fixes: []string{"tsrelay http --help"}},
		{args: []string{"serve", "-json"}, title: "Long flags take two dashes", reason: "tsrelay reads -json as the short flags -j, -s, -o, -n.",
			fixes: []string{"tsrelay serve --json"}},
		{args: []string{"serve", "--tls=maybe"}, title: `Invalid value "maybe" for --tls`, reason: "--tls takes true or false.",
			fixes: []string{"tsrelay serve --tls=false", "tsrelay serve --help"}},
		{args: []string{"hooks", "create", "--hold-timeout", "soon"}, title: `Invalid value "soon" for --hold-timeout`,
			reason: "--hold-timeout takes a duration such as 30s, 5m or 1h.",
			fixes:  []string{"tsrelay hooks create --hold-timeout 30s", "tsrelay hooks create --help"}},
		{args: []string{"serve", "--hold-queue", "many"}, title: `Invalid value "many" for --hold-queue`, reason: "--hold-queue takes a whole number.",
			fixes: []string{"tsrelay serve --hold-queue 10", "tsrelay serve --help"}},
		{args: []string{"hooks", "create", "--rate-limit", "fast"}, title: `Invalid value "fast" for --rate-limit`,
			reason: `Rate limit "fast" must be "perSecond/burst", e.g. "100/200".`,
			fixes:  []string{"tsrelay hooks create --rate-limit 100/200", "tsrelay hooks create --help"}},
		{args: []string{"serve", "--user-slug-override", "alice"}, title: `Invalid value "alice" for --user-slug-override`,
			reason: `"alice" is not key=value.`,
			fixes:  []string{"tsrelay serve --user-slug-override alice@example.com=alice", "tsrelay serve --help"}},
		{args: []string{"http", "8080", "-n"}, title: "--name needs a value",
			reason: "Hook path below your ~user prefix (default: remembered, else three random words).", fixes: []string{"tsrelay http --help"}},
		{args: []string{"http", "8080"}, env: map[string]string{"TSRELAY_PERSISTENT": "maybe"},
			title:  "Invalid TSRELAY_* variable in the environment",
			reason: `TSRELAY_PERSISTENT="maybe": …`, fixes: []string{"Correct or unset TSRELAY_PERSISTENT (it sets --persistent)"}},

		// targets
		{args: []string{"attach", "pay"}, title: "No target to forward calls to", reason: "attach needs to know where your local app listens.",
			fixes: []string{"tsrelay attach pay --to 3000", "tsrelay attach pay --to http://localhost:8080"}},
		{args: []string{"attach", "pay", "ship", "--to", "ftp://x"}, title: `"ftp://x" isn't a port or an http(s) URL`,
			reason: "tsrelay forwards calls to a port on this machine, or to an http(s) URL.",
			fixes:  []string{"tsrelay attach pay ship --to 3000", "tsrelay attach pay ship --to http://localhost:3000"}},
		{args: []string{"http", "70000"}, title: "Port 70000 is out of range", reason: "Ports go from 1 to 65535.",
			fixes: []string{"tsrelay http 3000", "tsrelay http http://localhost:3000"}},

		// hook commands
		{args: []string{"hooks", "update", "cb"}, title: "Nothing to change", reason: "hooks update changes only what its flags name.",
			fixes: []string{`tsrelay hooks update cb --description "payments callbacks"`, "tsrelay hooks update cb --channel prod", "tsrelay hooks update --help"}},
		{args: []string{"hooks", "update", "cb", "--expires-in", "-1h"}, title: "--expires-in can't be negative",
			reason: "It says how long from now the hook is deleted; 0 removes the expiry.",
			fixes:  []string{"tsrelay hooks update cb --expires-in 24h", "tsrelay hooks update cb --expires-in 0"}},
		{args: []string{"hooks", "create", "--expires-in", "-1h"}, title: "--expires-in can't be negative",
			reason: "It says how long from now the hook is deleted.", fixes: []string{"tsrelay hooks create --expires-in 24h"}},
		{args: []string{"hooks", "create", "--hold-timeout", "-1s"}, title: "--hold-timeout can't be negative",
			reason: "Durations are 0 or more.", fixes: []string{"Pass a duration such as --hold-timeout 30s"}},

		// serve
		{args: []string{"serve", "--user-slug", "bogus"}, title: `Unknown --user-slug "bogus"`,
			reason: "Slugs derive from the login's local part (local-part) or from the whole login (login).",
			fixes:  []string{"tsrelay serve --user-slug local-part", "tsrelay serve --user-slug login"}},
		{args: []string{"serve", "--revalidate-interval", "0s"}, title: "--revalidate-interval must be positive",
			reason: "It says how often attached tunnels are re-authorized.", fixes: []string{"tsrelay serve --revalidate-interval 1m"}},
		{args: []string{"serve", "--hold-queue", "0"}, title: "--hold-queue must be at least 1",
			reason: "It says how many calls per hook may wait for a tunnel at once.",
			fixes:  []string{"tsrelay serve --hold-queue 16", "To disable holding: tsrelay serve --hook-hold-timeout-default=0s"}},
		{args: []string{"serve", "--capture-calls", "0"}, title: "--capture-limit and --capture-calls must be at least 1",
			reason: "The inspector keeps at least one call, and at least one byte of it.",
			fixes:  []string{"tsrelay serve --capture-calls 50 --capture-limit 65536", "To keep no bodies: tsrelay serve --capture-bodies=false"}},
		{args: []string{"serve", "--user-slug-override", "a@b.com=A B"}, title: `Invalid slug "A B" for a@b.com`,
			reason: "A slug may only contain a-z, 0-9, '.', '_' and '-'.", fixes: []string{"tsrelay serve --user-slug-override a@b.com=a-b"}},
		{args: []string{"serve", "--log-level", "loud"}, title: `Unknown --log-level "loud"`,
			reason: "The levels are debug, info, warn and error.", fixes: []string{"tsrelay serve --log-level info"}},
		{args: []string{"serve", "--log-format", "xml"}, title: `Unknown --log-format "xml"`,
			reason: "Logs are JSON lines (json) or logfmt-style text (text).",
			fixes:  []string{"tsrelay serve --log-format json", "tsrelay serve --log-format text"}},
		{args: []string{"serve", "--hook-max-body-bytes-max", "-1"}, title: "--hook-max-body-bytes-max can't be negative",
			reason: "It is the largest body limit a hook may set; 0 means no maximum.", fixes: []string{"tsrelay serve --hook-max-body-bytes-max 0"}},
		{args: []string{"serve", "--hook-hold-timeout-default", "-1s"}, title: "--hook-hold-timeout-default can't be negative",
			reason: "Durations are 0 or more.", fixes: []string{"Pass a duration such as --hook-hold-timeout-default 30s"}},
		{args: []string{"serve", "--hook-attach-policy-default", "bogus"}, title: "Invalid hook defaults",
			reason: "Hook defaults conflict with the maxima: …", fixes: []string{"tsrelay serve --help"}},
	} {
		tc.status, tc.code = 2, "usage"
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			tc.check(t, deps{StatePath: filepath.Join(t.TempDir(), "state.json")})
		})
	}
}

// The errors that need a relay (or its absence) to happen.
func TestRelayErrorSites(t *testing.T) {
	unsetenv(t, "TSRELAY_RELAY")
	e := newCLIEnv(t)
	d := deps{HTTP: tunnel.NewHTTPClient(e.fake.Dial(receiver)), StatePath: e.statePath}
	for _, args := range [][]string{{"hooks", "create", "cb"}, {"hooks", "create", "eph", "--ephemeral"}} {
		if code, _, errOut := e.tsrelay(args...); code != 0 {
			t.Fatalf("%q: exit %d\n%s", args, code, errOut)
		}
	}
	noHook := func(args ...string) errorCase {
		return errorCase{args: args, status: 1, title: `No hook "nope"`, reason: "You have no hook with that ID or path.",
			fixes: []string{"tsrelay hooks list"}, code: "no_such_hook"}
	}
	for _, tc := range []errorCase{
		noHook("hooks", "show", "nope"),
		noHook("hooks", "update", "nope", "--description", "x"),
		noHook("hooks", "delete", "cb", "nope"),
		noHook("attach", "nope", "--to", "8080"),
		{args: []string{"http", e.local.URL, "--name", "cb", "--channel", "prod"}, status: 1,
			title:  `Your hook "cb" is in channel "default", not "prod"`,
			reason: "tsrelay http reuses the hook at this path, and won't move it to another channel on its own.",
			fixes:  []string{"tsrelay hooks update cb --channel prod", "tsrelay hooks delete cb", "Pick another path with --name"}, code: "hook_mismatch"},
		{args: []string{"http", e.local.URL, "--name", "eph", "--persistent"}, status: 1,
			title:  `Your hook "eph" is ephemeral`,
			reason: "--persistent asks for a hook that outlives tsrelay, and tsrelay http won't change the hook it reuses on its own.",
			fixes:  []string{"tsrelay hooks update eph --ephemeral=false", "tsrelay hooks delete eph", "Pick another path with --name"}, code: "hook_mismatch"},
		{args: []string{"http", e.local.URL, "--", "/nonexistent/tsrelay-test"}, status: 1,
			title: "Couldn't start /nonexistent/tsrelay-test", reason: "Fork/exec /nonexistent/tsrelay-test: …",
			fixes: []string{"Check that /nonexistent/tsrelay-test is installed and on your PATH"}, code: "command_failed"},
		{args: []string{"url", "8080"}, status: 1, title: "No hook known here yet",
			reason: "tsrelay url prints what tsrelay http remembered for this directory and target, and it has run for neither yet.",
			fixes:  []string{"tsrelay http 8080"}, code: "no_remembered_hook"},
		{args: []string{"url", "ftp://x"}, status: 2, title: `"ftp://x" isn't a port or an http(s) URL`,
			reason: "tsrelay forwards calls to a port on this machine, or to an http(s) URL.",
			fixes:  []string{"tsrelay url 3000", "tsrelay url http://localhost:3000"}, code: "usage"},
		{args: []string{"hooks", "create", "--channel", "Not Valid"}, status: 1, title: "The relay rejected the values",
			reason: "Something sent is not valid. The relay says: …", fixes: []string{"Correct what the relay named, then retry"}, code: problem.Invalid},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) { tc.check(t, d, "--relay", e.relayURL) })
	}

	t.Run("several targets", func(t *testing.T) {
		d := deps{HTTP: d.HTTP, StatePath: filepath.Join(t.TempDir(), "hooks.json")}
		st, _ := client.LoadState(d.StatePath, "")
		cwd, _ := os.Getwd()
		for _, port := range []string{"3000", "4000"} {
			target, _ := client.ParseTarget(port)
			if err := st.Remember(client.StateKey(e.relayURL, cwd, target), "p"+port, e.relayURL+"/~moritz/p"+port); err != nil {
				t.Fatal(err)
			}
		}
		errorCase{args: []string{"url"}, status: 2, title: "Hooks for several targets here",
			reason: "This directory has hooks for http://localhost:3000, http://localhost:4000; name one.",
			fixes:  []string{"tsrelay url http://localhost:3000", "tsrelay url http://localhost:4000"}, code: "usage",
		}.check(t, d, "--relay", e.relayURL)
	})
	t.Run("unreadable state", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "hooks.json")
		os.WriteFile(path, []byte("{"), 0o600)
		errorCase{args: []string{"url"}, status: 1, title: "Can't read tsrelay's remembered hooks",
			reason: "tsrelay remembers hooks per directory in " + path + ": …",
			fixes:  []string{"Repair the file, or move it aside to start over: mv " + path + " " + path + ".bak"}, code: "state_unreadable",
		}.check(t, deps{StatePath: path}, "--relay", e.relayURL)
	})
	t.Run("forbidden", func(t *testing.T) {
		errorCase{args: []string{"hooks", "create"}, status: 1, title: "The relay refused",
			reason: "Your grants don't allow this. The relay says: Creating hooks requires the receiver role, and only users (not tagged nodes) can own hooks.",
			fixes:  []string{"Ask a relay admin to grant you the role or channel; the relay's web UI shows them the grant"}, code: problem.Forbidden,
		}.check(t, deps{HTTP: tunnel.NewHTTPClient(e.fake.Dial(sender))}, "--relay", e.relayURL)
	})
	t.Run("not identified", func(t *testing.T) {
		e.fake.Fail(errors.New("whois failed"))
		defer e.fake.Fail(nil)
		errorCase{args: []string{"hooks", "list"}, status: 1, title: "The relay couldn't identify you",
			reason: "It asks Tailscale who is calling, and Tailscale didn't know this device.",
			fixes:  []string{"tailscale status", "Reach the relay over Tailscale, not another network"}, code: problem.Forbidden,
		}.check(t, d, "--relay", e.relayURL)
	})
	t.Run("version mismatch", func(t *testing.T) {
		e.restartRelayAt("v9.9.9")
		setVersion(t, "v0.1.0")
		errorCase{args: []string{"hooks", "create", "--channel", "Not Valid"}, status: 1, title: "The relay rejected the values",
			reason: "Something sent is not valid. The relay says: …",
			fixes:  []string{"Correct what the relay named, then retry", updateCommand(e.relayURL)}, code: problem.Invalid,
		}.check(t, d, "--relay", e.relayURL)
		var stderr syncBuf
		run(t.Context(), []string{"hooks", "create", "--channel", "Not Valid", "--relay", e.relayURL}, new(nopWriter), &stderr, d)
		if !strings.Contains(stderr.String(), "This tsrelay (v0.1.0) differs from the relay (v9.9.9), which may be why.") {
			t.Errorf("the reason should name the version mismatch:\n%s", stderr.String())
		}
	})
}

// A relay that cannot be found, reached or trusted is explained with the
// URL tried and where it came from.
func TestNetworkErrorSites(t *testing.T) {
	unsetenv(t, "TSRELAY_RELAY")
	status := func(st *ipnstate.Status, err error) deps {
		return deps{Status: func(context.Context) (*ipnstate.Status, error) { return st, err }}
	}
	errorCase{args: []string{"hooks", "list"}, status: 1, title: "Can't ask Tailscale for your tailnet",
		reason: "tsrelay finds the relay by your tailnet's name, which the local Tailscale didn't give: Connection refused.",
		fixes:  []string{"tailscale up", "Name the relay with --relay https://tsrelay.<your tailnet>.ts.net"}, code: "tailscale_unavailable",
	}.check(t, status(nil, errors.New("connection refused")))
	errorCase{args: []string{"hooks", "list"}, status: 1, title: "Can't tell which relay to use",
		reason: "tsrelay finds the relay at https://tsrelay.<your tailnet>, but this tailnet has no MagicDNS name.",
		fixes:  []string{"Name the relay with --relay https://<relay host>, or set TSRELAY_RELAY"}, code: "no_relay",
	}.check(t, status(&ipnstate.Status{}, nil))
	errorCase{args: []string{"hooks", "list"}, env: map[string]string{"TSRELAY_RELAY": "http://127.0.0.1:1"}, status: 1,
		title: "Can't reach the relay", reason: "Tried http://127.0.0.1:1 (from TSRELAY_RELAY): …",
		fixes: []string{"tailscale status", "Check that the relay is running", "tsrelay hooks list --relay https://tsrelay.<your tailnet>.ts.net"},
		code:  "relay_unreachable",
	}.check(t, deps{})
	unsetenv(t, "TSRELAY_RELAY")

	untrusted := httptest.NewUnstartedServer(http.NotFoundHandler())
	untrusted.Config.ErrorLog = log.New(io.Discard, "", 0) // the refused handshake
	untrusted.StartTLS()
	defer untrusted.Close()
	errorCase{args: []string{"hooks", "list"}, status: 1, title: "The relay's certificate isn't trusted",
		reason: "Verifying " + untrusted.URL + " (from --relay) failed: …",
		fixes:  []string{"Check that the URL names the relay's tailnet host", "tsrelay hooks list --relay https://tsrelay.<your tailnet>.ts.net"},
		code:   "relay_tls",
	}.check(t, deps{}, "--relay", untrusted.URL)

	plain := httptest.NewServer(http.NotFoundHandler())
	defer plain.Close()
	host := strings.TrimPrefix(plain.URL, "http://")
	errorCase{args: []string{"hooks", "list"}, status: 1, title: "The relay doesn't speak HTTPS",
		reason: "https://" + host + " answered in plain HTTP (the URL came from --relay); it may run with --tls=false.",
		fixes:  []string{"tsrelay hooks list --relay http://" + host}, code: "relay_tls",
	}.check(t, deps{}, "--relay", host)

	// A name that does not resolve; built by hand, as real lookups are slow
	// and vary by machine.
	ue := &client.UnreachableError{Base: "https://tsrelay.example.ts.net", Err: &url.Error{Op: "Get", URL: "https://tsrelay.example.ts.net/hooks",
		Err: &net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{Name: "tsrelay.example.ts.net", Err: "no such host", IsNotFound: true}}}}
	got := (&cli{cmdPath: "tsrelay hooks list"}).explain(ue)
	want := &cliError{Title: "Can't find the relay", Code: "relay_dns", err: ue,
		Reason: "tsrelay.example.ts.net does not resolve (the URL https://tsrelay.example.ts.net came from your tailnet's MagicDNS name). Tailscale may be down, or MagicDNS off.",
		Fixes:  []string{"tailscale status", "tsrelay hooks list --relay https://tsrelay.<your tailnet>.ts.net"}}
	if got.Title != want.Title || got.Reason != want.Reason || !slices.Equal(got.Fixes, want.Fixes) || got.Code != want.Code || !errors.Is(got, ue) {
		t.Errorf("explain(DNS failure) = %#v, want %#v", got, want)
	}
}

// problemCodes are the error codes internal/problem declares: the string
// constants that look like one. Parsing the source keeps this test right
// whatever codes are added.
func problemCodes(t *testing.T) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), filepath.Join("..", "..", "internal", "problem", "problem.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	looksLikeCode := regexp.MustCompile(`^[a-z]+(_[a-z]+)*$`)
	var codes []string
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			for _, v := range spec.(*ast.ValueSpec).Values {
				if lit, ok := v.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					if s, _ := strconv.Unquote(lit.Value); looksLikeCode.MatchString(s) {
						codes = append(codes, s)
					}
				}
			}
		}
	}
	if !slices.Contains(codes, problem.Forbidden) || !slices.Contains(codes, problem.TunnelLost) {
		t.Fatalf("found codes %q; the parse missed some", codes)
	}
	return codes
}

// Every problem code the relay can return has an explanation.
func TestEveryProblemCodeIsExplained(t *testing.T) {
	for _, code := range problemCodes(t) {
		if x, ok := problemExplanations[code]; !ok || x.title == "" || x.reason == "" || len(x.fixes) == 0 {
			t.Errorf("problem code %q has no explanation (title, reason and fixes) in problemExplanations", code)
		}
	}
}

func TestExplainProblem(t *testing.T) {
	c := &cli{hook: "pay", base: "https://relay"}
	for code, x := range problemExplanations {
		p := &problem.Problem{Status: 400, Code: code, Detail: "something happened"}
		got := c.explain(p)
		if got.Title != x.title || got.Reason != x.reason+" The relay says: Something happened." || got.Code != code || !errors.Is(got, p) {
			t.Errorf("%s: got %#v", code, got)
		}
		for _, f := range got.Fixes {
			if strings.Contains(f, "<hook>") {
				t.Errorf("%s: fix %q keeps a placeholder", code, f)
			}
		}
	}
	if got := c.explain(&problem.Problem{Code: problem.RateLimited}); !slices.Contains(got.Fixes, "tsrelay hooks update pay --rate-limit 100/200") {
		t.Errorf("fixes should name the hook given: %q", got.Fixes)
	}
	if got := c.explain(&problem.Problem{Code: problem.BadRequest}); !slices.Equal(got.Fixes, []string{updateCommand("https://relay")}) {
		t.Errorf("fixes should install the relay's version: %q", got.Fixes)
	}
}

// sampleErrors are one of each kind of explained error, for the golden
// files.
func sampleErrors() []*cliError {
	c := &cli{cmdPath: "tsrelay hooks list", relayFrom: "--relay", hook: "stripe"}
	return []*cliError{
		usage("No target to forward calls to", "attach needs to know where your local app listens.",
			"tsrelay attach stripe --to 3000", "tsrelay attach stripe --to http://localhost:8080"),
		c.explain(&problem.Problem{Status: 403, Code: problem.Forbidden, Detail: `your grants no longer cover channel "prod"`}),
		c.explain(&client.UnreachableError{Base: "https://tsrelay.example.ts.net", Err: errors.New("connection refused")}),
		{Title: "No hook \"nope\"", Code: "no_such_hook"},
	}
}

func TestErrorGolden(t *testing.T) {
	forceTerminal(t)
	t.Setenv("TTY_FORCE", "0")
	var text, js bytes.Buffer
	s := newStream(&text, false, false)
	for _, e := range sampleErrors() {
		fmt.Fprintln(s.w, formatError(s, e)+"\n")
		writeJSON(newStream(&js, false, false), newJSONError(e))
	}
	if strings.Contains(text.String(), "\x1b") {
		t.Errorf("plain errors contain escapes:\n%q", text.String())
	}
	golden(t, "errors-text", text.String())
	golden(t, "errors-json", js.String())

	t.Setenv("TTY_FORCE", "1")
	fancy := formatError(newStream(&bytes.Buffer{}, false, false), sampleErrors()[0])
	if !strings.Contains(fancy, "\x1b[") || ansi.Strip(fancy) != formatError(newStream(&bytes.Buffer{}, true, false), sampleErrors()[0]) {
		t.Errorf("styled errors should be the plain text with escapes:\n%q", fancy)
	}
}
