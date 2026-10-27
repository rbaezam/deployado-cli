package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	version   = "dev"
	commit    = "none"
	buildDate = "unknown"

	// profileFlag is the value of the global --profile flag. It overrides the
	// active profile in ~/.config/deployado/config.json and the DEPLOYADO_PROFILE
	// env var, in that order.
	profileFlag string
)

// SetVersion sets the version information for the CLI
func SetVersion(v, c, d string) {
	version = v
	commit = c
	buildDate = d
}

var rootCmd = &cobra.Command{
	Use:   "deployado",
	Short: "Deployado CLI - Deploy applications to your servers",
	Long: `Deployado CLI is a command-line tool for deploying and managing
applications on your Deployado platform.

Get started by running:
  deployado auth login

Then deploy your first app:
  deployado launch`,
}

// Execute runs the root command
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.PersistentFlags().StringVar(&profileFlag, "profile", "", "Use a named profile instead of the active one")

	// Add subcommands
	rootCmd.AddCommand(authCmd)
	rootCmd.AddCommand(appsCmd)
	rootCmd.AddCommand(deployCmd)
	rootCmd.AddCommand(rollbackCmd)
	rootCmd.AddCommand(logsCmd)
	rootCmd.AddCommand(envCmd)
	rootCmd.AddCommand(launchCmd)
	rootCmd.AddCommand(sshCmd)
	rootCmd.AddCommand(versionCmd)
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("deployado version %s\n", version)
		fmt.Printf("  commit: %s\n", commit)
		fmt.Printf("  built:  %s\n", buildDate)
	},
}
