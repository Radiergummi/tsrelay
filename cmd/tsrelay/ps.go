package main

import (
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func newPsCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ps",
		Short: "List background tunnels",
		Long: "List the tunnels started with --background. A tunnel whose process is gone is " +
			"listed once as exited, then forgotten along with its log.",
		Args: arity(0, 0, ""),
	}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		dir, err := openRunDir()
		if err != nil {
			return err
		}
		entries := []runJSON{}
		for _, r := range readRecords(dir) {
			e := r.public()
			e.State = "running"
			if !runAlive(dir, r) {
				e.State = "exited"
				removeRun(dir, r.ID)
			}
			entries = append(entries, e)
		}
		out, errStream := c.streams()
		if c.json {
			return writeJSON(out, struct {
				Runs []runJSON `json:"runs"`
			}{entries})
		}
		rows := make([][]string, len(entries))
		for i, e := range entries {
			urls := make([]string, len(e.Hooks))
			for j, h := range e.Hooks {
				urls[j] = out.link(h.URL, h.URL)
			}
			uptime := "-"
			if e.State == "running" {
				uptime = time.Since(e.StartedAt).Round(time.Second).String()
			}
			rows[i] = []string{e.ID, strings.Join(urls, " "), e.Target, uptime, e.State}
		}
		writeTable(out, errStream, []string{"ID", "HOOKS", "TARGET", "UPTIME", "STATE"}, rows,
			"no background tunnels; tsrelay http <port> --background starts one")
		return nil
	}
	return cmd
}

func newStopCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "stop <id|hook>... | --all",
		Short:   "Stop background tunnels",
		Long:    fmt.Sprintf("Stop background tunnels, named by ID or by a hook they serve. Each gets %s to detach cleanly before it is killed.", backgroundStopTimeout),
		Example: "  tsrelay stop k3x9qa\n  tsrelay stop payments\n  tsrelay stop --all",
	}
	cmd.ValidArgsFunction = c.completeBackground(-1)
	all := cmd.Flags().BoolP("all", "a", false, "stop every background tunnel")
	cmd.RunE = func(cmd *cobra.Command, refs []string) error {
		switch {
		case *all && len(refs) > 0:
			return usage("Both --all and a tunnel given", "tsrelay stop stops the tunnels named, or with --all every one.",
				"tsrelay stop --all", "tsrelay stop "+strings.Join(refs, " "))
		case !*all && len(refs) == 0:
			return usage("No background tunnel given", "tsrelay stop takes the ID or a hook of a background tunnel, or --all.",
				"tsrelay ps", "tsrelay stop --all")
		}
		dir, err := openRunDir()
		if err != nil {
			return err
		}
		rs := readRecords(dir)
		if !*all {
			var named []runRecord
			for _, ref := range refs {
				found, err := findRuns(rs, ref)
				if err != nil {
					return err
				}
				for _, r := range found {
					if !slices.ContainsFunc(named, func(n runRecord) bool { return n.ID == r.ID }) {
						named = append(named, r)
					}
				}
			}
			rs = named
		}
		// failure is a tunnel that would not stop, with the error on it.
		type failure struct {
			ID string `json:"id"`
			jsonError
		}
		result := struct {
			Stopped []runJSON `json:"stopped"`
			Failed  []failure `json:"failed"`
		}{[]runJSON{}, []failure{}}
		out, errStream := c.streams()
		// A tunnel that won't stop doesn't keep the others running, nor
		// hides the results of those stopped already.
		var failed error
		for _, r := range rs {
			state, err := stopRun(dir, r)
			if err != nil {
				if failed == nil {
					failed = err
				}
				result.Failed = append(result.Failed, failure{r.ID, newJSONError(c.explain(err))})
				continue
			}
			removeRun(dir, r.ID)
			j := r.public()
			j.State = state
			result.Stopped = append(result.Stopped, j)
			switch {
			case c.json:
			case state == "killed":
				fmt.Fprintln(errStream.w, errStream.glyph(errStream.style(styleWarn, "●"))+
					fmt.Sprintf("%s didn't detach within %s; killed it", r.ID, backgroundStopTimeout))
			case state == "exited":
				fmt.Fprintln(errStream.w, errStream.glyph(errStream.style(styleDim, "●"))+r.ID+" had exited already")
			default:
				fmt.Fprintln(errStream.w, errStream.glyph(errStream.style(styleOK, "●"))+"stopped "+r.ID)
			}
		}
		if c.json {
			if err := writeJSON(out, result); err != nil {
				return err
			}
		}
		return failed
	}
	return cmd
}

// stopRun stops the background tunnel r: it asks it to detach (see
// terminate), and kills it if it is still running after
// backgroundStopTimeout. The result is "stopped", "killed", or "exited" if
// it was gone already.
func stopRun(dir string, r runRecord) (string, error) {
	if !runAlive(dir, r) {
		return "exited", nil
	}
	if terminate(r.PID, r.ID) == nil && waitGone(dir, r, backgroundStopTimeout) {
		return "stopped", nil
	}
	if !runAlive(dir, r) {
		return "stopped", nil // it exited just now
	}
	p, err := os.FindProcess(r.PID)
	if err == nil {
		err = p.Kill()
	}
	if err != nil && runAlive(dir, r) {
		return "", &cliError{Title: "Couldn't stop " + r.ID, Code: "stop_failed", err: err,
			Reason: fmt.Sprintf("It didn't detach, and killing process %d failed: %s", r.PID, sentence(err.Error())),
			Fixes:  []string{fmt.Sprintf("Kill process %d yourself", r.PID)}}
	}
	waitGone(dir, r, time.Second)
	return "killed", nil
}

// waitGone waits up to timeout for the background tunnel r to exit and
// reports whether it did.
func waitGone(dir string, r runRecord, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for runAlive(dir, r) {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
	return true
}

// findRuns returns the background tunnels ref names; see runRecord.serves.
func findRuns(rs []runRecord, ref string) ([]runRecord, error) {
	var found []runRecord
	for _, r := range rs {
		if r.serves(ref) {
			found = append(found, r)
		}
	}
	if len(found) == 0 {
		return nil, &cliError{Title: fmt.Sprintf("No background tunnel %q", ref), Code: "no_such_tunnel",
			Reason: "A background tunnel is named by its ID or by a hook it serves.",
			Fixes:  []string{"tsrelay ps"}}
	}
	return found, nil
}

func newLogsCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "logs <id|hook>",
		Short:   "Print or follow a background tunnel's log",
		Example: "  tsrelay logs k3x9qa\n  tsrelay logs payments -f",
		Args:    arity(1, 1, "background tunnel"),
	}
	cmd.ValidArgsFunction = c.completeBackground(1)
	follow := cmd.Flags().BoolP("follow", "f", false, "keep printing what is logged until interrupted")
	cmd.RunE = func(cmd *cobra.Command, refs []string) error {
		dir, err := openRunDir()
		if err != nil {
			return err
		}
		found, err := findRuns(readRecords(dir), refs[0])
		if err != nil {
			return err
		}
		if len(found) > 1 {
			e := usage(fmt.Sprintf("%q names several background tunnels", refs[0]), "tsrelay logs prints one tunnel's log.")
			for _, r := range found {
				e.Fixes = append(e.Fixes, "tsrelay logs "+r.ID)
			}
			return e
		}
		f, err := os.Open(logPath(dir, found[0].ID))
		if err != nil {
			return &cliError{Title: "Can't read the log of " + found[0].ID, Reason: sentence(err.Error()), Code: "run_dir_unusable", err: err}
		}
		defer f.Close()
		if _, err := io.Copy(c.stdout, f); err != nil || !*follow {
			return err
		}
		tick := time.NewTicker(200 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-cmd.Context().Done():
				return nil
			case <-tick.C:
				if _, err := io.Copy(c.stdout, f); err != nil {
					return err
				}
			}
		}
	}
	return cmd
}

// completeBackground completes background tunnels by ID, with their target
// as help, and by the paths of the hooks they serve. Those already named
// on the line are left out. max is how many the command takes (< 0: any
// number).
func (c *cli) completeBackground(max int) cobra.CompletionFunc {
	return func(_ *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if max >= 0 && len(args) >= max {
			return nil, noCompletions
		}
		dir, err := openRunDir()
		if err != nil {
			return nil, noCompletions
		}
		var out []cobra.Completion
		for _, r := range readRecords(dir) {
			if slices.ContainsFunc(args, r.serves) {
				continue
			}
			if strings.HasPrefix(r.ID, toComplete) {
				out = append(out, cobra.CompletionWithDesc(r.ID, "forwarding to "+r.Target))
			}
			for _, h := range r.Hooks {
				if h.Path != "" && strings.HasPrefix(h.Path, toComplete) {
					out = append(out, cobra.CompletionWithDesc(h.Path, "background tunnel "+r.ID))
				}
			}
		}
		return out, noCompletions
	}
}
