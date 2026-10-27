package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/deployado/cli/internal/api"
	"github.com/deployado/cli/internal/config"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var appsCmd = &cobra.Command{
	Use:     "apps",
	Aliases: []string{"app"},
	Short:   "Manage applications",
	Long:    "Commands for listing, viewing, and managing applications.",
}

var appsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all apps",
	RunE:  runAppsList,
}

var appsListProjectID string
var appsListJSON bool

var appsShowCmd = &cobra.Command{
	Use:   "show [app-id]",
	Short: "Show app details",
	Args:  cobra.ExactArgs(1),
	RunE:  runAppsShow,
}

var appsDeleteCmd = &cobra.Command{
	Use:   "delete [app-id]",
	Short: "Delete an app",
	Args:  cobra.ExactArgs(1),
	RunE:  runAppsDelete,
}

var appsUpdateCmd = &cobra.Command{
	Use:   "update [app-id]",
	Short: "Update app configuration",
	Args:  cobra.ExactArgs(1),
	RunE:  runAppsUpdate,
}

var (
	appsUpdateName             string
	appsUpdateRuntime          string
	appsUpdateBranch           string
	appsUpdateBuildContext     string
	appsUpdateDockerfilePath   string
	appsUpdatePreDeployCommand string
	appsUpdateHealthCheckPath  string
	appsUpdatePort             int
	appsUpdateAutoDeploy       bool
	appsUpdateDeployGate       bool
	appsUpdateAutoRollback     bool
	appsUpdateRollbackWindow   int
	appsUpdateWatchPaths       []string
	appsUpdateReadOnlyRoot     bool
	appsUpdateJSON             bool
)

func init() {
	appsCmd.AddCommand(appsListCmd)
	appsCmd.AddCommand(appsShowCmd)
	appsCmd.AddCommand(appsDeleteCmd)
	appsCmd.AddCommand(appsUpdateCmd)

	appsListCmd.Flags().StringVar(&appsListProjectID, "project", "", "Filter by project ID")
	appsListCmd.Flags().BoolVar(&appsListJSON, "json", false, "Output as JSON")
	appsDeleteCmd.Flags().BoolP("force", "f", false, "Skip confirmation prompt")

	appsUpdateCmd.Flags().StringVar(&appsUpdateName, "name", "", "App name")
	appsUpdateCmd.Flags().StringVar(&appsUpdateRuntime, "runtime", "", "Runtime (e.g. python, node, static)")
	appsUpdateCmd.Flags().StringVar(&appsUpdateBranch, "branch", "", "Git branch")
	appsUpdateCmd.Flags().StringVar(&appsUpdateBuildContext, "build-context", "", "Docker build context")
	appsUpdateCmd.Flags().StringVar(&appsUpdateDockerfilePath, "dockerfile-path", "", "Path to Dockerfile")
	appsUpdateCmd.Flags().StringVar(&appsUpdatePreDeployCommand, "pre-deploy-command", "", "Command to run before deploy")
	appsUpdateCmd.Flags().StringVar(&appsUpdateHealthCheckPath, "health-check-path", "", "Health check path")
	appsUpdateCmd.Flags().IntVar(&appsUpdatePort, "port", 0, "Internal app port")
	appsUpdateCmd.Flags().BoolVar(&appsUpdateAutoDeploy, "auto-deploy", false, "Enable auto deploy on push")
	appsUpdateCmd.Flags().BoolVar(&appsUpdateDeployGate, "deploy-gate", false,
		"Gate auto-deploys on GitHub CI (only deploy a commit whose checks pass; pending defers, failure skips)")
	appsUpdateCmd.Flags().BoolVar(&appsUpdateAutoRollback, "auto-rollback", false,
		"Auto-rollback to the previous release if health degrades within the post-deploy window")
	appsUpdateCmd.Flags().IntVar(&appsUpdateRollbackWindow, "rollback-window", 300,
		"Post-deploy health observation window in seconds (30-3600)")
	appsUpdateCmd.Flags().StringSliceVar(&appsUpdateWatchPaths, "watch-paths", nil, "Comma-separated paths to watch for monorepos")
	appsUpdateCmd.Flags().BoolVar(&appsUpdateReadOnlyRoot, "read-only-root", false,
		"Mount container root filesystem as read-only (writes go to tmpfs at /tmp, /run, /var/cache). Triggers redeploy.")
	appsUpdateCmd.Flags().BoolVar(&appsUpdateJSON, "json", false, "Output as JSON")
}

func runAppsList(cmd *cobra.Command, args []string) error {
	client, err := getClient()
	if err != nil {
		return err
	}

	apps, err := client.ListAppsByProject(appsListProjectID)
	if err != nil {
		return fmt.Errorf("failed to list apps: %w", err)
	}

	if appsListJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]interface{}{"apps": apps})
	}

	if len(apps) == 0 {
		fmt.Println("No apps found.")
		fmt.Println("\nCreate your first app with:")
		fmt.Println("  deployado launch")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tBRANCH\tPORT")
	fmt.Fprintln(w, "--\t----\t------\t----")

	for _, app := range apps {
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\n",
			app.ID[:8],
			app.Name,
			app.Branch,
			app.Port,
		)
	}

	w.Flush()
	return nil
}

func runAppsShow(cmd *cobra.Command, args []string) error {
	client, err := getClient()
	if err != nil {
		return err
	}

	appID := args[0]
	app, err := client.GetApp(appID)
	if err != nil {
		return fmt.Errorf("failed to get app: %w", err)
	}

	fmt.Printf("Name:           %s\n", app.Name)
	fmt.Printf("ID:             %s\n", app.ID)
	fmt.Printf("Repository:     %s\n", app.RepositoryURL)
	fmt.Printf("Branch:         %s\n", app.Branch)
	fmt.Printf("Port:           %d\n", app.Port)
	fmt.Printf("Dockerfile:     %s\n", app.DockerfilePath)
	fmt.Printf("Build Context:  %s\n", app.BuildContext)
	fmt.Printf("Auto-deploy:    %v\n", app.AutoDeployEnabled)

	return nil
}

func runAppsDelete(cmd *cobra.Command, args []string) error {
	force, _ := cmd.Flags().GetBool("force")

	client, err := getClient()
	if err != nil {
		return err
	}

	appID := args[0]

	// Get app info for confirmation
	app, err := client.GetApp(appID)
	if err != nil {
		return fmt.Errorf("failed to get app: %w", err)
	}

	if !force {
		color.New(color.FgRed, color.Bold).Printf("Warning: ")
		fmt.Printf("You are about to delete app '%s'.\n", app.Name)
		fmt.Print("Type the app name to confirm: ")

		var confirm string
		fmt.Scanln(&confirm)

		if confirm != app.Name {
			fmt.Println("Deletion cancelled.")
			return nil
		}
	}

	if err := client.DeleteApp(appID); err != nil {
		return fmt.Errorf("failed to delete app: %w", err)
	}

	color.New(color.FgGreen).Printf("App '%s' deleted.\n", app.Name)
	return nil
}

func runAppsUpdate(cmd *cobra.Command, args []string) error {
	client, err := getClient()
	if err != nil {
		return err
	}

	appID := args[0]
	params := map[string]interface{}{}

	if cmd.Flags().Changed("name") {
		params["name"] = appsUpdateName
	}
	if cmd.Flags().Changed("runtime") {
		params["runtime"] = appsUpdateRuntime
	}
	if cmd.Flags().Changed("branch") {
		params["branch"] = appsUpdateBranch
	}
	if cmd.Flags().Changed("build-context") {
		params["build_context"] = appsUpdateBuildContext
	}
	if cmd.Flags().Changed("dockerfile-path") {
		params["dockerfile_path"] = appsUpdateDockerfilePath
	}
	if cmd.Flags().Changed("pre-deploy-command") {
		params["pre_deploy_command"] = appsUpdatePreDeployCommand
	}
	if cmd.Flags().Changed("health-check-path") {
		params["health_check_path"] = appsUpdateHealthCheckPath
	}
	if cmd.Flags().Changed("port") {
		params["port"] = appsUpdatePort
	}
	if cmd.Flags().Changed("auto-deploy") {
		params["auto_deploy_enabled"] = appsUpdateAutoDeploy
	}
	if cmd.Flags().Changed("deploy-gate") {
		params["deploy_gate_enabled"] = appsUpdateDeployGate
	}
	if cmd.Flags().Changed("auto-rollback") {
		params["auto_rollback_enabled"] = appsUpdateAutoRollback
	}
	if cmd.Flags().Changed("rollback-window") {
		params["rollback_window_seconds"] = appsUpdateRollbackWindow
	}
	if cmd.Flags().Changed("watch-paths") {
		params["watch_paths"] = appsUpdateWatchPaths
	}
	if cmd.Flags().Changed("read-only-root") {
		params["read_only_root"] = appsUpdateReadOnlyRoot
	}

	if len(params) == 0 {
		return fmt.Errorf("no update fields provided")
	}

	updated, err := client.UpdateApp(appID, params)
	if err != nil {
		return fmt.Errorf("failed to update app: %w", err)
	}

	if appsUpdateJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]interface{}{"app": updated})
	}

	color.New(color.FgGreen).Printf("App '%s' updated.\n", updated.Name)
	fmt.Printf("ID:             %s\n", updated.ID)
	fmt.Printf("Branch:         %s\n", updated.Branch)
	fmt.Printf("Port:           %d\n", updated.Port)
	fmt.Printf("Dockerfile:     %s\n", updated.DockerfilePath)
	fmt.Printf("Build Context:  %s\n", updated.BuildContext)
	fmt.Printf("Auto-deploy:    %v\n", updated.AutoDeployEnabled)
	fmt.Printf("Deploy gate:    %v\n", updated.DeployGateEnabled)
	fmt.Printf("Auto-rollback:  %v\n", updated.AutoRollbackEnabled)
	return nil
}

// getClient returns an authenticated API client for the active profile
// (honoring the --profile flag and DEPLOYADO_PROFILE env var).
func getClient() (*api.Client, error) {
	p, _, err := config.Active(profileFlag)
	if err != nil {
		if errors.Is(err, config.ErrNoProfile) {
			return nil, fmt.Errorf("not logged in. Run 'deployado auth login' first")
		}
		return nil, err
	}
	return api.NewClient(p.Host, p.AccessToken), nil
}
