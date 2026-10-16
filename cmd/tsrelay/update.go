package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"charm.land/fang/v2"
	"github.com/Radiergummi/tsrelay/internal/atomicfile"
	"github.com/Radiergummi/tsrelay/internal/client"
	"github.com/spf13/cobra"
)

// offerEvery is how often the update prompt and the release notice may
// each interrupt a command.
const offerEvery = 24 * time.Hour

func newUpdateCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Replace this tsrelay with the relay's version",
		Long: "Replace this tsrelay with the version the relay runs, downloaded from the relay " +
			"and checked against the release's SHA-256 checksum before anything is replaced.",
		Example: "  tsrelay update\n  tsrelay update --check",
		Args:    arity(0, 0, ""),
	}
	check := cmd.Flags().Bool("check", false, "only report whether this tsrelay differs from the relay's version")
	yes := cmd.Flags().BoolP("yes", "y", false, "update without asking")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		return c.update(cmd.Context(), *check, *yes)
	}
	return cmd
}

// updateResult is what tsrelay update prints with --json.
type updateResult struct {
	CLIVersion   string `json:"cliVersion"`
	RelayVersion string `json:"relayVersion"`
	Updated      bool   `json:"updated"`
	Path         string `json:"path,omitempty"` // the executable replaced
}

// update replaces this executable with the relay's version of tsrelay.
// check only reports; yes skips the confirmation.
func (c *cli) update(ctx context.Context, check, yes bool) error {
	c.versionWarned.Do(func() {}) // this command is what the warning suggests
	if version == "dev" {
		return &cliError{Title: "A dev build doesn't update itself", Code: "dev_build",
			Reason: "This tsrelay was built from source, so no release replaces it.",
			Fixes:  []string{"Rebuild it from source", "Install a release as the relay's web UI shows"}}
	}
	relayAPI, err := c.newRelayAPI(ctx)
	if err != nil {
		return err
	}
	me, err := relayAPI.Me(ctx)
	if err != nil {
		return err
	}
	res := updateResult{CLIVersion: version, RelayVersion: me.Version}
	out, errStream := c.streams()
	report := func(msg string) error {
		if c.json {
			return writeJSON(out, res)
		}
		fmt.Fprintln(errStream.w, msg)
		return nil
	}
	switch {
	case me.Version == "dev":
		return &cliError{Title: "The relay is a dev build", Code: "relay_dev",
			Reason: "It runs no release, so there is no version of tsrelay to match.",
			Fixes:  []string{"Ask the relay's operator to run a release"}}
	case me.Version == version:
		return report(errStream.glyph(errStream.style(styleOK, "●")) + fmt.Sprintf("tsrelay %s is the relay's version already", version))
	case check:
		return report(fmt.Sprintf("Relay is %s, you have %s. Update with: %s", me.Version, version, c.updateCommand()))
	}

	exe, err := executablePath(c.d)
	if err != nil {
		return &cliError{Title: "Can't tell where this tsrelay is", Code: "error", err: err, Reason: sentence(err.Error())}
	}
	// The new version is written next to the executable, so that renaming
	// it into place is atomic; this also finds out early whether it can be.
	f, err := os.CreateTemp(filepath.Dir(exe), ".tsrelay-update-*")
	if err != nil {
		return c.notWritable(exe, err)
	}
	tmp := f.Name()
	defer os.Remove(tmp) // no-op after the rename
	if !yes {
		if !c.interactive() {
			f.Close()
			return usage("Not updating without confirmation", "tsrelay update asks before replacing itself, and there is no terminal to ask on.",
				c.updateCommand()+" --yes")
		}
		if !c.confirm(ctx, fmt.Sprintf("Replace tsrelay %s at %s with the relay's %s?", version, exe, me.Version)) {
			f.Close()
			return report(fmt.Sprintf("Left tsrelay %s as it is", version))
		}
	}
	err = download(ctx, relayAPI.HTTP, c.base, f)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		return c.notWritable(exe, err)
	}
	if err := replaceExecutable(runtime.GOOS, exe, tmp); err != nil {
		return c.notWritable(exe, err)
	}
	res.Updated, res.Path = true, exe
	return report(errStream.glyph(errStream.style(styleOK, "●")) + fmt.Sprintf("updated tsrelay %s to %s at %s", version, me.Version, exe))
}

// executablePath is d.Executable in tests, else this process's executable,
// with symlinks resolved so that the file itself is replaced.
func executablePath(d deps) (string, error) {
	exe := d.Executable
	if exe == "" {
		var err error
		if exe, err = os.Executable(); err != nil {
			return "", err
		}
	}
	return filepath.EvalSymlinks(exe)
}

// notWritable explains that exe can't be replaced, and how to update anyway.
func (c *cli) notWritable(exe string, err error) *cliError {
	reason := fmt.Sprintf("tsrelay update writes the new version into %s: %s", filepath.Dir(exe), sentence(err.Error()))
	if errors.Is(err, fs.ErrPermission) {
		reason += " A package manager may own it."
	}
	fix := "sudo " + exe + strings.TrimPrefix(c.updateCommand(), "tsrelay")
	if runtime.GOOS == "windows" {
		fix = "Run " + c.updateCommand() + " in a terminal run as administrator"
	}
	return &cliError{Title: "Can't replace " + exe, Code: "not_writable", err: err, Reason: reason,
		Fixes: []string{fix, installCommand(c.base)}}
}

// installCommand installs the relay's version of tsrelay to ~/.local/bin
// (or %LOCALAPPDATA% on Windows) with the relay's install script.
func installCommand(base string) string {
	if runtime.GOOS == "windows" {
		return "irm " + base + "/install.ps1 | iex"
	}
	return "curl -fsSL " + base + "/install.sh | sh"
}

// download writes the relay's tsrelay for this platform to f, and checks
// it against the release's checksum. The relay redirects
// /download/{os}/{arch} to the release asset; the checksum is next to it,
// at checksumURL(asset).
// checksumURL is where a release keeps asset's checksum: its URL without
// ".exe", plus ".sha256", as the release workflow names it and install.sh
// and install.ps1 fetch it.
func checksumURL(asset string) string {
	return strings.TrimSuffix(asset, ".exe") + ".sha256"
}

func download(ctx context.Context, hc *http.Client, base string, f *os.File) error {
	asset, err := assetURL(ctx, hc, base+"/download/"+runtime.GOOS+"/"+runtime.GOARCH)
	if err != nil {
		return err
	}
	resp, err := fetch(ctx, hc, asset)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), resp.Body); err != nil {
		return downloadFailed(asset, err)
	}
	sum := checksumURL(asset)
	sumResp, err := fetch(ctx, hc, sum)
	if err != nil {
		return err
	}
	defer sumResp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(sumResp.Body, 1024))
	if err != nil {
		return downloadFailed(sum, err)
	}
	want, got := strings.Fields(string(b)), hex.EncodeToString(h.Sum(nil))
	if len(want) == 0 || !strings.EqualFold(want[0], got) {
		return &cliError{Title: "The download doesn't match its checksum", Code: "checksum_mismatch",
			Reason: fmt.Sprintf("%s has SHA-256 %s, but %s says otherwise. Nothing was replaced.", asset, got, sum),
			Fixes:  []string{"Retry; if it keeps failing, the download is being altered on its way"}}
	}
	return nil
}

// assetURL is where the relay's download link redirects. GitHub redirects
// that URL on to a signed one, which has no checksum next to it, so only
// the relay's redirect is followed here.
func assetURL(ctx context.Context, hc *http.Client, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	noFollow := *hc
	noFollow.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := noFollow.Do(req)
	if err != nil {
		return "", downloadFailed(url, err)
	}
	resp.Body.Close()
	loc, err := resp.Location()
	if err != nil {
		return "", downloadFailed(url, fmt.Errorf("it answered %s", resp.Status))
	}
	return loc.String(), nil
}

func fetch(ctx context.Context, hc *http.Client, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, downloadFailed(url, err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, downloadFailed(url, fmt.Errorf("it answered %s", resp.Status))
	}
	return resp, nil
}

func downloadFailed(url string, err error) *cliError {
	return &cliError{Title: "Can't download the relay's tsrelay", Code: "download_failed", err: err,
		Reason: fmt.Sprintf("Fetching %s failed: %s Nothing was replaced.", url, sentence(err.Error())),
		Fixes:  []string{"Retry", "Check that this machine can reach github.com"}}
}

// replaceExecutable renames tmp to exe. Windows can't replace a running
// executable, but it can rename one: the old one moves aside to exe.old,
// which removeOldExecutable deletes on the next run.
func replaceExecutable(goos, exe, tmp string) error {
	if goos != "windows" {
		return os.Rename(tmp, exe)
	}
	old := exe + ".old"
	_ = os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		return err
	}
	if err := os.Rename(tmp, exe); err != nil {
		_ = os.Rename(old, exe)
		return err
	}
	return nil
}

// removeOldExecutable deletes the executable an update moved aside on
// Windows. It fails quietly while that one still runs.
func removeOldExecutable(exe string) { _ = os.Remove(exe + ".old") }

// stdin is d.Stdin in tests, else os.Stdin.
func (c *cli) stdin() io.Reader {
	if c.d.Stdin != nil {
		return c.d.Stdin
	}
	return os.Stdin
}

// interactive reports whether someone can answer a question: stdin and
// stderr are terminals, and --json is off.
func (c *cli) interactive() bool {
	return !c.json && isTerminal(c.stdin()) && isTerminal(c.stderr)
}

// confirm asks question on stderr and reads the answer from stdin. An
// empty answer is yes; the end of input or an interrupt is no.
func (c *cli) confirm(ctx context.Context, question string) bool {
	s := c.stderrStream()
	fmt.Fprint(s.w, s.style(styleBold, question)+" [Y/n] ")
	answer := make(chan string, 1)
	go func() {
		line, err := bufio.NewReader(c.stdin()).ReadString('\n')
		if err != nil && line == "" {
			line = "n"
		}
		answer <- line
	}()
	select {
	case <-ctx.Done():
		fmt.Fprintln(s.w)
		return false
	case line := <-answer:
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "", "y", "yes":
			return true
		}
		return false
	}
}

// notices remembers when the CLI last interrupted a command to offer an
// update or to name a newer relay release, in notices.json next to the
// remembered hooks.
type notices struct {
	UpdatePrompt  time.Time `json:"updatePrompt,omitzero"`
	ReleaseNotice time.Time `json:"releaseNotice,omitzero"`
}

func noticesPath(d deps) (string, error) {
	p := d.StatePath
	if p == "" {
		var err error
		if p, err = client.DefaultStatePath(); err != nil {
			return "", err
		}
	}
	return filepath.Join(filepath.Dir(p), "notices.json"), nil
}

// offer runs after a command that talked to the relay. On an interactive
// terminal, at most once per offerEvery each, it offers to update to the
// relay's version when the two differ, and tells admins about a relay
// release newer than the relay. It never does in a background tunnel or a
// completion, or after an interrupt.
func (c *cli) offer(ctx context.Context) {
	if c.base == "" || c.bg != nil || ctx.Err() != nil || c.cmdPath == "tsrelay update" ||
		(len(c.args) > 0 && strings.HasPrefix(c.args[0], "__complete")) || !c.interactive() {
		return
	}
	path, err := noticesPath(c.d)
	if err != nil {
		return
	}
	var n notices
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &n)
	}
	save := func() {
		if b, err := json.Marshal(n); err == nil && os.MkdirAll(filepath.Dir(path), 0o700) == nil {
			_ = atomicfile.WriteFile(path, b, 0o600)
		}
	}
	now := time.Now()
	if v := c.version.Load(); v != nil && now.Sub(n.UpdatePrompt) >= offerEvery {
		n.UpdatePrompt = now
		save()
		if c.confirm(ctx, fmt.Sprintf("Relay is %s, you have %s. Update now?", v.relay, version)) {
			if err := c.update(ctx, false, true); err != nil {
				c.handleError(c.stderr, fang.Styles{}, err)
			}
		}
	}
	if now.Sub(n.ReleaseNotice) < offerEvery {
		return
	}
	n.ReleaseNotice = now
	save()
	ctx, cancel := context.WithTimeout(ctx, completionTimeout)
	defer cancel()
	relayAPI, err := c.newRelayAPI(ctx)
	if err != nil {
		return
	}
	me, err := relayAPI.Me(ctx)
	if err != nil || me.LatestVersion == "" {
		return
	}
	s := c.stderrStream()
	fmt.Fprintln(s.w, s.style(styleInfo, fmt.Sprintf("tsrelay %s is available; this relay runs %s.", me.LatestVersion, me.Version))+
		" Release notes: "+s.link(me.ReleaseURL, me.ReleaseURL))
}
