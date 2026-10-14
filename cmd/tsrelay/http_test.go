package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/Radiergummi/tsrelay/internal/audit"
	"github.com/Radiergummi/tsrelay/internal/client"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/identify"
	"github.com/Radiergummi/tsrelay/internal/problem"
	"github.com/Radiergummi/tsrelay/internal/server"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
)

var (
	receiver = identify.User("laptop", "moritz@example.com", 1, `{"roles":["receiver"]}`)
	sender   = identify.Tagged("worker", []string{"tag:prod-worker"}, `{"roles":["sender"]}`)
)

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

type cliEnv struct {
	t         *testing.T
	fake      *identify.Fake
	relayURL  string
	statePath string
	local     *httptest.Server
	calls     chan string
	srv       *server.Server
	mgr       *hooks.Manager
	offline   atomic.Bool // while set, the CLI's new connections fail

	handlerMu sync.Mutex
	handler   http.Handler // swapped by restartRelay to simulate a relay restart
}

func newCLIEnv(t *testing.T) *cliEnv {
	e := &cliEnv{t: t, fake: &identify.Fake{}, statePath: filepath.Join(t.TempDir(), "state.json"), calls: make(chan string, 8)}
	store, _ := hooks.OpenJSON(t.TempDir())
	t.Cleanup(func() { store.Close() })
	e.mgr, _ = hooks.NewManager(context.Background(), store, hooks.SlugConfig{})
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.handlerMu.Lock()
		h := e.handler
		e.handlerMu.Unlock()
		h.ServeHTTP(w, r)
	}))
	e.relayURL = "http://" + ts.Listener.Addr().String()
	e.srv = server.New(server.Config{BaseURL: e.relayURL, AllowedHosts: []string{"127.0.0.1"}},
		e.fake, e.mgr, audit.New(slog.New(slog.NewJSONHandler(io.Discard, nil))))
	e.handler = e.srv.Handler()
	ts.Start()
	t.Cleanup(func() { e.srv.Close(); ts.Close() })
	e.local = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.calls <- r.Method + " " + r.URL.Path
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(e.local.Close)
	return e
}

// restartRelay replaces the relay with a fresh instance over the same hook
// store, as a process restart would: the new instance's hook-updated
// sequence counter starts over at 0, which can be lower than one the CLI
// already saw from the instance before it.
func (e *cliEnv) restartRelay() {
	// Held for the whole swap: otherwise a reconnect raced between Close
	// and the new instance replacing e.handler could attach to the old,
	// already-closing instance's registry, where it would be stranded.
	e.handlerMu.Lock()
	defer e.handlerMu.Unlock()
	e.srv.Close() // drops the old tunnel so the CLI reconnects
	srv := server.New(server.Config{BaseURL: e.relayURL, AllowedHosts: []string{"127.0.0.1"}},
		e.fake, e.mgr, audit.New(slog.New(slog.NewJSONHandler(io.Discard, nil))))
	e.srv, e.handler = srv, srv.Handler()
}

type cliRun struct {
	stdout, stderr *syncBuf
	cancel         context.CancelFunc
	exit           chan int
	hc             *http.Client
}

func (e *cliEnv) start(extra ...string) *cliRun {
	return e.startCmd(append([]string{"http", e.local.URL, "--relay", e.relayURL}, extra...)...)
}

// startCmd runs tsrelay with args in the background. Its new connections
// fail while e.offline is set.
func (e *cliEnv) startCmd(args ...string) *cliRun {
	ctx, cancel := context.WithCancel(context.Background())
	dial := e.fake.Dial(receiver)
	hc := tunnel.NewHTTPClient(func(ctx context.Context, network, addr string) (net.Conn, error) {
		if e.offline.Load() {
			return nil, errors.New("offline")
		}
		return dial(ctx, network, addr)
	})
	r := &cliRun{stdout: &syncBuf{}, stderr: &syncBuf{}, cancel: cancel, exit: make(chan int, 1), hc: hc}
	d := deps{HTTP: hc, StatePath: e.statePath}
	go func() { r.exit <- run(ctx, args, r.stdout, r.stderr, d) }()
	e.t.Cleanup(cancel)
	return r
}

// api is a relay API client acting as the CLI's user, for test setup.
func (e *cliEnv) api() *client.API {
	return &client.API{BaseURL: e.relayURL, HTTP: tunnel.NewHTTPClient(e.fake.Dial(receiver))}
}

// waitOutput waits until b contains want.
func waitOutput(t *testing.T, b *syncBuf, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(b.String(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("%q was not printed; output:\n%s", want, b)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// url waits until the CLI is attached and returns the URL it printed.
func (e *cliEnv) url(r *cliRun) string {
	e.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(r.stderr.String(), "connected") {
		if time.Now().After(deadline) {
			e.t.Fatalf("CLI did not attach; stderr:\n%s", r.stderr)
		}
		time.Sleep(5 * time.Millisecond)
	}
	return strings.TrimSpace(r.stdout.String())
}

func (e *cliEnv) send(url string) int {
	e.t.Helper()
	resp, err := e.fake.Client(sender).Post(url+"/done", "application/json", strings.NewReader("{}"))
	if err != nil {
		e.t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func waitExit(t *testing.T, r *cliRun) int {
	t.Helper()
	select {
	case code := <-r.exit:
		return code
	case <-time.After(5 * time.Second):
		t.Fatalf("CLI did not exit; stderr:\n%s", r.stderr)
		return -1
	}
}

func TestHTTPForwardsAndLogs(t *testing.T) {
	e := newCLIEnv(t)
	r := e.start()
	url := e.url(r)
	if !strings.HasPrefix(url, e.relayURL+"/~moritz/") || strings.Count(url, "\n") != 0 {
		t.Fatalf("stdout = %q, want exactly the hook URL", url)
	}
	if code := e.send(url); code != http.StatusAccepted {
		t.Fatalf("send = %d", code)
	}
	if got := <-e.calls; got != "POST /done" {
		t.Fatalf("local got %q", got)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(r.stderr.String(), "POST /done → 202") {
		if time.Now().After(deadline) {
			t.Fatalf("call not logged; stderr:\n%s", r.stderr)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !strings.Contains(r.stderr.String(), "tag:prod-worker") {
		t.Errorf("call line should name the sender:\n%s", r.stderr)
	}
	if !strings.Contains(r.stderr.String(), e.relayURL+"/inspect/c_") {
		t.Errorf("call line should link to the inspector:\n%s", r.stderr)
	}
	r.cancel()
	if code := waitExit(t, r); code != 0 {
		t.Fatalf("exit after Ctrl-C = %d", code)
	}
}

// Restarting the CLI must print the same URL, so it can live in a .env file.
func TestHTTPReusesURLAcrossRestarts(t *testing.T) {
	e := newCLIEnv(t)
	r1 := e.start()
	first := e.url(r1)
	r1.cancel()
	waitExit(t, r1)

	r2 := e.start()
	if second := e.url(r2); second != first {
		t.Fatalf("URL changed across restarts: %q → %q", first, second)
	}
	if code := e.send(first); code != http.StatusAccepted {
		t.Fatalf("send after restart = %d", code)
	}
}

func TestHTTPExplicitName(t *testing.T) {
	e := newCLIEnv(t)
	r := e.start("--name", "payments/v1")
	if url := e.url(r); url != e.relayURL+"/~moritz/payments/v1" {
		t.Fatalf("url = %q", url)
	}
}

// A second CLI on the same hook takes it over; the first must exit instead
// of reconnecting and stealing it back.
func TestHTTPTakeover(t *testing.T) {
	e := newCLIEnv(t)
	r1 := e.start("--name", "shared")
	url := e.url(r1)
	r2 := e.start("--name", "shared")
	e.url(r2)
	if code := waitExit(t, r1); code != 1 {
		t.Fatalf("replaced CLI exit = %d, want 1", code)
	}
	if !strings.Contains(r1.stderr.String(), "another") {
		t.Errorf("replaced CLI should explain why it stopped:\n%s", r1.stderr)
	}
	if code := e.send(url); code != http.StatusAccepted {
		t.Fatalf("send after takeover = %d", code)
	}
}

func TestHTTPRelayRefusal(t *testing.T) {
	e := newCLIEnv(t)
	r := e.start("--channel", "Not Valid")
	if code := waitExit(t, r); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(r.stderr.String(), "channel") {
		t.Errorf("stderr should carry the relay's reason:\n%s", r.stderr)
	}
}

// Reusing a hook whose channel or persistence differs from the flags would
// silently ignore them; the CLI must say so instead.
// Minor 2: with --json, a relay refusal must also land on stderr as JSON,
// carrying the problem, not only flattened into the stopped event on stdout.
func TestHTTPRelayRefusalJSON(t *testing.T) {
	e := newCLIEnv(t)
	r := e.start("--channel", "Not Valid", "--json")
	if code := waitExit(t, r); code != 1 {
		t.Fatalf("exit %d, want 1\n%s", code, r.stderr)
	}
	var je struct {
		Error   string           `json:"error"`
		Problem *problem.Problem `json:"problem"`
	}
	if err := json.Unmarshal([]byte(r.stderr.String()), &je); err != nil || je.Problem == nil || je.Problem.Status != 422 {
		t.Fatalf("stderr should be a JSON error carrying the problem: %v\n%s", err, r.stderr)
	}
	lines := strings.Split(strings.TrimSpace(r.stdout.String()), "\n")
	var last jsonEvent
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &last); err != nil || last.Event != "stopped" {
		t.Fatalf("stdout's last line should be the stopped event: %v\n%s", err, r.stdout)
	}
}

func TestHTTPRefusesMismatchedReuse(t *testing.T) {
	e := newCLIEnv(t)
	r1 := e.start("--name", "cb")
	e.url(r1)
	r1.cancel()
	waitExit(t, r1)

	for flag, want := range map[string]string{"--channel=prod": "channel", "--persistent": "ephemeral"} {
		r := e.start("--name", "cb", flag)
		if code := waitExit(t, r); code != 1 {
			t.Fatalf("%s: exit = %d, want 1", flag, code)
		}
		if out := r.stderr.String(); !strings.Contains(out, want) || !strings.Contains(out, "cb") {
			t.Errorf("%s: stderr should explain the mismatch:\n%s", flag, out)
		}
	}
	r := e.start("--name", "cb", "--channel=default")
	e.url(r) // matching flags still reuse the hook
}

// Minor 9: reusing an existing hook silently ignored settings flags; it must
// now warn, naming the hook and pointing at hooks update.
func TestHTTPWarnsSettingsIgnoredOnReuse(t *testing.T) {
	e := newCLIEnv(t)
	r1 := e.start("--name", "cb")
	e.url(r1)
	r1.cancel()
	waitExit(t, r1)

	r2 := e.start("--name", "cb", "--hold-timeout", "5s")
	e.url(r2)
	if out := r2.stderr.String(); !strings.Contains(out, "settings flags apply only when the hook is created") ||
		!strings.Contains(out, "tsrelay hooks update") || !strings.Contains(out, "cb") {
		t.Errorf("stderr should warn that settings were ignored:\n%s", out)
	}
	r2.cancel()
	waitExit(t, r2)
}

// The same reuse must not warn when no settings flag was given.
func TestHTTPNoWarningWithoutSettingsFlags(t *testing.T) {
	e := newCLIEnv(t)
	r1 := e.start("--name", "cb")
	e.url(r1)
	r1.cancel()
	waitExit(t, r1)

	r2 := e.start("--name", "cb")
	e.url(r2)
	if strings.Contains(r2.stderr.String(), "settings flags apply only when the hook is created") {
		t.Errorf("no settings flag was given; stderr should not warn:\n%s", r2.stderr)
	}
	r2.cancel()
	waitExit(t, r2)
}

// Minor 12: a hook the relay reaped during a long disconnect is recreated
// at the same URL instead of the CLI giving up.
func TestHTTPRecreatesReapedHook(t *testing.T) {
	e := newCLIEnv(t)
	r := e.start()
	hookURL := e.url(r)
	ctx := context.Background()
	hs, err := e.api().ListHooks(ctx)
	if err != nil || len(hs) != 1 {
		t.Fatalf("hooks = %v, %v", hs, err)
	}

	e.offline.Store(true)
	r.hc.CloseIdleConnections()
	e.srv.Close() // drops the tunnel; the CLI cannot redial while offline
	waitOutput(t, r.stderr, "tunnel lost")
	if err := e.api().DeleteHook(ctx, hs[0].ID); err != nil { // what the reaper does
		t.Fatal(err)
	}
	e.offline.Store(false)

	waitOutput(t, r.stderr, "recreated at the same URL")
	if code := e.send(hookURL); code != http.StatusAccepted {
		t.Fatalf("send to the recreated hook = %d", code)
	}
	if got := <-e.calls; got != "POST /done" {
		t.Fatalf("local got %q", got)
	}
	select {
	case code := <-r.exit:
		t.Fatalf("CLI exited with %d; stderr:\n%s", code, r.stderr)
	default:
	}
}

func TestHTTPStopsWhenHookDeleted(t *testing.T) {
	e := newCLIEnv(t)
	r := e.start()
	e.url(r)
	ctx := context.Background()
	hs, err := e.api().ListHooks(ctx)
	if err != nil || len(hs) != 1 {
		t.Fatalf("hooks = %v, %v", hs, err)
	}
	if err := e.api().DeleteHook(ctx, hs[0].ID); err != nil {
		t.Fatal(err)
	}
	if code := waitExit(t, r); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(r.stderr.String(), "was deleted") {
		t.Errorf("CLI should say the hook was deleted:\n%s", r.stderr)
	}
}

// Renaming the hook elsewhere prints its new URL and remembers it.
func TestHTTPFollowsHookUpdate(t *testing.T) {
	e := newCLIEnv(t)
	r := e.start("--name", "old")
	oldURL := e.url(r)
	ctx := context.Background()
	hs, err := e.api().ListHooks(ctx)
	if err != nil || len(hs) != 1 {
		t.Fatalf("hooks = %v, %v", hs, err)
	}
	h, err := e.api().UpdateHook(ctx, hs[0].ID, hooks.UpdateRequest{Path: hooks.Opt[string]{Set: true, Value: new("new")}})
	if err != nil {
		t.Fatal(err)
	}
	waitOutput(t, r.stdout, h.URL)
	if out := r.stdout.String(); out != oldURL+"\n"+h.URL+"\n" {
		t.Fatalf("stdout = %q, want the old and then the new URL", out)
	}
	st, err := client.LoadState(e.statePath, "")
	if err != nil {
		t.Fatal(err)
	}
	cwd, _ := os.Getwd()
	target, _ := client.ParseTarget(e.local.URL)
	key := client.StateKey(e.relayURL, cwd, target)
	if st.Path(key) != "new" || st.URL(key) != h.URL {
		t.Fatalf("state = %q %q, want new %s", st.Path(key), st.URL(key), h.URL)
	}
	// An update that keeps the URL prints nothing.
	if _, err := e.api().UpdateHook(ctx, h.ID, hooks.UpdateRequest{Description: hooks.Opt[string]{Set: true, Value: new("x")}}); err != nil {
		t.Fatal(err)
	}
	if code := e.send(h.URL); code != http.StatusAccepted {
		t.Fatalf("send to the new URL = %d", code)
	}
	if out := r.stdout.String(); out != oldURL+"\n"+h.URL+"\n" {
		t.Fatalf("stdout = %q after an update that kept the URL", out)
	}
}

// Fix round 1 (Task 3): a relay restart resets its hook-updated sequence
// counter to 0, which can be lower than one the CLI already saw from
// before the restart. The CLI must still apply it, because it belongs to
// a new connection (attached precedes it), not to the one the old
// sequence number was tracking.
func TestHTTPAppliesLowerSeqAfterReconnect(t *testing.T) {
	e := newCLIEnv(t)
	r := e.start("--name", "old")
	e.url(r)
	ctx := context.Background()
	hs, err := e.api().ListHooks(ctx)
	if err != nil || len(hs) != 1 {
		t.Fatalf("hooks = %v, %v", hs, err)
	}
	id := hs[0].ID

	// Run the relay's sequence counter up before the restart.
	for _, p := range []string{"a", "b", "c"} {
		if _, err := e.api().UpdateHook(ctx, id, hooks.UpdateRequest{Path: hooks.Opt[string]{Set: true, Value: new(p)}}); err != nil {
			t.Fatal(err)
		}
	}
	waitOutput(t, r.stdout, "/c")

	e.restartRelay()
	waitOutput(t, r.stderr, "reconnected")

	// The new relay's sequence starts over at 1, lower than the 3 the CLI
	// already saw, but it must still be applied.
	h, err := e.api().UpdateHook(ctx, id, hooks.UpdateRequest{Path: hooks.Opt[string]{Set: true, Value: new("new")}})
	if err != nil {
		t.Fatal(err)
	}
	waitOutput(t, r.stdout, h.URL)
}

func TestDropMessage(t *testing.T) {
	const u = "https://r/~m/cb"
	for typ, want := range map[tunnel.ControlType]string{
		tunnel.ControlReplaced:    "another tsrelay process",
		tunnel.ControlRevoked:     "grants no longer allow",
		tunnel.ControlHookDeleted: "was deleted",
	} {
		d, ok := drops[typ]
		if !ok {
			t.Errorf("%s: no drop message", typ)
			continue
		}
		if msg := fmt.Sprintf(d.text, u); !strings.Contains(msg, want) || !strings.Contains(msg, u) {
			t.Errorf("%s: %q", typ, msg)
		}
	}
	if _, ok := drops[tunnel.ControlAttached]; ok {
		t.Error("attached does not end delivery")
	}
}

func TestHTTPJSON(t *testing.T) {
	e := newCLIEnv(t)
	r := e.start("--json")
	waitOutput(t, r.stdout, `"event":"connected"`)
	var first jsonEvent
	if err := json.Unmarshal([]byte(strings.SplitN(r.stdout.String(), "\n", 2)[0]), &first); err != nil || first.Event != "url" || first.Hook == "" {
		t.Fatalf("first event = %+v, %v", first, err)
	}
	if code := e.send(first.URL); code != http.StatusAccepted {
		t.Fatalf("send = %d", code)
	}
	waitOutput(t, r.stdout, `"event":"call"`)
	r.cancel()
	if code := waitExit(t, r); code != 0 {
		t.Fatalf("exit = %d", code)
	}

	var call, last jsonEvent
	for _, line := range strings.Split(strings.TrimSpace(r.stdout.String()), "\n") {
		var ev jsonEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("stdout line is not JSON: %q", line)
		}
		if ev.Event == "call" {
			call = ev
		}
		last = ev
	}
	if call.Hook != first.Hook || call.Method != "POST" || call.Path != "/done" || call.Status != 202 || call.Sender != "tag:prod-worker" {
		t.Errorf("call event = %+v", call)
	}
	if last.Event != "stopped" || last.Reason != "interrupted" || last.ExitCode == nil || *last.ExitCode != 0 {
		t.Errorf("last event = %+v", last)
	}
	if r.stderr.String() != "" {
		t.Errorf("--json wrote to stderr:\n%s", r.stderr)
	}
}

// TestHelperChild is the command the "-- cmd" tests start: it records its
// PID, TSRELAY_URL and the arguments it received, then sleeps or exits as
// TSRELAY_TEST_CHILD says.
func TestHelperChild(t *testing.T) {
	mode := os.Getenv("TSRELAY_TEST_CHILD")
	if mode == "" {
		t.Skip("runs only as the child of the -- cmd tests")
	}
	out := os.Getenv("TSRELAY_TEST_CHILD_OUT")
	report := fmt.Sprintf("%d %s %s", os.Getpid(), os.Getenv("TSRELAY_URL"), strings.Join(os.Args[1:], " "))
	if err := os.WriteFile(out+".tmp", []byte(report), 0o600); err != nil {
		os.Exit(10)
	}
	if err := os.Rename(out+".tmp", out); err != nil {
		os.Exit(10)
	}
	if mode == "exit3" {
		os.Exit(3)
	}
	time.Sleep(time.Minute)
	os.Exit(0)
}

// startChild runs tsrelay http with the test binary as its "-- cmd" child,
// in the given TestHelperChild mode. The child reports to the returned file.
func (e *cliEnv) startChild(mode string) (*cliRun, string) {
	out := filepath.Join(e.t.TempDir(), "child")
	e.t.Setenv("TSRELAY_TEST_CHILD", mode)
	e.t.Setenv("TSRELAY_TEST_CHILD_OUT", out)
	return e.start("--", os.Args[0], "-test.run=^TestHelperChild$"), out
}

// readChild waits for the child's report and returns its PID, TSRELAY_URL
// and the arguments it received after "--".
func readChild(t *testing.T, path string) (pid int, url, args string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		b, err := os.ReadFile(path)
		if err == nil {
			if _, err := fmt.Sscanf(string(b), "%d %s %s", &pid, &url, &args); err == nil {
				return pid, url, args
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("child did not report: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Review focus: Ctrl-C must stop the "-- cmd" child, not leave it running.
func TestHTTPCommandStoppedOnInterrupt(t *testing.T) {
	e := newCLIEnv(t)
	r, report := e.startChild("sleep")
	hookURL := e.url(r)
	pid, childURL, childArgs := readChild(t, report)
	if childURL != hookURL {
		t.Fatalf("child got TSRELAY_URL=%q, want %q", childURL, hookURL)
	}
	// Proves that a flag-like argument after "--" reaches the child
	// unparsed, rather than being consumed by tsrelay's own flag parsing.
	if !strings.Contains(childArgs, "-test.run=^TestHelperChild$") {
		t.Fatalf("child args = %q, want it to include -test.run=^TestHelperChild$", childArgs)
	}
	r.cancel() // what Ctrl-C does to run's context
	if code := waitExit(t, r); code != 0 {
		t.Fatalf("exit after Ctrl-C = %d", code)
	}
	if p, err := os.FindProcess(pid); err == nil && p.Signal(syscall.Signal(0)) == nil {
		p.Kill()
		t.Fatalf("the command (pid %d) still runs after tsrelay was interrupted", pid)
	}
}

func TestHTTPCommandExitCode(t *testing.T) {
	e := newCLIEnv(t)
	r, _ := e.startChild("exit3")
	if code := waitExit(t, r); code != 3 {
		t.Fatalf("exit = %d, want the command's 3; stderr:\n%s", code, r.stderr)
	}
	if !strings.Contains(r.stderr.String(), "exited with status 3") {
		t.Errorf("stderr should say why tsrelay stopped:\n%s", r.stderr)
	}
}

func TestHTTPReportsTarget(t *testing.T) {
	e := newCLIEnv(t)
	e.url(e.start())
	hs, err := e.api().ListHooks(context.Background())
	if err != nil || len(hs) != 1 || hs[0].Tunnel == nil || hs[0].Tunnel.Target != e.local.URL {
		t.Fatalf("hooks = %+v, %v; want the tunnel's target %s", hs, err, e.local.URL)
	}
}
