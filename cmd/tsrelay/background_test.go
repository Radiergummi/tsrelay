package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Radiergummi/tsrelay/internal/authz"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/identify"
)

// The background tests start real background tunnels: --background
// re-executes this test binary, which then runs as tsrelay.
func TestMain(m *testing.M) {
	if os.Getenv(backgroundIDEnv) != "" {
		main()
	}
	if os.Getenv("TSRELAY_TEST_SLEEP") != "" { // a stranger for TestStaleRecordSparesReusedPID
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// strangersAre identifies connections the fake doesn't know, such as a
// background tunnel's, as id.
type strangersAre struct {
	*identify.Fake
	id authz.Identity
}

func (s strangersAre) Identify(ctx context.Context, remoteAddr string) (authz.Identity, error) {
	if id, err := s.Fake.Identify(ctx, remoteAddr); err == nil {
		return id, nil
	}
	return s.id, nil
}

// newBackgroundEnv is newCLIEnv for background tunnels: they keep their
// state in a temporary $XDG_STATE_HOME, and the relay takes them for the
// receiver. Tunnels still running when the test ends are killed.
func newBackgroundEnv(t *testing.T) *cliEnv {
	e := newCLIEnv(t)
	e.id = strangersAre{e.fake, receiver}
	e.restartRelay()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Cleanup(func() {
		for _, r := range readRecords(e.runDir()) {
			if p, err := os.FindProcess(r.PID); err == nil && runAlive(e.runDir(), r) {
				_ = p.Kill()
				waitGone(e.runDir(), r, 5*time.Second) // Windows won't remove the log while it is open
			}
		}
	})
	return e
}

func (e *cliEnv) runDir() string {
	return filepath.Join(os.Getenv("XDG_STATE_HOME"), "tsrelay", "run")
}

// A record left behind by a tunnel that is gone, whose pid now belongs to
// another process (a reboot, a reused pid), is exited: ps forgets it, and
// stop leaves that process alone.
func TestStaleRecordSparesReusedPID(t *testing.T) {
	e := newBackgroundEnv(t)
	if err := os.MkdirAll(e.runDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	stranger := exec.Command(os.Args[0], "-test.run=^$")
	stranger.Env = append(os.Environ(), "TSRELAY_TEST_SLEEP=1")
	if err := stranger.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stranger.Process.Kill(); stranger.Wait() })
	r := runRecord{ID: "stale1", PID: stranger.Process.Pid, Started: time.Now()}
	b, _ := json.Marshal(r)
	if err := os.WriteFile(recordPath(e.runDir(), r.ID), b, 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := e.tsrelay("stop", "--all", "--json")
	if code != 0 || !strings.Contains(out, `"exited"`) {
		t.Fatalf("stop: exit %d\n%s%s", code, out, errOut)
	}
	if stranger.ProcessState != nil || stranger.Process.Signal(syscall.Signal(0)) != nil {
		t.Fatal("stop killed a process that isn't the tunnel")
	}
}

var backgroundID = regexp.MustCompile(`running in the background as (\w+)`)

// background starts a background tunnel with args and returns its record.
func (e *cliEnv) background(args ...string) runRecord {
	e.t.Helper()
	code, out, errOut := e.tsrelay(append(args, "--background")...)
	m := backgroundID.FindStringSubmatch(errOut)
	if code != 0 || m == nil {
		e.t.Fatalf("%v --background: exit %d\nstdout:\n%s\nstderr:\n%s", args, code, out, errOut)
	}
	return e.record(m[1])
}

func (e *cliEnv) record(id string) runRecord {
	e.t.Helper()
	b, err := os.ReadFile(recordPath(e.runDir(), id))
	if err != nil {
		e.t.Fatal(err)
	}
	var r runRecord
	if err := json.Unmarshal(b, &r); err != nil {
		e.t.Fatal(err)
	}
	return r
}

// waitLog waits until the log of the background tunnel id contains want.
func (e *cliEnv) waitLog(id, want string) {
	e.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		b, _ := os.ReadFile(logPath(e.runDir(), id))
		if strings.Contains(string(b), want) {
			return
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("%q was not logged; log:\n%s", want, b)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// assertGone fails unless the background tunnel r has exited and left no
// record or log.
func (e *cliEnv) assertGone(r runRecord) {
	e.t.Helper()
	if runAlive(e.runDir(), r) {
		e.t.Errorf("process %d of %s still runs", r.PID, r.ID)
	}
	for _, p := range []string{recordPath(e.runDir(), r.ID), logPath(e.runDir(), r.ID), lockPath(e.runDir(), r.ID)} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			e.t.Errorf("%s is left behind (%v)", p, err)
		}
	}
}

func (e *cliEnv) assertRunDirEmpty() {
	e.t.Helper()
	if entries, _ := os.ReadDir(e.runDir()); len(entries) > 0 {
		e.t.Errorf("run directory not empty: %v", entries)
	}
}

func TestBackgroundAttach(t *testing.T) {
	e := newBackgroundEnv(t)
	a, err := e.api().CreateHook(context.Background(), hooks.CreateRequest{Path: "pay"})
	if err != nil {
		t.Fatal(err)
	}
	code, out, errOut := e.tsrelay("attach", "pay", "--to", e.local.URL, "--background")
	if code != 0 || out != a.URL+"\n" {
		t.Fatalf("exit %d, stdout %q, want the hook URL\n%s", code, out, errOut)
	}
	m := backgroundID.FindStringSubmatch(errOut)
	if m == nil || !strings.Contains(errOut, "tsrelay stop "+m[1]) {
		t.Fatalf("stderr should name the background ID and how to stop it:\n%s", errOut)
	}
	r := e.record(m[1])
	if r.PID == os.Getpid() || !runAlive(e.runDir(), r) {
		t.Fatalf("pid %d should be a running process of its own", r.PID)
	}
	want := []runHook{{ID: a.ID, Path: a.Path, URL: a.URL}}
	if !slices.Equal(r.Hooks, want) || r.Relay != e.relayURL || r.Target != e.local.URL || r.Args[0] != "attach" || time.Since(r.Started) > time.Minute {
		t.Fatalf("record = %+v", r)
	}

	if code := e.send(a.URL); code != http.StatusAccepted {
		t.Fatalf("send = %d", code)
	}
	e.waitLog(r.ID, "connected")
	e.waitLog(r.ID, "POST /done → 202")

	code, out, errOut = e.tsrelay("ps", "--json")
	var ps []psEntry
	if err := json.Unmarshal([]byte(out), &ps); code != 0 || err != nil || len(ps) != 1 || ps[0].ID != r.ID || ps[0].State != "running" {
		t.Fatalf("ps --json: exit %d, %v\n%s%s", code, err, out, errOut)
	}
	if code, out, _ := e.tsrelay("ps"); code != 0 || !strings.Contains(out, r.ID+"\t"+a.URL+"\t"+e.local.URL) {
		t.Fatalf("ps: exit %d\n%s", code, out)
	}

	if code, _, errOut := e.tsrelay("stop", r.ID); code != 0 || !strings.Contains(errOut, "stopped "+r.ID) {
		t.Fatalf("stop: exit %d\n%s", code, errOut)
	}
	e.assertGone(r)
}

// A hook renamed while a background tunnel serves it is found by its new
// path, and ps shows its new URL.
func TestBackgroundFollowsRename(t *testing.T) {
	e := newBackgroundEnv(t)
	ctx := context.Background()
	a, err := e.api().CreateHook(ctx, hooks.CreateRequest{Path: "pay"})
	if err != nil {
		t.Fatal(err)
	}
	r := e.background("attach", "pay", "--to", e.local.URL)
	e.waitLog(r.ID, "connected")
	moved, err := e.api().UpdateHook(ctx, a.ID, hooks.UpdateRequest{Path: hooks.Opt[string]{Set: true, Value: new("billing")}})
	if err != nil {
		t.Fatal(err)
	}
	e.waitLog(r.ID, moved.URL)
	want := []runHook{{ID: a.ID, Path: "billing", URL: moved.URL}}
	deadline := time.Now().Add(5 * time.Second)
	for !slices.Equal(e.record(r.ID).Hooks, want) {
		if time.Now().After(deadline) {
			t.Fatalf("record hooks = %+v, want %+v", e.record(r.ID).Hooks, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if code, _, errOut := e.tsrelay("stop", "billing"); code != 0 || !strings.Contains(errOut, "stopped "+r.ID) {
		t.Fatalf("stop by the new path: exit %d\n%s", code, errOut)
	}
}

func TestBackgroundStop(t *testing.T) {
	e := newBackgroundEnv(t)
	ctx := context.Background()
	for _, p := range []string{"two", "three"} {
		if _, err := e.api().CreateHook(ctx, hooks.CreateRequest{Path: p}); err != nil {
			t.Fatal(err)
		}
	}
	// --json prints the record.
	code, out, errOut := e.tsrelay("http", e.local.URL, "--name", "one", "--background", "--json")
	var one runRecord
	if err := json.Unmarshal([]byte(out), &one); code != 0 || err != nil || one.ID == "" {
		t.Fatalf("http --background --json: exit %d, %v\n%s%s", code, err, out, errOut)
	}
	if got := e.record(one.ID); !slices.Equal(got.Hooks, one.Hooks) || got.PID != one.PID {
		t.Fatalf("printed %+v, recorded %+v", one, got)
	}
	two := e.background("attach", "two", "--to", e.local.URL)
	three := e.background("attach", "three", "--to", e.local.URL)

	// IDs and hook paths complete; those already named don't.
	var compl syncBuf
	run(ctx, []string{"__complete", "stop", two.ID, ""}, &compl, &compl, deps{})
	for _, w := range []string{one.ID, one.Hooks[0].Path, three.ID, three.Hooks[0].Path} {
		if !strings.Contains(compl.String(), w+"\t") {
			t.Errorf("completion lacks %q:\n%s", w, compl.String())
		}
	}
	if strings.Contains(compl.String(), two.Hooks[0].Path+"\t") {
		t.Errorf("completion offers %q, which is named already:\n%s", two.Hooks[0].Path, compl.String())
	}

	// By hook.
	if code, _, errOut := e.tsrelay("stop", one.Hooks[0].Path); code != 0 || !strings.Contains(errOut, "stopped "+one.ID) {
		t.Fatalf("stop by hook: exit %d\n%s", code, errOut)
	}
	e.assertGone(one)
	if !runAlive(e.runDir(), two) || !runAlive(e.runDir(), three) {
		t.Fatal("stopping one tunnel stopped others")
	}
	// By ID.
	if code, _, errOut := e.tsrelay("stop", two.ID); code != 0 {
		t.Fatalf("stop by ID: exit %d\n%s", code, errOut)
	}
	e.assertGone(two)
	// All.
	if code, _, errOut := e.tsrelay("stop", "--all"); code != 0 || !strings.Contains(errOut, "stopped "+three.ID) {
		t.Fatalf("stop --all: exit %d\n%s", code, errOut)
	}
	e.assertGone(three)
	if code, out, _ := e.tsrelay("ps", "--json"); code != 0 || strings.TrimSpace(out) != "[]" {
		t.Fatalf("ps after stop --all: exit %d\n%s", code, out)
	}
}

func TestBackgroundPsCleansStale(t *testing.T) {
	e := newBackgroundEnv(t)
	dead := exec.Command(os.Args[0], "-test.run=^$")
	if err := dead.Run(); err != nil {
		t.Fatal(err)
	}
	r := runRecord{ID: "gone01", PID: dead.ProcessState.Pid(), Args: []string{"attach", "pay"}, Target: "http://localhost:3000",
		Hooks: []runHook{{ID: "h_1", Path: "pay", URL: e.relayURL + "/~moritz/pay"}}, Started: time.Now()}
	b, _ := json.Marshal(r)
	if err := os.MkdirAll(e.runDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(recordPath(e.runDir(), r.ID), b, 0o600)
	os.WriteFile(logPath(e.runDir(), r.ID), []byte("connected\n"), 0o600)

	code, out, errOut := e.tsrelay("ps")
	if code != 0 || !strings.Contains(out, "gone01\t"+r.Hooks[0].URL+"\thttp://localhost:3000\t-\texited") {
		t.Fatalf("ps should list the tunnel once as exited: exit %d\n%s%s", code, out, errOut)
	}
	e.assertGone(r)
	if code, out, _ := e.tsrelay("ps"); code != 0 || strings.Contains(out, "gone01") {
		t.Fatalf("ps lists a forgotten tunnel again:\n%s", out)
	}
}

func TestBackgroundLogs(t *testing.T) {
	e := newBackgroundEnv(t)
	a, err := e.api().CreateHook(context.Background(), hooks.CreateRequest{Path: "pay"})
	if err != nil {
		t.Fatal(err)
	}
	r := e.background("attach", "pay", "--to", e.local.URL)
	e.waitLog(r.ID, "connected")
	if code, out, errOut := e.tsrelay("logs", a.Path); code != 0 || !strings.Contains(out, "connected") {
		t.Fatalf("logs by hook: exit %d\n%s%s", code, out, errOut)
	}

	f := e.startCmd("logs", r.ID, "-f")
	waitOutput(t, f.stdout, "connected")
	if code := e.send(a.URL); code != http.StatusAccepted {
		t.Fatalf("send = %d", code)
	}
	waitOutput(t, f.stdout, "POST /done → 202")
	f.cancel()
	if code := waitExit(t, f); code != 0 {
		t.Fatalf("logs -f after Ctrl-C: exit %d\n%s", code, f.stderr)
	}
}

// A failed attach is reported like a foreground one, and leaves nothing
// behind: startBackground waits for the process, and the run directory is
// empty.
func TestBackgroundFailedAttach(t *testing.T) {
	e := newBackgroundEnv(t)
	code, out, errOut := e.tsrelay("attach", "nope", "--to", e.local.URL, "--background")
	if code != 1 || out != "" || !strings.HasPrefix(errOut, "✗ ") || !strings.Contains(errOut, `"nope"`) || !strings.Contains(errOut, "Try:") {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
	e.assertRunDirEmpty()

	code, _, errOut = e.tsrelay("attach", "nope", "--to", e.local.URL, "--background", "--json")
	var je jsonError
	if err := json.Unmarshal([]byte(errOut), &je); code != 1 || err != nil || !strings.Contains(je.Error.Title, "nope") {
		t.Fatalf("--json: exit %d, %v\n%s", code, err, errOut)
	}
	e.assertRunDirEmpty()
}

func TestBackgroundRefusals(t *testing.T) {
	e := newBackgroundEnv(t)
	for _, tc := range []struct {
		args  []string
		code  int
		title string
	}{
		{[]string{"http", "8080", "--background", "--", "npm", "run", "dev"}, 2, "--background can't start a command"},
		{[]string{"stop"}, 2, "No background tunnel given"},
		{[]string{"stop", "--all", "abc"}, 2, "Both --all and a tunnel given"},
		{[]string{"stop", "nope"}, 1, `No background tunnel "nope"`},
		{[]string{"logs", "nope"}, 1, `No background tunnel "nope"`},
		{[]string{"logs"}, 2, "No background tunnel given"},
	} {
		code, _, errOut := e.tsrelay(tc.args...)
		if code != tc.code || !strings.HasPrefix(errOut, "✗ "+tc.title+"\n") {
			t.Errorf("%v: exit %d, want %d and %q\n%s", tc.args, code, tc.code, tc.title, errOut)
		}
	}
	e.assertRunDirEmpty()
}
