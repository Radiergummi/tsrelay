package main

import (
	"io"
	"sync"
	"sync/atomic"

	"github.com/spf13/cobra"
)

// cli is what every command shares: where output goes, the global flags,
// and what tests replace.
type cli struct {
	d              deps
	stdout, stderr io.Writer
	relay          string    // --relay
	json           bool      // -j/--json
	noColor        bool      // --no-color
	noLinks        bool      // --no-hyperlinks
	versionWarned  sync.Once // warnVersion runs at most once per run
	// What explaining an error needs to know about the run.
	args      []string                    // the arguments as given
	cmdPath   string                      // the command run, e.g. "tsrelay hooks list"
	relayFrom string                      // where the relay URL came from: "--relay", "TSRELAY_RELAY" or "" (the tailnet)
	base      string                      // the relay's URL, once known
	hook      string                      // the hook the command names, if any
	version   atomic.Pointer[versionSeen] // the relay's version, if it differs from this CLI's
	bg        *bgChild                    // the background tunnel this process runs as, if any
}

func newRoot(c *cli) *cobra.Command {
	root := &cobra.Command{
		Use:   "tsrelay",
		Short: "Deliver callbacks from your tailnet to apps on your machine",
		Long: "tsrelay delivers callbacks from your tailnet to apps on your machine.\n\n" +
			"Every flag can also be set as TSRELAY_<FLAG>, e.g. TSRELAY_RELAY for --relay; " +
			"the command line wins.",
		Args:                       subcommand,
		SuggestionsMinimumDistance: 2,
		RunE:                       c.helpAndFail,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			if cmd.Name() == cobra.ShellCompRequestCmd || cmd.Name() == cobra.ShellCompNoDescRequestCmd {
				return nil // completing applies the environment to the command completed
			}
			if err := envDefaults(cmd.Flags()); err != nil {
				return err
			}
			c.cmdPath = cmd.CommandPath()
			if f := cmd.Flags().Lookup("relay"); f != nil && f.Changed {
				c.relayFrom = "--relay"
				if envSet(f) {
					c.relayFrom = "TSRELAY_RELAY"
				}
			}
			return nil
		},
	}
	root.SetOut(c.stdout)
	root.SetErr(c.stderr)
	root.SetFlagErrorFunc(c.flagError)
	pf := root.PersistentFlags()
	pf.StringVar(&c.relay, "relay", "", "relay URL or host (default: https://tsrelay.<your tailnet>)")
	pf.BoolVarP(&c.json, "json", "j", false, "print JSON on stdout (indented on a terminal)")
	pf.BoolVar(&c.noColor, "no-color", false, "print plain text, as NO_COLOR does")
	pf.BoolVar(&c.noLinks, "no-hyperlinks", false, "print full inspector URLs instead of clickable call IDs")
	root.AddCommand(newHTTPCmd(c), newAttachCmd(c), newHooksCmd(c), newURLCmd(c), newServeCmd(c),
		newPsCmd(c), newStopCmd(c), newLogsCmd(c))
	return root
}

// exitWith ends a command with code, which has been reported already.
func exitWith(code int) error {
	if code == 0 {
		return nil
	}
	return exitCodeError(code)
}

// helpAndFail is the RunE of a command that needs a subcommand: without
// one, it shows the help on stderr and exits 2.
func (c *cli) helpAndFail(cmd *cobra.Command, _ []string) error {
	cmd.SetOut(c.stderr)
	_ = cmd.Help()
	return exitCodeError(2)
}
