package app

import (
	"fmt"
	"github.com/spf13/cobra"
)

func buildRootCmd(version string, commit string) *cobra.Command {
	rootCmd := cobra.Command{
		Use:     appName,
		Short:   "A command-line utility to migrate data from one Kubernetes PersistentVolumeClaim to another",
		Version: fmt.Sprintf("%s (commit: %s)", version, commit),
	}

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.pv-migrate.yaml)")
	rootCmd.Flags().BoolP("author", "a", false, "print author information")

	rootCmd.AddCommand(buildMigrateCmd())
	rootCmd.AddCommand(buildCompletionCmd())

	return &rootCmd
}
