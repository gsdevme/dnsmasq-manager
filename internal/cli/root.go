// Package cli builds the dnsmasq-manager command tree.
//
// Commands return errors rather than printing them; the caller prints the
// error and chooses the exit code.
package cli

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/gsdevme/dnsmasq-manager/internal/version"
)

// NewRootCommand returns the dnsmasq-manager root command, writing normal
// output to stdout and cobra's diagnostics to stderr. Run with no arguments
// it prints help. It has a --version flag with no -v shorthand, and no
// completion command.
func NewRootCommand(stdout, stderr io.Writer) *cobra.Command {
	root := &cobra.Command{
		Use:           "dnsmasq-manager",
		Short:         "Manage LAN DNS records served by dnsmasq",
		Version:       version.String(),
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetVersionTemplate("dnsmasq-manager {{.Version}}\n")
	root.Flags().Bool("version", false, "print the version and exit")
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.AddCommand(newVersionCommand())
	return root
}
