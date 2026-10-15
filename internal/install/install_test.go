package install

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func get(t *testing.T, h *Handler, p string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	h.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
	return rec
}

const releases = "https://github.com/Radiergummi/tsrelay/releases"

func TestDownloadRedirectsToTheRelaysRelease(t *testing.T) {
	h := &Handler{Version: "v0.3.0", BaseURL: "https://tsrelay.example.ts.net"}
	for p, want := range map[string]string{
		"/download/darwin/arm64":  releases + "/download/v0.3.0/tsrelay_darwin_arm64",
		"/download/linux/amd64":   releases + "/download/v0.3.0/tsrelay_linux_amd64",
		"/download/windows/amd64": releases + "/download/v0.3.0/tsrelay_windows_amd64.exe",
	} {
		rec := get(t, h, p)
		if rec.Code != http.StatusFound || rec.Header().Get("Location") != want {
			t.Errorf("%s = %d → %q, want 302 → %q", p, rec.Code, rec.Header().Get("Location"), want)
		}
	}
}

func TestDownloadOnADevRelayUsesTheLatestRelease(t *testing.T) {
	for _, v := range []string{"dev", ""} {
		rec := get(t, &Handler{Version: v}, "/download/linux/arm64")
		if want := releases + "/latest/download/tsrelay_linux_arm64"; rec.Header().Get("Location") != want {
			t.Errorf("version %q → %q, want %q", v, rec.Header().Get("Location"), want)
		}
	}
}

func TestDownloadRejectsUnbuiltTargets(t *testing.T) {
	h := &Handler{Version: "v0.3.0"}
	for _, p := range []string{"/download/freebsd/amd64", "/download/linux/386", "/download/Linux/amd64"} {
		if rec := get(t, h, p); rec.Code != http.StatusNotFound || rec.Header().Get("Content-Type") != "application/problem+json" {
			t.Errorf("%s = %d %s, want a 404 problem", p, rec.Code, rec.Header().Get("Content-Type"))
		}
	}
}

func TestScriptsFetchFromTheRelaysRelease(t *testing.T) {
	for _, tc := range []struct{ version, base string }{
		{"v0.3.0", releases + "/download/v0.3.0"},
		{"dev", releases + "/latest/download"},
	} {
		h := &Handler{Version: tc.version, BaseURL: "https://tsrelay.example.ts.net"}
		for p, ctype := range map[string]string{
			"/install.sh":  "text/x-shellscript; charset=utf-8",
			"/install.ps1": "text/plain; charset=utf-8",
		} {
			rec := get(t, h, p)
			if rec.Code != 200 || rec.Header().Get("Content-Type") != ctype {
				t.Errorf("%s (%s) = %d %s", p, tc.version, rec.Code, rec.Header().Get("Content-Type"))
			}
			if body := rec.Body.String(); !strings.Contains(body, "$base = '"+tc.base+"'") && !strings.Contains(body, "base='"+tc.base+"'") {
				t.Errorf("%s (%s) does not fetch from %s:\n%s", p, tc.version, tc.base, body)
			}
		}
	}
}

func TestScriptsNameANonDefaultRelay(t *testing.T) {
	for base, hint := range map[string]bool{
		"https://tsrelay.example.ts.net": false,
		"https://relay.example.ts.net":   true,
		"http://tsrelay.example.ts.net":  true, // --tls=false: the CLI's default is https
	} {
		h := &Handler{Version: "v0.3.0", BaseURL: base}
		for _, p := range []string{"/install.sh", "/install.ps1"} {
			body := get(t, h, p).Body.String()
			if got := strings.Contains(body, "TSRELAY_RELAY"); got != hint {
				t.Errorf("%s for %s mentions TSRELAY_RELAY = %v, want %v", p, base, got, hint)
			}
			if hint && !strings.Contains(body, "TSRELAY_RELAY', '"+base) && !strings.Contains(body, "TSRELAY_RELAY="+base) {
				t.Errorf("%s for %s does not name the relay", p, base)
			}
		}
	}
}

// TestScriptsPassLinters runs shellcheck on install.sh and parses
// install.ps1 with pwsh. GitHub's Ubuntu runners have both; elsewhere a
// missing tool skips its check, unless CI is set. pwsh -Command
// appends further arguments to the command, so the file comes in $SCRIPT.
func TestScriptsPassLinters(t *testing.T) {
	const parse = `$errs = $null; [System.Management.Automation.Language.Parser]::ParseFile($env:SCRIPT, [ref]$null, [ref]$errs) | Out-Null; if ($errs) { $errs | ForEach-Object { Write-Output $_ }; exit 1 }`
	for _, h := range []*Handler{
		{Version: "v0.3.0", BaseURL: "https://relay.example.ts.net"},
		{Version: "dev", BaseURL: "https://tsrelay.example.ts.net"},
	} {
		dir := t.TempDir()
		for _, tc := range []struct {
			name, tool string
			args       func(file string) []string
		}{
			{"install.sh", "shellcheck", func(f string) []string { return []string{"-s", "sh", f} }},
			{"install.ps1", "pwsh", func(f string) []string { return []string{"-NoProfile", "-NonInteractive", "-Command", parse} }},
		} {
			if _, err := exec.LookPath(tc.tool); err != nil {
				if os.Getenv("CI") != "" {
					t.Fatalf("%s is not installed", tc.tool)
				}
				t.Logf("skipping %s: %s is not installed", tc.name, tc.tool)
				continue
			}
			f := filepath.Join(dir, tc.name)
			if err := os.WriteFile(f, get(t, h, "/"+tc.name).Body.Bytes(), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(tc.tool, tc.args(f)...)
			cmd.Env = append(os.Environ(), "SCRIPT="+f)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("%s (%s, %s): %v\n%s", tc.tool, h.Version, h.BaseURL, err, out)
			}
		}
	}
}

// TestShellScriptInstalls runs install.sh as `curl … | sh` would, against
// a fake release whose "binary" is a shell script.
func TestShellScriptInstalls(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("install.sh is for macOS and Linux")
	}
	bin := []byte("#!/bin/sh\necho tsrelay version v0.3.0\n")
	sum := sha256.Sum256(bin)
	release := func(t *testing.T, sumHex string) string {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if name, ok := strings.CutSuffix(path.Base(r.URL.Path), ".sha256"); ok {
				fmt.Fprintf(w, "%s  %s\n", sumHex, name)
				return
			}
			w.Write(bin)
		}))
		t.Cleanup(srv.Close)
		return srv.URL
	}
	install := func(t *testing.T, releases, home string) (string, error) {
		h := &Handler{Version: "v0.3.0", BaseURL: "https://tsrelay.example.ts.net", Releases: releases}
		cmd := exec.Command("sh")
		cmd.Stdin = strings.NewReader(get(t, h, "/install.sh").Body.String())
		cmd.Env = append(os.Environ(), "HOME="+home)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	existing := func(t *testing.T, home string) string {
		dst := filepath.Join(home, ".local", "bin", "tsrelay")
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, []byte("old"), 0o755); err != nil {
			t.Fatal(err)
		}
		return dst
	}

	t.Run("installs", func(t *testing.T) {
		home := t.TempDir()
		out, err := install(t, release(t, hex.EncodeToString(sum[:])), home)
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		fi, err := os.Stat(filepath.Join(home, ".local", "bin", "tsrelay"))
		if err != nil || fi.Mode()&0o111 == 0 {
			t.Fatalf("installed binary: %v %v", fi, err)
		}
		if !strings.Contains(out, "tsrelay version v0.3.0") || !strings.Contains(out, "export PATH=") {
			t.Errorf("output lacks the version or the PATH hint:\n%s", out)
		}
	})
	t.Run("replaces an existing binary", func(t *testing.T) {
		home := t.TempDir()
		dst := existing(t, home)
		if out, err := install(t, release(t, hex.EncodeToString(sum[:])), home); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if b, _ := os.ReadFile(dst); string(b) != string(bin) {
			t.Errorf("binary = %q, want the new one", b)
		}
	})
	t.Run("refuses a checksum mismatch", func(t *testing.T) {
		home := t.TempDir()
		dst := existing(t, home)
		out, err := install(t, release(t, strings.Repeat("0", 64)), home)
		if err == nil || !strings.Contains(out, "checksum") {
			t.Fatalf("err = %v, want a checksum failure:\n%s", err, out)
		}
		if b, _ := os.ReadFile(dst); string(b) != "old" {
			t.Errorf("the existing binary was replaced: %q", b)
		}
	})
}
