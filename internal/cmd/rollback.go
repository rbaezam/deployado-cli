package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/deployado/cli/internal/api"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var rollbackCmd = &cobra.Command{
	Use:   "rollback [app-id]",
	Short: "Rollback an app to a previous release",
	Long: `Rollback an application to a previous release version.

If no app-id is provided, the CLI will try to detect the app
based on the current directory.

By default, rolls back to the immediately previous release. Use one
of the targeting flags to pick a specific release:

  --release <id>     full release UUID (paste from 'releases logs'
                     or 'rollback --list' output)
  --version <ver>    release version string (e.g. 20260513.160342)

The --list flag prints the recent release table so you can pick
which one to roll back to.

Watch the rollout in real time with --watch — runtime logs will
stream so you can confirm the previous version comes up healthy.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runRollback,
}

func init() {
	rollbackCmd.Flags().StringP("version", "v", "", "Target release version (e.g. 20260513.160342)")
	rollbackCmd.Flags().StringP("release", "r", "", "Target release ID (full UUID)")
	rollbackCmd.Flags().BoolP("list", "l", false, "List available releases for rollback")
	rollbackCmd.Flags().BoolP("watch", "w", false, "Watch rollback progress until completion")
}

func runRollback(cmd *cobra.Command, args []string) error {
	client, err := getClient()
	if err != nil {
		return err
	}

	var appID string
	var app *api.App

	if len(args) > 0 {
		appID = args[0]
		app, err = client.GetApp(appID)
		if err != nil {
			return fmt.Errorf("failed to get app: %w", err)
		}
	} else {
		// Try to detect app from current directory
		app, err = detectAppFromDirectory(client)
		if err != nil {
			return err
		}
		appID = app.Slug
		color.New(color.FgCyan).Printf("Detected app: %s\n", app.Name)
	}

	listReleases, _ := cmd.Flags().GetBool("list")
	if listReleases {
		return listReleasesForRollback(client, appID)
	}

	version, _ := cmd.Flags().GetString("version")
	releaseID, _ := cmd.Flags().GetString("release")
	watch, _ := cmd.Flags().GetBool("watch")

	if version != "" && releaseID != "" {
		// Defensive: server-side find_rollback_target/2 prefers
		// release_id when both are present, but the precedence isn't
		// surfaced to the user. Failing here keeps "what release am I
		// actually rolling back to" unambiguous.
		return fmt.Errorf("--version and --release are mutually exclusive (pick one)")
	}

	// Build rollback parameters
	params := map[string]interface{}{}
	if version != "" {
		params["version"] = version
	}
	if releaseID != "" {
		params["release_id"] = releaseID
	}

	fmt.Printf("Rolling back %s...\n", app.Name)

	resp, err := client.RollbackApp(appID, params)
	if err != nil {
		return fmt.Errorf("failed to rollback: %w", err)
	}

	color.New(color.FgGreen, color.Bold).Println("Rollback started!")
	fmt.Printf("Target version: %s\n", resp.ReleaseVersion)
	fmt.Printf("Deployment ID: %s\n", resp.DeploymentID)

	if watch {
		fmt.Println("\nWatching rollback progress...")
		// Rollbacks reuse a pre-built image — no build log to stream.
		// Runtime logs ARE useful: a rollback's whole purpose is to
		// recover from a bad state, and seeing the previous version
		// boot cleanly is the confirmation users want.
		return watchDeployment(appID, "", false, true)
	}

	fmt.Println("\nWatch progress with:")
	fmt.Printf("  deployado rollback %s --watch\n", appID)

	return nil
}

func listReleasesForRollback(client *api.Client, appID string) error {
	releases, err := client.ListReleases(appID, 10)
	if err != nil {
		return fmt.Errorf("failed to list releases: %w", err)
	}

	if len(releases) == 0 {
		fmt.Println("No releases available for rollback.")
		return nil
	}

	fmt.Println("Available releases for rollback:")
	fmt.Println()

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "RELEASE ID\tVERSION\tSTATUS\tCOMMIT\tCREATED\tCURRENT")
	fmt.Fprintln(w, "----------\t-------\t------\t------\t-------\t-------")

	for _, r := range releases {
		current := ""
		if r.IsCurrent {
			current = "←"
		}

		commitSha := ""
		if r.CommitSHA != "" && len(r.CommitSHA) >= 7 {
			commitSha = r.CommitSHA[:7]
		}

		// Full UUID, not the 8-char prefix — both --release and
		// `releases logs` require the full id, and printing the
		// truncated version here would just mean a second lookup
		// every time the user wants to act on a row.
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			r.ID,
			r.Version,
			r.Status,
			commitSha,
			formatRelativeTime(r.CreatedAt),
			current,
		)
	}

	w.Flush()

	fmt.Println()
	fmt.Println("Rollback to a specific release:")
	fmt.Printf("  deployado rollback %s --release <RELEASE-ID>\n", appID)
	fmt.Println("Or by version:")
	fmt.Printf("  deployado rollback %s --version <VERSION>\n", appID)

	return nil
}

func formatRelativeTime(timestamp string) string {
	// Simple formatting - just return the date portion for now
	if len(timestamp) >= 10 {
		return timestamp[:10]
	}
	return timestamp
}

// detectAppFromDirectory is defined in deploy.go
// We reuse it here for rollback command
