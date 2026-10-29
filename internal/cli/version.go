package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the module path, version, commit, and build date",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), versionLine())
			return nil
		},
	}
}

func versionLine() string {
	return fmt.Sprintf("gplay (%s) %s %s %s", modulePath, buildVersion, buildCommit, buildDate)
}
