package main

import (
	"errors"
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
		Use:   "url [<port|url>]",
		Short: "Print the URL tsrelay http would use here",
		Args:  usageArgs(cobra.MaximumNArgs(1)),
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
			if target, err = client.ParseTarget(args[0]); err != nil {
				return &usageError{err.Error()}
			}
		} else {
			switch targets := state.Targets(base, cwd); len(targets) {
			case 0:
			case 1:
				target, _ = url.Parse(targets[0])
			default:
				return usagef("this directory has hooks for several targets (%s); name one", strings.Join(targets, ", "))
			}
		}
		hookURL := ""
		if target != nil {
			hookURL = state.URL(client.StateKey(base, cwd, target))
		}
		if hookURL == "" {
			return errors.New("no hook is known for this directory and target yet; run tsrelay http first")
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
