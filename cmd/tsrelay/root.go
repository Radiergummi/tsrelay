package main

import (
	"context"
	"io"

	"github.com/spf13/cobra"
)

// cli is what every command shares: where output goes, and what tests replace.
type cli struct {
	d              deps
	stdout, stderr io.Writer
}

func newRoot(c *cli) *cobra.Command {
	root := &cobra.Command{
		Use:   "tsrelay",
		Short: "Deliver callbacks from your tailnet to apps on your machine",
		Long: "tsrelay delivers callbacks from your tailnet to apps on your machine.\n\n" +
			"Every flag can also be set as TSRELAY_<FLAG>, e.g. TSRELAY_RELAY for --relay; " +
			"the command line wins.",
		Args: usageArgs(cobra.NoArgs),
		// Without a command, show the help and exit 2.
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SetOut(c.stderr)
			_ = cmd.Help()
			return exitCodeError(2)
		},
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			if cmd.DisableFlagParsing {
				return nil // a pass-through command parses its own flags
			}
			return envDefaults(cmd.Flags())
		},
	}
	root.SetOut(c.stdout)
	root.SetErr(c.stderr)
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return &usageError{err.Error()} })
	root.AddCommand(newServeCmd(c))
	for _, p := range []struct {
		name, short string
		fn          func(context.Context, []string, io.Writer, io.Writer, deps) int
	}{
		{"http", "Forward a new or remembered hook to a local app", httpCmd},
		{"attach", "Serve existing hooks from one local app", attachCmd},
		{"hooks", "Manage your hooks", hooksCmd},
		{"url", "Print the URL tsrelay http would use here", urlCmd},
	} {
		root.AddCommand(passThrough(c, p.name, p.short, p.fn)) // replaced in Task 2
	}
	return root
}

// passThrough runs a command that still parses its own flags. It goes in Task 2.
func passThrough(c *cli, name, short string, fn func(context.Context, []string, io.Writer, io.Writer, deps) int) *cobra.Command {
	return &cobra.Command{
		Use: name, Short: short, DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if code := fn(cmd.Context(), args, c.stdout, c.stderr, c.d); code != 0 {
				return exitCodeError(code)
			}
			return nil
		},
	}
}
