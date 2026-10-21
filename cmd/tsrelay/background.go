package main

import (
	"bufio"
	"cmp"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Radiergummi/tsrelay/internal/api"
	"github.com/Radiergummi/tsrelay/internal/atomicfile"
	"github.com/Radiergummi/tsrelay/internal/client"
)

// backgroundIDEnv names the background tunnel a tsrelay started by
// --background runs as. No flag is called --background-id, so it is not
// one of the TSRELAY_<FLAG> variables.
const backgroundIDEnv = "TSRELAY_BACKGROUND_ID"

const (
	// backgroundStartTimeout is how long --background waits for the tunnel
	// to attach before it stops it.
	backgroundStartTimeout = 30 * time.Second
	// backgroundStopTimeout is how long tsrelay stop waits for a clean
	// detach before it kills the tunnel.
	backgroundStopTimeout = 10 * time.Second
)

// runRecord describes a background tunnel; it is run/<id>.json.
type runRecord struct {
	ID      string    `json:"id"`
	PID     int       `json:"pid"`
	Args    []string  `json:"args"` // the command line, without the program
	Relay   string    `json:"relay"`
	Hooks   []runHook `json:"hooks"`
	Target  string    `json:"target"`
	Started time.Time `json:"started"`
}

type runHook struct {
	ID   string `json:"id"`
	Path string `json:"path"`
	URL  string `json:"url"`
}

// serves reports whether r is the tunnel ref names: by its ID, or by a
// hook's ID, path or URL.
func (r runRecord) serves(ref string) bool {
	return r.ID == ref || slices.ContainsFunc(r.Hooks, func(h runHook) bool {
		return h.ID == ref || h.Path != "" && h.Path == strings.Trim(ref, "/") || h.URL == ref
	})
}

// backgroundMessage is what a background tunnel tells the tsrelay that
// started it, as one JSON line: its record once attached, or the error that
// ended it.
type backgroundMessage struct {
	Ready *runRecord `json:"ready,omitempty"`
	jsonError
}

// openRunDir returns client.DefaultRunDir, created if need be.
func openRunDir() (string, error) {
	dir, err := client.DefaultRunDir()
	if err == nil {
		err = os.MkdirAll(dir, 0o700)
	}
	if err != nil {
		return "", &cliError{Title: "Can't use tsrelay's run directory", Code: "run_dir_unusable", err: err,
			Reason: "Background tunnels keep their records and logs in $XDG_STATE_HOME/tsrelay/run: " + sentence(err.Error()),
			Fixes:  []string{"Set XDG_STATE_HOME to a directory you can write to"}}
	}
	return dir, nil
}

func recordPath(dir, id string) string { return filepath.Join(dir, id+".json") }
func logPath(dir, id string) string    { return filepath.Join(dir, id+".log") }
func lockPath(dir, id string) string   { return filepath.Join(dir, id+".lock") }

// removeRun forgets the background tunnel id: its record, its log and its
// lock.
func removeRun(dir, id string) {
	_ = os.Remove(recordPath(dir, id))
	_ = os.Remove(logPath(dir, id))
	_ = os.Remove(lockPath(dir, id))
}

// readRecords returns the records in dir, oldest first. Files that are no
// record are skipped.
func readRecords(dir string) []runRecord {
	paths, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	var rs []runRecord
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var r runRecord
		if json.Unmarshal(b, &r) != nil || r.ID == "" {
			continue
		}
		rs = append(rs, r)
	}
	slices.SortFunc(rs, func(a, b runRecord) int { return a.Started.Compare(b.Started) })
	return rs
}

// newRunID picks an ID no tunnel in dir uses: six lowercase letters and
// digits.
func newRunID(dir string) string {
	for {
		id := strings.ToLower(rand.Text()[:6])
		if _, err := os.Stat(logPath(dir, id)); errors.Is(err, os.ErrNotExist) {
			return id
		}
	}
}

// startBackground runs this command line again as a background tunnel (see
// detach), with its output appended to run/<id>.log, and waits until the
// tunnel has attached. Then it prints the hook URLs and the tunnel's ID. If
// the tunnel fails to attach, it reports why and leaves no process, record
// or log behind.
//
// The background tunnel's stdout is a pipe to this process, on which it
// sends one backgroundMessage and then closes it; if it ends before that,
// the pipe closes empty.
func (c *cli) startBackground(ctx context.Context) error {
	dir, err := openRunDir()
	if err != nil {
		return err
	}
	id := newRunID(dir)
	startFailed := func(err error) error {
		removeRun(dir, id)
		return &cliError{Title: "Couldn't start tsrelay in the background", Reason: sentence(err.Error()), Code: "background_failed", err: err,
			Fixes: []string{c.foreground()}}
	}
	exe, err := os.Executable()
	if err != nil {
		return startFailed(err)
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		return startFailed(err)
	}
	defer pr.Close()
	log, err := os.OpenFile(logPath(dir, id), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		pw.Close()
		return startFailed(err)
	}
	cmd := exec.Command(exe, c.args...)
	cmd.Env = append(os.Environ(), backgroundIDEnv+"="+id)
	cmd.Stdout, cmd.Stderr = pw, log
	detach(cmd)
	err = cmd.Start()
	// Only the tunnel writes to these. Windows can't remove a file that is
	// still open, so the log must be closed before a failure removes it.
	pw.Close()
	log.Close()
	if err != nil {
		return startFailed(err)
	}

	msgs := make(chan backgroundMessage, 1)
	go func() {
		var m backgroundMessage
		line, _ := bufio.NewReader(pr).ReadBytes('\n')
		_ = json.Unmarshal(line, &m)
		msgs <- m
	}()
	timer := time.NewTimer(backgroundStartTimeout)
	defer timer.Stop()
	var m backgroundMessage
	failed := &cliError{Code: "background_failed", Fixes: []string{c.foreground()}}
	select {
	case m = <-msgs:
		failed.Title = "The background tunnel ended before it attached"
	case <-timer.C:
		failed.Title, failed.Reason = fmt.Sprintf("The tunnel didn't attach within %s", backgroundStartTimeout), "It was stopped. "
	case <-ctx.Done():
		failed.Title, failed.Reason = "Interrupted before the tunnel attached", "It was stopped. "
	}
	if m.Ready == nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if e := m.Error; e.Title != "" {
			failed = &cliError{Title: e.Title, Reason: e.Reason, Fixes: e.Fixes, Code: e.Code, usage: e.Code == "usage"}
		} else {
			failed.Reason += logTail(logPath(dir, id))
		}
		removeRun(dir, id)
		return failed
	}
	// Reaps the tunnel if it ends while this process still runs, as it does
	// in tests; otherwise this process exits now and init adopts it.
	go cmd.Wait()

	if c.json {
		return writeJSON(c.stdoutStream(), m.Ready)
	}
	out, errStream := c.streams()
	for _, h := range m.Ready.Hooks {
		fmt.Fprintln(out.w, out.style(styleBold, h.URL))
	}
	fmt.Fprintln(errStream.w, errStream.glyph(errStream.style(styleOK, "●"))+"running in the background as "+errStream.style(styleBold, id))
	fmt.Fprintln(errStream.w, errStream.style(styleDim, "tsrelay logs "+id+" -f  ·  tsrelay stop "+id))
	return nil
}

// foreground is this command line without --background.
func (c *cli) foreground() string {
	args := slices.DeleteFunc(slices.Clone(c.args), func(a string) bool {
		return a == "--background" || strings.HasPrefix(a, "--background=")
	})
	return strings.Join(append([]string{"tsrelay"}, args...), " ")
}

// logTail says how the log at path ended, for explaining a failed start.
func logTail(path string) string {
	b, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return "It logged nothing."
	}
	return "Its log ended with:\n" + strings.Join(lines[max(len(lines)-10, 0):], "\n")
}

// bgChild is the background tunnel this process runs as. It tells the
// tsrelay that started it how the start went, once.
type bgChild struct {
	id   string
	pipe io.Writer // this process's stdout
	once sync.Once

	mu  sync.Mutex
	rec *runRecord // as written to run/<id>.json, once ready
}

// ready records the tunnel in run/<id>.json and reports it attached.
func (b *bgChild) ready(r runRecord) {
	if b == nil {
		return
	}
	b.once.Do(func() {
		b.mu.Lock()
		err := b.write(r)
		if err == nil {
			b.rec = &r
		}
		b.mu.Unlock()
		if err != nil {
			b.send(backgroundMessage{jsonError: newJSONError(&cliError{Title: "Can't record the background tunnel", Reason: sentence(err.Error()), Code: "run_dir_unusable"})})
			return
		}
		b.send(backgroundMessage{Ready: &r})
	})
}

// hookChanged updates the recorded hook oldID to h, after it was renamed or
// recreated, so ps, stop and logs find it by its new path.
func (b *bgChild) hookChanged(oldID string, h api.Hook) {
	b.updateHooks(func(hs []runHook) []runHook {
		for i := range hs {
			if hs[i].ID == oldID {
				hs[i] = runHook{ID: h.ID, Path: h.Path, URL: h.URL}
			}
		}
		return hs
	})
}

// hookDropped removes the recorded hook id once the tunnel no longer
// serves it.
func (b *bgChild) hookDropped(id string) {
	b.updateHooks(func(hs []runHook) []runHook {
		return slices.DeleteFunc(hs, func(h runHook) bool { return h.ID == id })
	})
}

// updateHooks rewrites the record's hooks with f, if there is a record yet.
func (b *bgChild) updateHooks(f func([]runHook) []runHook) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.rec == nil {
		return
	}
	b.rec.Hooks = f(b.rec.Hooks)
	_ = b.write(*b.rec) // best effort: the tunnel keeps running either way
}

// write writes r to run/<id>.json. The caller holds mu.
func (b *bgChild) write(r runRecord) error {
	data, err := json.MarshalIndent(r, "", "  ")
	dir, derr := openRunDir()
	if err = cmp.Or(err, derr); err != nil {
		return err
	}
	return atomicfile.WriteFile(recordPath(dir, b.id), data, 0o600)
}

// fail reports e as the reason the tunnel ends, unless it was reported
// attached already.
func (b *bgChild) fail(e *cliError) {
	if b == nil {
		return
	}
	b.once.Do(func() { b.send(backgroundMessage{jsonError: newJSONError(e)}) })
}

// send writes m and closes the pipe: once the tsrelay that started this
// process has exited, writing to it would end this one with SIGPIPE.
func (b *bgChild) send(m backgroundMessage) {
	_ = json.NewEncoder(b.pipe).Encode(m)
	if cl, ok := b.pipe.(io.Closer); ok {
		_ = cl.Close()
	}
}

// runRecord describes this process as a background tunnel serving hs;
// target is where calls go.
func (c *cli) runRecord(hs []api.Hook, target string) runRecord {
	r := runRecord{ID: c.bg.id, PID: os.Getpid(), Args: c.args, Relay: c.base, Target: target, Started: time.Now().UTC()}
	for _, h := range hs {
		r.Hooks = append(r.Hooks, runHook{ID: h.ID, Path: h.Path, URL: h.URL})
	}
	return r
}
