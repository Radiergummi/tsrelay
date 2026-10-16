package main

import (
	"io"
	"sync"

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
}

func newRoot(c *cli) *cobra.Command {
	root := &cobra.Command{
		Use:   "tsrelay",
		Short: "Deliver callbacks from your tailnet to apps on your machine",
		Long: "tsrelay delivers callbacks from your tailnet to apps on your machine.\n\n" +
			"Every flag can also be set as TSRELAY_<FLAG>, e.g. TSRELAY_RELAY for --relay; " +
			"the command line wins.",
		Args: usageArgs(cobra.NoArgs),
		RunE: c.helpAndFail,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			return envDefaults(cmd.Flags())
		},
	}
	root.SetOut(c.stdout)
	root.SetErr(c.stderr)
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return &usageError{err.Error()} })
	pf := root.PersistentFlags()
	pf.StringVar(&c.relay, "relay", "", "relay URL or host (default: https://tsrelay.<your tailnet>)")
	pf.BoolVarP(&c.json, "json", "j", false, "print JSON on stdout (indented on a terminal)")
	pf.BoolVar(&c.noColor, "no-color", false, "print plain text, as NO_COLOR does")
	pf.BoolVar(&c.noLinks, "no-hyperlinks", false, "print full inspector URLs instead of clickable call IDs")
	root.AddCommand(newHTTPCmd(c), newAttachCmd(c), newHooksCmd(c), newURLCmd(c), newServeCmd(c))
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
