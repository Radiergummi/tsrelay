package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Radiergummi/tsrelay/internal/sbom"
)

// newLicensesCmd prints the notices of the open-source components built
// into tsrelay, which their licenses require to travel with the binary,
// and where the relay lists them. It never contacts the relay.
func newLicensesCmd(c *cli) *cobra.Command {
	return &cobra.Command{
		Use:   "licenses",
		Short: "Print the licenses of the open-source components in tsrelay",
		Args:  arity(0, 0, ""),
		RunE: func(cmd *cobra.Command, _ []string) error {
			c.stdout.Write(sbom.Notices())
			if base, err := relayBase(cmd.Context(), c.relay, c.d); err == nil {
				fmt.Fprintf(c.stdout, "\nThe relay lists them at %s/licenses\n", base)
			}
			return nil
		},
	}
}
