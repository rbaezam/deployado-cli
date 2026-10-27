package cmd

import (
	"fmt"
	"strings"

	"github.com/deployado/cli/internal/api"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var psCmd = &cobra.Command{
	Use:   "ps [app]",
	Short: "Show running deployments and their status",
	Long: `Display the status of running deployments.

If no app is specified, shows all running deployments.
If an app is specified, shows detailed info for that app.

Examples:
  # List all running apps
  deployado ps

  # Show details for a specific app
  deployado ps my-app`,
	Args: cobra.MaximumNArgs(1),
	RunE: runPs,
}

func init() {
	rootCmd.AddCommand(psCmd)
}

func runPs(cmd *cobra.Command, args []string) error {
	client, err := getClient()
	if err != nil {
		return err
	}

	if len(args) > 0 {
		// Show specific app
		return showAppStatus(client, args[0])
	}

	// List all apps with their status
	apps, err := client.ListApps()
	if err != nil {
		return fmt.Errorf("failed to list apps: %w", err)
	}

	if len(apps) == 0 {
		fmt.Println("No apps found.")
		return nil
	}

	// Print header
	fmt.Printf("%-20s %-12s %-15s %-20s\n", "APP", "STATUS", "VERSION", "SERVER")
	fmt.Println(strings.Repeat("-", 70))

	for _, app := range apps {
		deployment, _ := client.GetCurrentDeployment(app.ID)

		status := "-"
		version := "-"
		serverName := "-"

		if deployment != nil {
			status = deployment.Status
			if deployment.Version != "" {
				version = deployment.Version
			}
			serverName = deployment.ServerName
		}

		statusColor := getStatusColor(status)

		fmt.Printf("%-20s ", truncate(app.Name, 20))
		statusColor.Printf("%-12s ", status)
		fmt.Printf("%-15s %-20s\n",
			truncate(version, 15),
			truncate(serverName, 20))
	}

	return nil
}

func showAppStatus(client *api.Client, appIdentifier string) error {
	app, err := resolveApp(client, appIdentifier)
	if err != nil {
		return err
	}

	deployment, _ := client.GetCurrentDeployment(app.ID)

	// Print app info
	bold := color.New(color.Bold)

	bold.Printf("App: %s\n", app.Name)
	fmt.Printf("  Slug:    %s\n", app.Slug)
	fmt.Printf("  Port:    %d\n", app.Port)

	if app.RepositoryURL != "" {
		fmt.Printf("  Repo:    %s\n", app.RepositoryURL)
	}
	if app.Branch != "" {
		fmt.Printf("  Branch:  %s\n", app.Branch)
	}

	fmt.Println()

	if deployment == nil {
		color.Yellow("No active deployment")
		return nil
	}

	bold.Println("Current Deployment:")
	fmt.Printf("  Status:      ")
	getStatusColor(deployment.Status).Println(deployment.Status)
	fmt.Printf("  Version:     %s\n", deployment.Version)
	fmt.Printf("  Server:      %s\n", deployment.ServerName)

	if deployment.ContainerID != nil && *deployment.ContainerID != "" {
		containerID := *deployment.ContainerID
		if len(containerID) > 12 {
			containerID = containerID[:12]
		}
		fmt.Printf("  Container:   %s\n", containerID)
	}

	if deployment.StartedAt != "" {
		fmt.Printf("  Started at:  %s\n", deployment.StartedAt)
	}

	if deployment.Error != nil && *deployment.Error != "" {
		fmt.Printf("  Error:       ")
		color.Red(*deployment.Error)
	}

	return nil
}

func getStatusColor(status string) *color.Color {
	switch status {
	case "running", "healthy":
		return color.New(color.FgGreen)
	case "pending", "deploying", "building":
		return color.New(color.FgYellow)
	case "failed", "error":
		return color.New(color.FgRed)
	case "stopped":
		return color.New(color.FgHiBlack)
	default:
		return color.New(color.FgWhite)
	}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
