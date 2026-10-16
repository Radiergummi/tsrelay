package main

import (
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/Radiergummi/tsrelay/internal/client"
	"github.com/spf13/cobra"
)

// newURLCmd prints the URL tsrelay http would use here, from the state
// file. It never contacts the relay, so it creates nothing.
func newURLCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:               "url [<port|url>]",
		Short:             "Print the URL tsrelay http would use here",
		Args:              arity(0, 1, "port or URL"),
		ValidArgsFunction: c.completeTargetArg,
	}
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		base, err := relayBase(ctx, c.relay, c.d)
		if err != nil {
			return err
		}
		state, err := openState(c.d)
		if err != nil {
			return err
		}
		cwd, _ := os.Getwd()
		var target *url.URL
		if len(args) == 1 {
			if target, err = parseTarget(args[0], func(t string) string { return "tsrelay url " + t }); err != nil {
				return err
			}
		} else {
			switch targets := state.Targets(base, cwd); len(targets) {
			case 0:
			case 1:
				target, _ = url.Parse(targets[0])
			default:
				e := usage("Hooks for several targets here", "This directory has hooks for "+strings.Join(targets, ", ")+"; name one.")
				for _, t := range targets {
					e.Fixes = append(e.Fixes, "tsrelay url "+t)
				}
				return e
			}
		}
		hookURL := ""
		if target != nil {
			hookURL = state.URL(client.StateKey(base, cwd, target))
		}
		if hookURL == "" {
			fix := "tsrelay http 3000"
			if len(args) == 1 {
				fix = "tsrelay http " + args[0]
			}
			return &cliError{Title: "No hook known here yet", Code: "no_remembered_hook",
				Reason: "tsrelay url prints what tsrelay http remembered for this directory and target, and it has run for neither yet.",
				Fixes:  []string{fix}}
		}
		if c.json {
			out := c.stdoutStream()
			return writeJSON(out, map[string]string{"url": hookURL})
		}
		fmt.Fprintln(c.stdout, hookURL)
		return nil
	}
	return cmd
}
