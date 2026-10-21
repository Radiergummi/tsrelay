package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Radiergummi/tsrelay/internal/authz"
	"github.com/Radiergummi/tsrelay/internal/identify"
	"github.com/Radiergummi/tsrelay/internal/release"
	"github.com/Radiergummi/tsrelay/internal/server"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
)

var newBinary = []byte("#!/bin/sh\necho the relay's tsrelay\n")

// assetServer serves release assets as GitHub does: the binary for this
// platform below /download/<version>/, with its checksum file next to it.
// Like GitHub, it redirects the binary on to a signed URL that has no
// checksum file next to it.
type assetServer struct {
	*httptest.Server
	sum  string // what the .sha256 file says
	hits atomic.Int32
}

func newAssetServer(t *testing.T, version string) *assetServer {
	h := sha256.Sum256(newBinary)
	a := &assetServer{sum: hex.EncodeToString(h[:])}
	name := "tsrelay_" + runtime.GOOS + "_" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /download/"+version+"/"+name, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/assets/"+name+"?sig=signed", http.StatusFound)
	})
	mux.HandleFunc("GET /assets/"+name, func(w http.ResponseWriter, r *http.Request) {
		a.hits.Add(1)
		w.Write(newBinary)
	})
	// As the release workflow names it: without .exe.
	mux.HandleFunc("GET /download/"+version+"/"+strings.TrimSuffix(name, ".exe")+".sha256", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, a.sum+"  "+name+"\n")
	})
	a.Server = httptest.NewServer(mux)
	t.Cleanup(a.Close)
	return a
}

// relayWithAssets restarts the relay at version, with its downloads
// redirecting to a fake asset server instead of GitHub.
func (e *cliEnv) relayWithAssets(version string) *assetServer {
	a := newAssetServer(e.t, version)
	e.restartRelayWith(server.Config{Version: version, ReleaseAssets: a.URL})
	return a
}

// oldExecutable is a stand-in for this tsrelay's executable, in a
// directory of its own. Its path has no symlinks (macOS's temporary
// directory has), so it is what update names.
func oldExecutable(t *testing.T) string {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "tsrelay")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	return exe
}

// tsrelayAs runs tsrelay against the relay as who, with stdin as the
// answers and exe as the executable update replaces.
func (e *cliEnv) tsrelayAs(who authz.Identity, exe, stdin string, args ...string) (code int, stdout, stderr string) {
	var out, errOut syncBuf
	d := deps{HTTP: tunnel.NewHTTPClient(e.fake.Dial(who)), StatePath: e.statePath, Executable: exe, Stdin: strings.NewReader(stdin)}
	code = run(context.Background(), slices.Concat(args, []string{"--relay", e.relayURL}), &out, &errOut, d)
	return code, out.String(), errOut.String()
}

func (e *cliEnv) update(exe string, args ...string) (int, string, string) {
	return e.tsrelayAs(receiver, exe, "", append([]string{"update"}, args...)...)
}

// assertUnchanged fails unless exe is still the old executable, alone in
// its directory.
func assertUnchanged(t *testing.T, exe string) {
	t.Helper()
	if b, _ := os.ReadFile(exe); string(b) != "old" {
		t.Errorf("%s was replaced: %q", exe, b)
	}
	if entries, _ := os.ReadDir(filepath.Dir(exe)); len(entries) != 1 {
		t.Errorf("left behind next to the executable: %v", entries)
	}
}

func jsonErrorCode(t *testing.T, stderr string) string {
	t.Helper()
	var je jsonError
	if err := json.Unmarshal([]byte(strings.TrimSpace(stderr)), &je); err != nil {
		t.Fatalf("stderr is not a JSON error: %v\n%s", err, stderr)
	}
	return je.Error.Code
}

func TestUpdateReplacesTheExecutable(t *testing.T) {
	e := newCLIEnv(t)
	e.relayWithAssets("v0.3.0")
	setVersion(t, "v0.2.2")
	exe := oldExecutable(t)
	code, out, errOut := e.update(exe, "--yes")
	if code != 0 || out != "" || !strings.Contains(errOut, "updated tsrelay v0.2.2 to v0.3.0 at "+exe) || strings.Contains(errOut, "warning") {
		t.Fatalf("exit %d\nstdout %q\nstderr %q", code, out, errOut)
	}
	if b, _ := os.ReadFile(exe); !bytes.Equal(b, newBinary) {
		t.Fatalf("executable is %q, want the downloaded binary", b)
	}
	if fi, _ := os.Stat(exe); runtime.GOOS != "windows" && fi.Mode().Perm()&0o111 == 0 {
		t.Errorf("mode %v is not executable", fi.Mode())
	}
	if entries, _ := os.ReadDir(filepath.Dir(exe)); len(entries) != 1 {
		t.Errorf("left behind next to the executable: %v", entries)
	}
}

func TestUpdateFollowsSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	e := newCLIEnv(t)
	e.relayWithAssets("v0.3.0")
	setVersion(t, "v0.2.2")
	exe := oldExecutable(t)
	link := filepath.Join(t.TempDir(), "tsrelay")
	if err := os.Symlink(exe, link); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := e.update(link, "--yes", "--json"); code != 0 {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
	if b, _ := os.ReadFile(exe); !bytes.Equal(b, newBinary) {
		t.Fatalf("the link's target is %q, want the downloaded binary", b)
	}
	if target, err := os.Readlink(link); err != nil || target != exe {
		t.Fatalf("the link now points at %q (%v)", target, err)
	}
}

func TestUpdateAsJSON(t *testing.T) {
	e := newCLIEnv(t)
	e.relayWithAssets("v0.3.0")
	setVersion(t, "v0.2.2")
	exe := oldExecutable(t)
	code, out, errOut := e.update(exe, "--yes", "--json")
	var res updateResult
	if code != 0 || errOut != "" || json.Unmarshal([]byte(out), &res) != nil ||
		res != (updateResult{CLIVersion: "v0.2.2", RelayVersion: "v0.3.0", Updated: true, Path: exe}) {
		t.Fatalf("exit %d\nstdout %q\nstderr %q", code, out, errOut)
	}
}

func TestUpdateAsks(t *testing.T) {
	e := newCLIEnv(t)
	e.relayWithAssets("v0.3.0")
	setVersion(t, "v0.2.2")

	t.Setenv("TTY_FORCE", "1")
	t.Setenv("NO_COLOR", "1")
	exe := oldExecutable(t)
	code, _, errOut := e.tsrelayAs(receiver, exe, "n\n", "update")
	if code != 0 || !strings.Contains(errOut, "Replace tsrelay v0.2.2 at "+exe+" with the relay's v0.3.0? [Y/n]") || !strings.Contains(errOut, "Left tsrelay v0.2.2 as it is") {
		t.Fatalf("exit %d\nstderr %q", code, errOut)
	}
	assertUnchanged(t, exe)
	if code, _, errOut := e.tsrelayAs(receiver, exe, "\n", "update"); code != 0 || !strings.Contains(errOut, "updated tsrelay") {
		t.Fatalf("an empty answer is yes: exit %d\nstderr %q", code, errOut)
	}

	t.Setenv("TTY_FORCE", "0")
	exe = oldExecutable(t)
	code, _, errOut = e.update(exe)
	if code != 2 || !strings.Contains(errOut, "Not updating without confirmation") || !strings.Contains(errOut, "tsrelay update --relay "+e.relayURL+" --yes") {
		t.Fatalf("without a terminal: exit %d\nstderr %q", code, errOut)
	}
	assertUnchanged(t, exe)
}

func TestUpdateChecksumMismatchReplacesNothing(t *testing.T) {
	e := newCLIEnv(t)
	a := e.relayWithAssets("v0.3.0")
	a.sum = strings.Repeat("0", 64)
	setVersion(t, "v0.2.2")
	exe := oldExecutable(t)
	code, _, errOut := e.update(exe, "--yes", "--json")
	if code != 1 || jsonErrorCode(t, errOut) != "checksum_mismatch" || !strings.Contains(errOut, "Nothing was replaced") {
		t.Fatalf("exit %d\nstderr %q", code, errOut)
	}
	assertUnchanged(t, exe)
}

func TestUpdateRefusesAnUnwritableLocation(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a directory this user can't write to")
	}
	e := newCLIEnv(t)
	a := e.relayWithAssets("v0.3.0")
	setVersion(t, "v0.2.2")
	exe := oldExecutable(t)
	dir := filepath.Dir(exe)
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	code, _, errOut := e.update(exe, "--yes")
	want := []string{
		"Can't replace " + exe,
		"A package manager may own it.",
		"sudo " + exe + " update --relay " + e.relayURL,
		"curl -fsSL " + e.relayURL + "/install.sh | sh",
	}
	for _, w := range want {
		if !strings.Contains(errOut, w) {
			t.Errorf("stderr lacks %q", w)
		}
	}
	if code != 1 || a.hits.Load() != 0 {
		t.Fatalf("exit %d, %d downloads\nstderr %q", code, a.hits.Load(), errOut)
	}
	assertUnchanged(t, exe)
}

func TestUpdateWhenAlreadyCurrent(t *testing.T) {
	e := newCLIEnv(t)
	a := e.relayWithAssets("v0.3.0")
	setVersion(t, "v0.3.0")
	exe := oldExecutable(t)
	code, out, errOut := e.update(exe, "--yes")
	if code != 0 || out != "" || !strings.Contains(errOut, "tsrelay v0.3.0 is the relay's version already") {
		t.Fatalf("exit %d\nstdout %q\nstderr %q", code, out, errOut)
	}
	code, out, _ = e.update(exe, "--json")
	var res updateResult
	if code != 0 || json.Unmarshal([]byte(out), &res) != nil || res.Updated || res.RelayVersion != "v0.3.0" {
		t.Fatalf("--json: exit %d, stdout %q", code, out)
	}
	assertUnchanged(t, exe)
	if a.hits.Load() != 0 {
		t.Fatalf("downloaded %d times", a.hits.Load())
	}
}

func TestUpdateCheckOnlyReports(t *testing.T) {
	e := newCLIEnv(t)
	a := e.relayWithAssets("v0.3.0")
	setVersion(t, "v0.2.2")
	exe := oldExecutable(t)
	code, _, errOut := e.update(exe, "--check")
	if code != 0 || !strings.Contains(errOut, "Relay is v0.3.0, you have v0.2.2. Update with: tsrelay update --relay "+e.relayURL) {
		t.Fatalf("exit %d\nstderr %q", code, errOut)
	}
	code, out, _ := e.update(exe, "--check", "--json")
	var res updateResult
	if code != 0 || json.Unmarshal([]byte(out), &res) != nil || res != (updateResult{CLIVersion: "v0.2.2", RelayVersion: "v0.3.0"}) {
		t.Fatalf("--json: exit %d, stdout %q", code, out)
	}
	assertUnchanged(t, exe)
	if a.hits.Load() != 0 {
		t.Fatalf("downloaded %d times", a.hits.Load())
	}
}

func TestUpdateRefusesADevBuild(t *testing.T) {
	e := newCLIEnv(t)
	a := e.relayWithAssets("v0.3.0")
	exe := oldExecutable(t)
	code, _, errOut := e.update(exe, "--yes", "--json")
	if code != 1 || jsonErrorCode(t, errOut) != "dev_build" {
		t.Fatalf("exit %d\nstderr %q", code, errOut)
	}
	assertUnchanged(t, exe)
	if a.hits.Load() != 0 {
		t.Fatalf("downloaded %d times", a.hits.Load())
	}
}

func TestUpdateRefusesADevRelay(t *testing.T) {
	e := newCLIEnv(t)
	setVersion(t, "v0.2.2")
	exe := oldExecutable(t)
	if code, _, errOut := e.update(exe, "--yes", "--json"); code != 1 || jsonErrorCode(t, errOut) != "relay_dev" {
		t.Fatalf("exit %d\nstderr %q", code, errOut)
	}
	assertUnchanged(t, exe)
}

func TestReplaceExecutableOnWindows(t *testing.T) {
	dir := t.TempDir()
	exe, tmp := filepath.Join(dir, "tsrelay.exe"), filepath.Join(dir, ".tsrelay-update-1")
	os.WriteFile(exe, []byte("old"), 0o755)
	os.WriteFile(exe+".old", []byte("from an earlier update"), 0o755)
	os.WriteFile(tmp, []byte("new"), 0o755)
	if err := replaceExecutable("windows", exe, tmp); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "new" {
		t.Fatalf("executable is %q", b)
	}
	if b, _ := os.ReadFile(exe + ".old"); string(b) != "old" {
		t.Fatalf("the running executable should be moved aside, got %q", b)
	}
	removeOldExecutable(exe)
	if entries, _ := os.ReadDir(dir); len(entries) != 1 || entries[0].Name() != "tsrelay.exe" {
		t.Fatalf("after the next run's cleanup: %v", entries)
	}
}

func TestReplaceExecutableOnWindowsRestoresOnFailure(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "tsrelay.exe")
	os.WriteFile(exe, []byte("old"), 0o755)
	if err := replaceExecutable("windows", exe, filepath.Join(dir, "missing")); err == nil {
		t.Fatal("renaming a missing file succeeded")
	}
	if b, _ := os.ReadFile(exe); string(b) != "old" {
		t.Fatalf("the old executable should be back, got %q", b)
	}
}

const updatePrompt = "Relay is v0.3.0, you have v0.2.2. Update now? [Y/n]"

func TestUpdatePromptAtMostOnceADay(t *testing.T) {
	e := newCLIEnv(t)
	e.relayWithAssets("v0.3.0")
	setVersion(t, "v0.2.2")
	t.Setenv("TTY_FORCE", "1")
	t.Setenv("NO_COLOR", "1")
	exe := oldExecutable(t)
	if code, _, errOut := e.tsrelayAs(receiver, exe, "n\n", "hooks", "list"); code != 0 || !strings.Contains(errOut, updatePrompt) {
		t.Fatalf("exit %d\nstderr %q", code, errOut)
	}
	assertUnchanged(t, exe)
	if _, _, errOut := e.tsrelayAs(receiver, exe, "y\n", "hooks", "list"); strings.Contains(errOut, "Update now?") {
		t.Fatalf("prompted twice in a day:\n%s", errOut)
	}

	// A day later it asks again, and a yes updates.
	notices := filepath.Join(filepath.Dir(e.statePath), "notices.json")
	b, _ := json.Marshal(map[string]time.Time{"updatePrompt": time.Now().Add(-25 * time.Hour), "releaseNotice": time.Now()})
	os.WriteFile(notices, b, 0o600)
	code, _, errOut := e.tsrelayAs(receiver, exe, "y\n", "hooks", "list")
	if code != 0 || !strings.Contains(errOut, updatePrompt) || !strings.Contains(errOut, "updated tsrelay v0.2.2 to v0.3.0") {
		t.Fatalf("exit %d\nstderr %q", code, errOut)
	}
	if b, _ := os.ReadFile(exe); !bytes.Equal(b, newBinary) {
		t.Fatalf("executable is %q after a yes", b)
	}
}

func TestUpdatePromptNeedsATerminalWithoutJSON(t *testing.T) {
	e := newCLIEnv(t)
	e.relayWithAssets("v0.3.0")
	setVersion(t, "v0.2.2")
	exe := oldExecutable(t)
	for _, tc := range []struct {
		tty  string
		args []string
	}{
		{"0", []string{"hooks", "list"}},
		{"1", []string{"hooks", "list", "--json"}},
		{"1", []string{"__complete", "hooks", "show", ""}},
	} {
		t.Setenv("TTY_FORCE", tc.tty)
		if _, _, errOut := e.tsrelayAs(receiver, exe, "y\n", tc.args...); strings.Contains(errOut, "Update now?") {
			t.Errorf("TTY_FORCE=%s %v prompted:\n%s", tc.tty, tc.args, errOut)
		}
	}
	assertUnchanged(t, exe)
	if _, err := os.Stat(filepath.Join(filepath.Dir(e.statePath), "notices.json")); err == nil {
		t.Error("notices.json was written, though nothing was offered")
	}
}

// A background tunnel and a completion never ask, whatever their streams.
func TestOfferSkipsBackgroundAndCompletions(t *testing.T) {
	t.Setenv("TTY_FORCE", "1")
	setVersion(t, "v0.2.2")
	statePath := filepath.Join(t.TempDir(), "hooks.json")
	for name, c := range map[string]*cli{
		"background": {bg: &bgChild{id: "k3x9qa"}},
		"completion": {args: []string{"__complete", "attach", ""}},
		"update":     {cmdPath: "tsrelay update"},
		"no relay":   {},
	} {
		var stderr syncBuf
		c.stderr, c.d = &stderr, deps{StatePath: statePath, Stdin: strings.NewReader("y\n")}
		if name != "no relay" {
			c.base = "http://127.0.0.1:1"
		}
		c.version.Store(&versionSeen{relay: "v0.3.0"})
		c.offer(t.Context())
		if stderr.String() != "" {
			t.Errorf("%s: printed %q", name, stderr.String())
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(statePath), "notices.json")); err == nil {
		t.Error("notices.json was written, though nothing was offered")
	}
}

func TestReleaseNoticeForAdmins(t *testing.T) {
	github := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"tag_name":"v0.4.0","html_url":"https://github.com/Radiergummi/tsrelay/releases/tag/v0.4.0"}`)
	}))
	t.Cleanup(github.Close)
	releases := &release.Checker{URL: github.URL}
	releases.Check(t.Context())
	e := newCLIEnv(t)
	e.restartRelayWith(server.Config{Version: "v0.3.0", Releases: releases})
	setVersion(t, "v0.3.0")
	t.Setenv("TTY_FORCE", "1")
	t.Setenv("NO_COLOR", "1")
	admin := identify.User("laptop", "moritz@example.com", 1, `{"roles":["receiver","admin"]}`)
	notice := "tsrelay v0.4.0 is available; this relay runs v0.3.0. Release notes: https://github.com/Radiergummi/tsrelay/releases/tag/v0.4.0\n"

	if _, _, errOut := e.tsrelayAs(receiver, "", "", "hooks", "list"); strings.Contains(errOut, "is available") {
		t.Fatalf("a receiver was told about the release:\n%s", errOut)
	}
	os.Remove(filepath.Join(filepath.Dir(e.statePath), "notices.json"))
	if code, _, errOut := e.tsrelayAs(admin, "", "", "hooks", "list"); code != 0 || !strings.HasSuffix(errOut, notice) {
		t.Fatalf("exit %d\nstderr %q\nwant it to end with %q", code, errOut, notice)
	}
	if _, _, errOut := e.tsrelayAs(admin, "", "", "hooks", "list"); strings.Contains(errOut, "is available") {
		t.Fatalf("told twice in a day:\n%s", errOut)
	}
	os.Remove(filepath.Join(filepath.Dir(e.statePath), "notices.json"))
	t.Setenv("TTY_FORCE", "0")
	if _, _, errOut := e.tsrelayAs(admin, "", "", "hooks", "list"); strings.Contains(errOut, "is available") {
		t.Fatalf("told without a terminal:\n%s", errOut)
	}
}

// The release names a Windows binary's checksum without its .exe.
func TestChecksumURL(t *testing.T) {
	for asset, want := range map[string]string{
		"https://example.com/v1/tsrelay_linux_amd64":       "https://example.com/v1/tsrelay_linux_amd64.sha256",
		"https://example.com/v1/tsrelay_windows_amd64.exe": "https://example.com/v1/tsrelay_windows_amd64.sha256",
	} {
		if got := checksumURL(asset); got != want {
			t.Errorf("checksumURL(%q) = %q, want %q", asset, got, want)
		}
	}
}
