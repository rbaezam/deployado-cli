package cmd

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/deployado/cli/internal/detect"
	"github.com/deployado/cli/internal/dockerfile"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var launchCmd = &cobra.Command{
	Use:   "launch",
	Short: "Launch a new app from the current directory",
	Long: `Launch detects your project stack, generates a Dockerfile if needed,
creates an app in Deployado, and triggers the first deployment.

This is the quickest way to get started with Deployado.`,
	RunE: runLaunch,
}

func init() {
	launchCmd.Flags().StringP("name", "n", "", "App name (default: directory name)")
	launchCmd.Flags().StringP("project", "p", "", "Project ID to create the app in")
	launchCmd.Flags().StringP("server", "s", "", "Server ID to deploy to")
	launchCmd.Flags().IntP("port", "P", 0, "Port the app listens on (default: auto-detected)")
	launchCmd.Flags().Bool("no-dockerfile", false, "Don't generate a Dockerfile")
	launchCmd.Flags().Bool("no-deploy", false, "Create app but don't deploy")
	launchCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompts")
}

func runLaunch(cmd *cobra.Command, args []string) error {
	client, err := getClient()
	if err != nil {
		return err
	}

	dir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	// Flags
	appName, _ := cmd.Flags().GetString("name")
	projectID, _ := cmd.Flags().GetString("project")
	serverID, _ := cmd.Flags().GetString("server")
	portFlag, _ := cmd.Flags().GetInt("port")
	noDockerfile, _ := cmd.Flags().GetBool("no-dockerfile")
	noDeploy, _ := cmd.Flags().GetBool("no-deploy")
	yes, _ := cmd.Flags().GetBool("yes")

	// Step 1: Detect stack
	fmt.Println("Detecting project stack...")
	stackInfo := detect.DetectStack(dir)

	// Check if Dockerfile exists
	gen := dockerfile.NewGenerator(dir)
	hasDockerfile := gen.ExistsDockerfile()

	if stackInfo.Stack == detect.StackUnknown {
		if hasDockerfile {
			color.New(color.FgYellow).Println("Could not automatically detect your project stack.")
			fmt.Println("Using existing Dockerfile.")
			fmt.Println()
			// Set default port for unknown stack with existing Dockerfile
			if stackInfo.Port == 0 {
				stackInfo.Port = 8080
			}
		} else {
			color.New(color.FgYellow).Println("Could not automatically detect your project stack.")
			fmt.Println("Please create a Dockerfile manually and run 'deployado launch' again.")
			return nil
		}
	} else {
		color.New(color.FgGreen).Printf("Detected: %s\n", stackInfo.Name)
		fmt.Printf("  %s\n\n", stackInfo.Description)
	}

	// Override port if specified via flag
	if portFlag > 0 {
		stackInfo.Port = portFlag
	}

	// Step 2: Check/generate Dockerfile
	dockerfileCreated := false

	if !hasDockerfile && !noDockerfile {
		fmt.Println("Generating Dockerfile...")

		content, err := gen.Generate(stackInfo)
		if err != nil {
			return fmt.Errorf("failed to generate Dockerfile: %w", err)
		}

		if !yes {
			fmt.Println()
			fmt.Println("Generated Dockerfile:")
			fmt.Println("---")
			// Show first 20 lines
			lines := strings.Split(content, "\n")
			for i, line := range lines {
				if i >= 20 {
					fmt.Printf("... (%d more lines)\n", len(lines)-20)
					break
				}
				fmt.Println(line)
			}
			fmt.Println("---")
			fmt.Println()

			if !confirm("Write Dockerfile to disk?") {
				fmt.Println("Cancelled.")
				return nil
			}
		}

		if err := gen.WriteDockerfile(content); err != nil {
			return fmt.Errorf("failed to write Dockerfile: %w", err)
		}

		color.New(color.FgGreen).Println("Dockerfile created!")
		dockerfileCreated = true
		fmt.Println()
	} else if hasDockerfile && stackInfo.Stack != detect.StackUnknown {
		// Only print this if we didn't already print it in the unknown stack case
		fmt.Println("Using existing Dockerfile.")
		fmt.Println()
	}

	// Step 2.5: Check if Dockerfile is committed to git
	dockerfileGitStatus := getDockerfileGitStatus(dir)
	if dockerfileGitStatus != "committed" {
		fmt.Println()
		if dockerfileCreated {
			color.New(color.FgYellow, color.Bold).Println("⚠️  The Dockerfile needs to be pushed to your repository.")
		} else {
			color.New(color.FgYellow, color.Bold).Println("⚠️  Warning: Dockerfile is not committed to your repository.")
		}
		fmt.Println("   The build will fail unless you push it first.")
		fmt.Println()

		if dockerfileGitStatus == "untracked" || dockerfileGitStatus == "modified" {
			if yes || confirm("Commit and push Dockerfile now?") {
				if err := commitAndPushDockerfile(dir); err != nil {
					color.New(color.FgRed).Printf("   Failed to push: %v\n", err)
					fmt.Println("   Please commit and push the Dockerfile manually before deploying.")
					fmt.Println()
					if !yes && !confirm("Continue anyway?") {
						return fmt.Errorf("deployment cancelled - Dockerfile not in repository")
					}
				} else {
					color.New(color.FgGreen).Println("   Dockerfile committed and pushed!")
					fmt.Println()
				}
			} else {
				color.New(color.FgYellow).Println("   Please commit and push the Dockerfile manually before deploying.")
				fmt.Println()
				if !yes && !confirm("Continue anyway?") {
					return fmt.Errorf("deployment cancelled - Dockerfile not in repository")
				}
			}
		}
	}

	// Step 3: Get project if not specified
	if projectID == "" {
		projects, err := client.ListProjects()
		if err != nil {
			return fmt.Errorf("failed to list projects: %w", err)
		}

		if len(projects) == 0 {
			return fmt.Errorf("no projects found. Create a project first in the Deployado dashboard")
		}

		if len(projects) == 1 {
			projectID = projects[0].ID
			fmt.Printf("Using project: %s\n", projects[0].Name)
		} else {
			fmt.Println("Available projects:")
			for i, p := range projects {
				fmt.Printf("  [%d] %s\n", i+1, p.Name)
			}
			fmt.Print("Select project (number): ")

			var selection int
			fmt.Scanln(&selection)

			if selection < 1 || selection > len(projects) {
				return fmt.Errorf("invalid selection")
			}

			projectID = projects[selection-1].ID
		}
		fmt.Println()
	}

	// Step 4: Get server if not specified
	if serverID == "" {
		servers, err := client.ListServers()
		if err != nil {
			return fmt.Errorf("failed to list servers: %w", err)
		}

		// Filter online servers with app role
		var availableServers []struct {
			ID   string
			Name string
		}
		for _, s := range servers {
			if s.Status == "online" {
				availableServers = append(availableServers, struct {
					ID   string
					Name string
				}{s.ID, s.Name})
			}
		}

		if len(availableServers) == 0 {
			return fmt.Errorf("no online servers available. Add a server first")
		}

		if len(availableServers) == 1 {
			serverID = availableServers[0].ID
			fmt.Printf("Using server: %s\n", availableServers[0].Name)
		} else {
			fmt.Println("Available servers:")
			for i, s := range availableServers {
				fmt.Printf("  [%d] %s\n", i+1, s.Name)
			}
			fmt.Print("Select server (number): ")

			var selection int
			fmt.Scanln(&selection)

			if selection < 1 || selection > len(availableServers) {
				return fmt.Errorf("invalid selection")
			}

			serverID = availableServers[selection-1].ID
		}
		fmt.Println()
	}

	// Step 5: Determine app name
	if appName == "" {
		defaultName := sanitizeAppName(filepath.Base(dir))

		if yes {
			// In non-interactive mode, use the default
			appName = defaultName
		} else {
			// Prompt user for app name
			fmt.Printf("App name [%s]: ", defaultName)
			reader := bufio.NewReader(os.Stdin)
			input, _ := reader.ReadString('\n')
			input = strings.TrimSpace(input)

			if input == "" {
				appName = defaultName
			} else {
				appName = sanitizeAppName(input)
			}
		}
	} else {
		// Name was provided via flag, sanitize it
		appName = sanitizeAppName(appName)
	}

	if !yes {
		fmt.Printf("Port: %d\n", stackInfo.Port)
		fmt.Println()

		if !confirm("Create app and deploy?") {
			fmt.Println("Cancelled.")
			return nil
		}
	}

	// Step 6: Detect git repository URL
	repoURL := detectGitRemote(dir)
	if repoURL == "" {
		fmt.Print("Repository URL (or press Enter to skip): ")
		reader := bufio.NewReader(os.Stdin)
		repoURL, _ = reader.ReadString('\n')
		repoURL = strings.TrimSpace(repoURL)
	} else {
		fmt.Printf("Repository: %s\n", repoURL)
	}

	// Step 7: Create app
	fmt.Println()
	fmt.Println("Creating app...")

	params := map[string]interface{}{
		"project_id":      projectID,
		"name":            appName,
		"repository_url":  repoURL,
		"branch":          "main",
		"dockerfile_path": "Dockerfile",
		"build_context":   ".",
		"port":            stackInfo.Port,
		"health_check_path": defaultHealthCheckPath(
			stackToRuntime(stackInfo.Stack),
			stackInfo.Stack,
			dir,
		),
		"server_id": serverID,
	}

	app, err := client.CreateApp(params)
	if err != nil {
		return handleCreateAppError(err, appName)
	}

	color.New(color.FgGreen, color.Bold).Println("App created!")
	fmt.Printf("  ID:   %s\n", app.ID)
	fmt.Printf("  Name: %s\n", app.Name)
	fmt.Printf("  URL:  https://%s.deployado.com\n", app.Slug)
	fmt.Println()

	// Step 8: Configure environment variables based on stack
	// Use app.Slug for the subdomain (e.g., myapp.deployado.com)
	envVars := detect.GetRequiredEnvVars(stackInfo.Stack, app.Slug)

	// For .NET apps, also extract variables from appsettings.json
	if stackInfo.Stack == detect.StackDotNet {
		appSettingsVars := detect.ExtractDotNetEnvVars(dir)
		if len(appSettingsVars) > 0 {
			fmt.Printf("Found %d environment variables in appsettings.json\n", len(appSettingsVars))
			envVars = append(envVars, appSettingsVars...)
		}
	}

	// For static sites, extract variables from .env files
	if stackInfo.Stack == detect.StackStatic {
		staticEnvVars := detect.ExtractStaticEnvVars(dir)
		if len(staticEnvVars) > 0 {
			fmt.Printf("Found %d environment variables in .env files\n", len(staticEnvVars))
			envVars = append(envVars, staticEnvVars...)
		}

		// Add API URL placeholder for frontend apps
		siteInfo := detect.DetectStaticSite(dir)
		if siteInfo != nil && siteInfo.EnvPrefix != "" {
			apiUrlKey := siteInfo.EnvPrefix + "API_URL"
			// Check if already exists
			exists := false
			for _, ev := range envVars {
				if ev.Key == apiUrlKey {
					exists = true
					break
				}
			}
			if !exists {
				envVars = append(envVars, detect.EnvVar{
					Key:         apiUrlKey,
					Value:       "",
					Description: "API URL for backend connection",
					Required:    false,
					Secret:      false,
				})
			}
		}
	}

	if len(envVars) > 0 {
		fmt.Println("Configuring environment variables...")

		envMap := make(map[string]string)
		for _, ev := range envVars {
			value := ev.Value

			// Generate secure values for secrets
			if ev.Secret && value == "" {
				value = generateSecretKey()
			}

			// Skip empty non-secret values (user needs to set these)
			if value == "" {
				continue
			}

			envMap[ev.Key] = value
		}

		if len(envMap) > 0 {
			runtimeCount, buildCount, err := setEnvWithAutoScope(client, app.ID, envMap)
			if err != nil {
				color.New(color.FgYellow).Printf("Warning: Failed to set environment variables: %v\n", err)
			} else {
				color.New(color.FgGreen).Printf("  Configured %d environment variables\n", len(envMap))
				for key := range envMap {
					fmt.Printf("    - %s\n", key)
				}
				if buildCount > 0 {
					fmt.Printf("  Scope split: %d runtime, %d build\n", runtimeCount, buildCount)
				}
			}
		}

		// Show variables that need manual configuration
		var needsConfig []string
		for _, ev := range envVars {
			if ev.Required && ev.Value == "" && !ev.Secret {
				needsConfig = append(needsConfig, ev.Key)
			}
		}
		if len(needsConfig) > 0 {
			fmt.Println()
			color.New(color.FgYellow).Println("  The following variables need to be configured:")
			for _, key := range needsConfig {
				fmt.Printf("    - %s\n", key)
			}
			fmt.Println("  Set them in the Deployado dashboard or with: deployado env set <KEY>=<VALUE>")
		}
		fmt.Println()
	}

	// Step 8.5: Configure pre-deploy command for stacks that need migrations
	preDeployCmd := detect.GetPreDeployCommand(stackInfo.Stack)
	if preDeployCmd != "" {
		fmt.Println("Configuring pre-deploy command (for database migrations)...")

		updateParams := map[string]interface{}{
			"pre_deploy_command": preDeployCmd,
		}

		if _, err := client.UpdateApp(app.ID, updateParams); err != nil {
			color.New(color.FgYellow).Printf("Warning: Failed to set pre-deploy command: %v\n", err)
		} else {
			color.New(color.FgGreen).Printf("  Pre-deploy command: %s\n", preDeployCmd)
		}
		fmt.Println()
	}

	// Step 9: Deploy (unless --no-deploy)
	if noDeploy {
		fmt.Println("Skipping deployment (--no-deploy flag).")
		fmt.Println("\nTo deploy later, run:")
		fmt.Printf("  deployado deploy %s\n", app.ID)
		return nil
	}

	fmt.Println("Starting deployment...")

	resp, err := client.DeployApp(app.ID, nil)
	if err != nil {
		return fmt.Errorf("failed to start deployment: %w", err)
	}

	color.New(color.FgGreen, color.Bold).Println("Deployment started!")
	fmt.Printf("  Release ID: %s\n", resp.ReleaseID)

	fmt.Println()
	fmt.Println("Your app is being deployed. View status in the Deployado dashboard.")
	fmt.Println()
	fmt.Println("To view logs:")
	fmt.Printf("  deployado logs %s\n", app.ID)

	return nil
}

func confirm(prompt string) bool {
	fmt.Printf("%s [y/N]: ", prompt)

	var response string
	fmt.Scanln(&response)

	response = strings.ToLower(strings.TrimSpace(response))
	return response == "y" || response == "yes"
}

func sanitizeAppName(name string) string {
	// Convert to lowercase
	name = strings.ToLower(name)

	// Replace invalid characters with hyphens
	var result strings.Builder
	for _, c := range name {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' {
			result.WriteRune(c)
		} else if c == '_' || c == ' ' {
			result.WriteRune('-')
		}
	}

	// Remove leading/trailing hyphens
	return strings.Trim(result.String(), "-")
}

func handleCreateAppError(err error, appName string) error {
	errStr := err.Error()

	// Check for duplicate slug error
	if strings.Contains(errStr, "app slug already exists") {
		fmt.Println()
		color.New(color.FgRed, color.Bold).Println("Error: An app with this name already exists in the project.")
		fmt.Println()
		fmt.Println("You can either:")
		fmt.Printf("  1. Use a different name: deployado launch -n %s-2\n", appName)
		fmt.Println("  2. Delete the existing app from the Deployado dashboard (Settings > Danger Zone)")
		fmt.Println("  3. Deploy to the existing app: deployado deploy <app-id>")
		fmt.Println()
		return fmt.Errorf("app '%s' already exists in this project", appName)
	}

	// Check for validation errors
	if strings.Contains(errStr, "validation_failed") {
		fmt.Println()
		color.New(color.FgRed, color.Bold).Println("Error: Validation failed")
		fmt.Println()
		// Extract and display the specific validation errors
		if strings.Contains(errStr, "slug:") {
			fmt.Println("  - App name/slug is invalid. Use only lowercase letters, numbers, and hyphens.")
		}
		if strings.Contains(errStr, "name:") {
			fmt.Println("  - App name is required.")
		}
		if strings.Contains(errStr, "repository_url:") {
			fmt.Println("  - Repository URL is invalid. Use a valid HTTPS URL.")
		}
		fmt.Println()
		return fmt.Errorf("validation failed: %s", errStr)
	}

	// Check for not found errors
	if strings.Contains(errStr, "not_found") {
		fmt.Println()
		color.New(color.FgRed, color.Bold).Println("Error: Resource not found")
		fmt.Println()
		if strings.Contains(errStr, "Project") {
			fmt.Println("  The specified project was not found. Check your project ID.")
		} else {
			fmt.Println("  The specified resource was not found.")
		}
		fmt.Println()
		return fmt.Errorf("resource not found")
	}

	// Generic error
	return fmt.Errorf("failed to create app: %w", err)
}

func detectGitRemote(dir string) string {
	// Try to read .git/config
	gitConfig := filepath.Join(dir, ".git", "config")
	data, err := os.ReadFile(gitConfig)
	if err != nil {
		return ""
	}

	// Simple parsing for origin URL
	lines := strings.Split(string(data), "\n")
	inOrigin := false

	for _, line := range lines {
		line = strings.TrimSpace(line)

		if line == "[remote \"origin\"]" {
			inOrigin = true
			continue
		}

		if inOrigin {
			if strings.HasPrefix(line, "[") {
				break
			}

			if strings.HasPrefix(line, "url = ") {
				url := strings.TrimPrefix(line, "url = ")
				// Convert SSH URL to HTTPS
				if strings.HasPrefix(url, "git@github.com:") {
					url = strings.Replace(url, "git@github.com:", "https://github.com/", 1)
					url = strings.TrimSuffix(url, ".git")
				}
				return url
			}
		}
	}

	return ""
}

// generateSecretKey generates a cryptographically secure random key
// suitable for use as SECRET_KEY_BASE, DJANGO_SECRET_KEY, etc.
func generateSecretKey() string {
	bytes := make([]byte, 64)
	if _, err := rand.Read(bytes); err != nil {
		// This should never happen, but return empty to skip setting the var
		return ""
	}
	return base64.URLEncoding.EncodeToString(bytes)
}

// getDockerfileGitStatus checks if Dockerfile is tracked in git
// Returns: "committed", "untracked", "modified", or "no-git"
func getDockerfileGitStatus(dir string) string {
	// Check if this is a git repository
	gitDir := filepath.Join(dir, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		return "no-git"
	}

	// Check if Dockerfile exists
	dockerfilePath := filepath.Join(dir, "Dockerfile")
	if _, err := os.Stat(dockerfilePath); os.IsNotExist(err) {
		return "no-dockerfile"
	}

	// Run git status --porcelain to check Dockerfile status
	cmd := execCommand("git", "status", "--porcelain", "Dockerfile")
	cmd.Dir = dir
	output, err := cmd.Output()
	if err != nil {
		return "no-git"
	}

	status := strings.TrimSpace(string(output))

	// If output is empty, file is committed and unchanged
	if status == "" {
		// But we should also verify it's actually tracked
		cmd = execCommand("git", "ls-files", "Dockerfile")
		cmd.Dir = dir
		output, err = cmd.Output()
		if err != nil || strings.TrimSpace(string(output)) == "" {
			return "untracked"
		}
		return "committed"
	}

	// Check the status code
	if strings.HasPrefix(status, "??") {
		return "untracked"
	}

	if strings.HasPrefix(status, " M") || strings.HasPrefix(status, "M ") || strings.HasPrefix(status, "MM") {
		return "modified"
	}

	if strings.HasPrefix(status, "A ") {
		return "staged"
	}

	return "modified"
}

// commitAndPushDockerfile commits and pushes the Dockerfile to the remote repository
func commitAndPushDockerfile(dir string) error {
	// Stage the Dockerfile
	cmd := execCommand("git", "add", "Dockerfile")
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git add failed: %s", string(output))
	}

	// Commit
	cmd = execCommand("git", "commit", "-m", "Add Dockerfile for Deployado")
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		// Check if nothing to commit (already staged)
		if strings.Contains(string(output), "nothing to commit") {
			// Already committed, just need to push
		} else {
			return fmt.Errorf("git commit failed: %s", string(output))
		}
	}

	// Push
	cmd = execCommand("git", "push")
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git push failed: %s", string(output))
	}

	return nil
}

// execCommand is a wrapper for exec.Command that can be mocked in tests
var execCommand = func(name string, args ...string) *exec.Cmd {
	return exec.Command(name, args...)
}
