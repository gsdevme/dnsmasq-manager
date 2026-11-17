package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/gsdevme/dnsmasq-manager/internal/version"
)

// newVersionCommand returns the version subcommand, which prints
// "dnsmasq-manager <version>" and takes no arguments.
func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version and exit",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "dnsmasq-manager %s\n", version.String())
			return err
		},
	}
}
