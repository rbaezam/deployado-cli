package cmd

import (
	"fmt"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var (
	dbRestoreApp    string
	dbRestoreTo     string
	dbRestoreNoDrop bool
	dbRestoreForce  bool
)

var databaseRestoreCmd = &cobra.Command{
	Use:   "restore",
	Short: "Restore an app's database from a snapshot",
	Long: `Restore an app's database to a release's pre-migrate snapshot, or a
specific snapshot id.

Deployado takes an automatic snapshot before every deploy that runs a
migration (pre_deploy_command). This reverts the database to one of those.

Examples:
  # Revert to the snapshot taken before release 20260617.210149 deployed
  deployado db restore --app insurance-board --to 20260617.210149

  # Restore a specific snapshot by id
  deployado db restore --app insurance-board --to 7a1b...-uuid

WARNING: this overwrites the current database (drop + restore). Use
--no-drop to restore without dropping existing objects first.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := getClient()
		if err != nil {
			return err
		}

		if !dbRestoreForce {
			color.New(color.FgYellow).Printf(
				"This will OVERWRITE the database for app '%s' with snapshot '%s'.\n",
				dbRestoreApp, dbRestoreTo,
			)
			fmt.Print("Type 'restore' to confirm: ")

			var confirm string
			fmt.Scanln(&confirm)

			if confirm != "restore" {
				fmt.Println("Aborted.")
				return nil
			}
		}

		resp, err := client.RestoreAppDB(dbRestoreApp, dbRestoreTo, !dbRestoreNoDrop)
		if err != nil {
			return fmt.Errorf("failed to start restore: %w", err)
		}

		color.New(color.FgGreen).Println("Restore started.")
		fmt.Printf("Restore run ID: %s\n", resp.RestoreRunID)
		fmt.Println("Track progress in the dashboard or with 'deployado db' backup tooling.")
		return nil
	},
}

func init() {
	databaseRestoreCmd.Flags().StringVar(&dbRestoreApp, "app", "", "App ID or slug (required)")
	databaseRestoreCmd.Flags().StringVar(&dbRestoreTo, "to", "",
		"Release version or snapshot id to restore (required)")
	databaseRestoreCmd.Flags().BoolVar(&dbRestoreNoDrop, "no-drop", false,
		"Restore without dropping existing objects first")
	databaseRestoreCmd.Flags().BoolVarP(&dbRestoreForce, "force", "f", false,
		"Skip confirmation prompt")
	databaseRestoreCmd.MarkFlagRequired("app")
	databaseRestoreCmd.MarkFlagRequired("to")

	databaseCmd.AddCommand(databaseRestoreCmd)
}
