package cmd

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/deployado/cli/internal/blueprint"
	"github.com/deployado/cli/internal/detect"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a deployado.yaml blueprint file",
	Long: `Initialize a deployado.yaml blueprint file for your project.

This command analyzes your project structure and generates a blueprint
file that can be used with 'deployado up' to deploy your entire stack.

Examples:
  # Initialize in current directory
  deployado init

  # Initialize with a specific project name
  deployado init --name my-project

  # Add a database to the blueprint
  deployado init --with-db

  # Initialize for a monorepo with multiple services
  deployado init --monorepo`,
	RunE: runInit,
}

var (
	initName      string
	initWithDB    bool
	initDBVersion string
	initMonorepo  bool
	initForce     bool
)

func init() {
	initCmd.Flags().StringVarP(&initName, "name", "n", "", "Project name")
	initCmd.Flags().BoolVar(&initWithDB, "with-db", false, "Add a PostgreSQL database")
	initCmd.Flags().StringVar(&initDBVersion, "db-version", "18", "PostgreSQL version (14, 15, 16, 17, 18)")
	initCmd.Flags().BoolVar(&initMonorepo, "monorepo", false, "Scan subdirectories for multiple services")
	initCmd.Flags().BoolVarP(&initForce, "force", "f", false, "Overwrite existing blueprint file")

	rootCmd.AddCommand(initCmd)
}

func runInit(cmd *cobra.Command, args []string) error {
	dir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	// Check if blueprint already exists
	for _, name := range blueprint.DefaultBlueprintNames {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			if !initForce {
				return fmt.Errorf("blueprint file '%s' already exists. Use --force to overwrite", name)
			}
		}
	}

	// Determine project name
	projectName := initName
	if projectName == "" {
		projectName = filepath.Base(dir)
	}

	color.New(color.FgCyan, color.Bold).Printf("Initializing blueprint for '%s'...\n\n", projectName)

	// Detect git repository
	repoURL, branch := detectGitRepository(dir)

	// Create blueprint
	bp := &blueprint.Blueprint{
		Name:       projectName,
		Repository: repoURL,
		Branch:     branch,
		Services:   []blueprint.Service{},
		Databases:  []blueprint.Database{},
	}

	// Detect services
	if initMonorepo {
		// Scan subdirectories
		services, err := scanMonorepo(dir)
		if err != nil {
			return err
		}

		// Let user select which services to include
		if len(services) > 1 {
			services, err = selectServices(services)
			if err != nil {
				return err
			}
		}

		bp.Services = services
	} else {
		// Single service in current directory
		service := detectService(dir, "")
		if service != nil {
			bp.Services = append(bp.Services, *service)
		} else {
			// If no deployable service found in root, automatically scan subdirectories
			// This handles cases like Flutter apps with a website/ subdirectory
			color.New(color.FgYellow).Println("No deployable service found in root directory, scanning subdirectories...")
			fmt.Println()

			services, err := scanMonorepo(dir)
			if err != nil {
				return err
			}

			if len(services) > 0 {
				// Let user select which services to include
				if len(services) > 1 {
					services, err = selectServices(services)
					if err != nil {
						return err
					}
				}
				bp.Services = services
			}
		}
	}

	if len(bp.Services) == 0 {
		color.New(color.FgYellow).Println("No services detected. Creating a minimal blueprint.")
		bp.Services = append(bp.Services, blueprint.Service{
			Name:    projectName,
			Type:    "web",
			Runtime: "docker",
		})
	}

	// Add database if requested
	if initWithDB {
		// Use project name as database name base (sanitized)
		dbName := sanitizeDBName(projectName) + "-db"
		bp.Databases = append(bp.Databases, blueprint.Database{
			Name:    dbName,
			Version: initDBVersion,
		})

		// Add database connection to first service
		if len(bp.Services) > 0 {
			bp.Services[0].EnvVars = append(bp.Services[0].EnvVars, blueprint.EnvVar{
				Key: "DATABASE_URL",
				FromDatabase: &blueprint.DatabaseRef{
					Name:     dbName,
					Property: "connectionString",
				},
			})
		}
	}

	// Print summary
	printInitSummary(bp)

	// Ask for confirmation
	reader := bufio.NewReader(os.Stdin)
	fmt.Print("\nGenerate deployado.yaml? (Y/n): ")
	confirm, _ := reader.ReadString('\n')
	confirm = strings.TrimSpace(strings.ToLower(confirm))
	if confirm != "" && confirm != "y" && confirm != "yes" {
		fmt.Println("Cancelled.")
		return nil
	}

	// Generate YAML
	data, err := yaml.Marshal(bp)
	if err != nil {
		return fmt.Errorf("failed to generate YAML: %w", err)
	}

	// Add header comment
	header := `# Deployado Blueprint
# Deploy with: deployado up --project <project-id>
# Docs: https://docs.deployado.com/blueprints

`
	content := header + string(data)

	// Write file
	outputPath := filepath.Join(dir, "deployado.yaml")
	if err := os.WriteFile(outputPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write blueprint: %w", err)
	}

	fmt.Println()
	color.New(color.FgGreen, color.Bold).Println("Blueprint created: deployado.yaml")
	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Println("  1. Review and customize the blueprint")
	fmt.Println("  2. Deploy with: deployado up --project <project-id>")

	return nil
}

func selectServices(services []blueprint.Service) ([]blueprint.Service, error) {
	fmt.Printf("\nDetected %d services:\n", len(services))

	for i, svc := range services {
		path := svc.Path
		if path == "" {
			path = "."
		}
		fmt.Printf("  %d. %s (%s) - %s\n", i+1, color.CyanString(svc.Name), svc.Runtime, path)
	}

	fmt.Println()
	fmt.Println("Enter service numbers to include (comma-separated), or press Enter for all:")
	fmt.Print("> ")

	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)

	// If empty, include all
	if input == "" {
		return services, nil
	}

	// Parse selected numbers
	selected := []blueprint.Service{}
	parts := strings.Split(input, ",")

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		var num int
		if _, err := fmt.Sscanf(part, "%d", &num); err != nil {
			color.New(color.FgYellow).Printf("Skipping invalid input: %s\n", part)
			continue
		}

		if num < 1 || num > len(services) {
			color.New(color.FgYellow).Printf("Skipping out of range: %d\n", num)
			continue
		}

		selected = append(selected, services[num-1])
	}

	if len(selected) == 0 {
		return services, nil // Default to all if none selected
	}

	return selected, nil
}

func scanMonorepo(dir string) ([]blueprint.Service, error) {
	var services []blueprint.Service
	scannedPaths := make(map[string]bool)

	// Scan all immediate subdirectories
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}

		subdir := filepath.Join(dir, entry.Name())
		relPath := entry.Name()

		// Try to detect service in this directory
		service := detectService(subdir, relPath)
		if service != nil {
			services = append(services, *service)
			scannedPaths[subdir] = true
			continue
		}

		// If not found, scan one level deeper
		subentries, err := os.ReadDir(subdir)
		if err != nil {
			continue
		}

		for _, subentry := range subentries {
			if !subentry.IsDir() || strings.HasPrefix(subentry.Name(), ".") {
				continue
			}

			deepDir := filepath.Join(subdir, subentry.Name())
			deepRelPath := filepath.Join(relPath, subentry.Name())

			if scannedPaths[deepDir] {
				continue
			}

			service := detectService(deepDir, deepRelPath)
			if service != nil {
				services = append(services, *service)
				scannedPaths[deepDir] = true
			}
		}
	}

	// If no matches found, try current directory
	if len(services) == 0 {
		service := detectService(dir, "")
		if service != nil {
			services = append(services, *service)
		}
	}

	return services, nil
}

func detectService(dir, relPath string) *blueprint.Service {
	// Check if this is a Flutter web subdirectory first
	if detect.IsFlutterWebSubdir(dir) {
		return nil
	}

	stackInfo := detect.DetectStack(dir)
	if stackInfo == nil || stackInfo.Stack == detect.StackUnknown {
		return nil
	}

	// Skip Flutter projects - they're not deployable as web services
	if stackInfo.Stack == detect.StackFlutter {
		return nil
	}

	name := stackInfo.AppName
	if name == "" {
		name = filepath.Base(dir)
	}

	// Determine service type
	svcType := "web"
	if strings.Contains(strings.ToLower(name), "worker") {
		svcType = "worker"
	}

	service := &blueprint.Service{
		Name:           name,
		Type:           svcType,
		Runtime:        stackToRuntime(stackInfo.Stack),
		RuntimeVersion: stackInfo.RuntimeVersion,
		Port:           stackInfo.Port,
	}

	if relPath != "" && relPath != "." {
		service.Path = "./" + relPath
	}

	// Add stack-specific configuration
	addStackDefaults(service, stackInfo)

	return service
}

func addStackDefaults(service *blueprint.Service, stackInfo *detect.StackInfo) {
	switch stackInfo.Stack {
	case detect.StackPhoenix, detect.StackElixir:
		service.PreDeployCommand = "/app/bin/migrate"
		service.EnvVars = append(service.EnvVars,
			blueprint.EnvVar{Key: "PHX_HOST", Value: service.Name + ".deployado.com"},
			blueprint.EnvVar{Key: "SECRET_KEY_BASE", GenerateSecret: true},
		)

	case detect.StackDjango:
		service.PreDeployCommand = "python manage.py migrate --noinput"
		service.EnvVars = append(service.EnvVars,
			blueprint.EnvVar{Key: "DJANGO_SECRET_KEY", GenerateSecret: true},
			blueprint.EnvVar{Key: "DJANGO_ALLOWED_HOSTS", Value: "*"},
		)

	case detect.StackRails:
		service.PreDeployCommand = "bundle exec rails db:migrate"
		service.EnvVars = append(service.EnvVars,
			blueprint.EnvVar{Key: "RAILS_ENV", Value: "production"},
			blueprint.EnvVar{Key: "SECRET_KEY_BASE", GenerateSecret: true},
		)

	case detect.StackDotNet:
		service.EnvVars = append(service.EnvVars,
			blueprint.EnvVar{Key: "ASPNETCORE_ENVIRONMENT", Value: "Production"},
			blueprint.EnvVar{Key: "ASPNETCORE_URLS", Value: "http://+:8080"},
		)

	case detect.StackNode:
		service.EnvVars = append(service.EnvVars,
			blueprint.EnvVar{Key: "NODE_ENV", Value: "production"},
		)

	case detect.StackStatic:
		service.HealthCheck = &blueprint.HealthCheck{
			Path: "/",
		}

		// Static sites usually need API URLs
		service.EnvVars = append(service.EnvVars,
			blueprint.EnvVar{
				Key:         "VITE_API_URL",
				Description: "URL of the API backend",
				Prompt:      true,
			},
		)
	}
}

// detectGitRepository extracts the repository URL and branch from git config
func detectGitRepository(dir string) (string, string) {
	// Try to get remote URL
	cmd := exec.Command("git", "-C", dir, "remote", "get-url", "origin")
	output, err := cmd.Output()
	if err != nil {
		return "", ""
	}

	remoteURL := strings.TrimSpace(string(output))
	repoURL := normalizeGitURL(remoteURL)

	// Try to get current branch
	cmd = exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD")
	output, err = cmd.Output()
	branch := "main"
	if err == nil {
		branch = strings.TrimSpace(string(output))
	}

	// Don't include branch if it's main (default)
	if branch == "main" {
		branch = ""
	}

	return repoURL, branch
}

// normalizeGitURL converts various git URL formats to github.com/owner/repo format
func normalizeGitURL(url string) string {
	// Handle SSH format: git@github.com:owner/repo.git
	sshRegex := regexp.MustCompile(`^git@([^:]+):(.+?)(?:\.git)?$`)
	if matches := sshRegex.FindStringSubmatch(url); matches != nil {
		return matches[1] + "/" + matches[2]
	}

	// Handle HTTPS format: https://github.com/owner/repo.git
	httpsRegex := regexp.MustCompile(`^https?://([^/]+)/(.+?)(?:\.git)?$`)
	if matches := httpsRegex.FindStringSubmatch(url); matches != nil {
		return matches[1] + "/" + matches[2]
	}

	return url
}

func printInitSummary(bp *blueprint.Blueprint) {
	fmt.Printf("Detected configuration:\n\n")

	if bp.Repository != "" {
		fmt.Printf("Repository: %s\n", color.CyanString(bp.Repository))
		if bp.Branch != "" {
			fmt.Printf("Branch:     %s\n", bp.Branch)
		}
		fmt.Println()
	}

	if len(bp.Databases) > 0 {
		fmt.Printf("Databases:\n")
		for _, db := range bp.Databases {
			fmt.Printf("  - %s (PostgreSQL %s)\n", db.Name, db.Version)
		}
		fmt.Println()
	}

	fmt.Printf("Services:\n")
	for _, svc := range bp.Services {
		path := svc.Path
		if path == "" {
			path = "."
		}
		fmt.Printf("  - %s\n", color.CyanString(svc.Name))
		fmt.Printf("      Type:    %s\n", svc.Type)
		if svc.RuntimeVersion != "" {
			fmt.Printf("      Runtime: %s %s\n", svc.Runtime, svc.RuntimeVersion)
		} else {
			fmt.Printf("      Runtime: %s\n", svc.Runtime)
		}
		if svc.Port > 0 {
			fmt.Printf("      Port:    %d\n", svc.Port)
		}
		if path != "." {
			fmt.Printf("      Path:    %s\n", path)
		}
		if svc.PreDeployCommand != "" {
			fmt.Printf("      Pre-deploy: %s\n", svc.PreDeployCommand)
		}
		if len(svc.EnvVars) > 0 {
			fmt.Printf("      Env vars: %d configured\n", len(svc.EnvVars))
		}
	}
}

// sanitizeDBName converts a project name to a valid database name
// It lowercases, replaces spaces and special chars with hyphens, and removes invalid chars
func sanitizeDBName(name string) string {
	// Lowercase
	name = strings.ToLower(name)

	// Replace underscores and spaces with hyphens
	name = strings.ReplaceAll(name, "_", "-")
	name = strings.ReplaceAll(name, " ", "-")

	// Remove any characters that aren't alphanumeric or hyphens
	re := regexp.MustCompile(`[^a-z0-9-]`)
	name = re.ReplaceAllString(name, "")

	// Remove consecutive hyphens
	re = regexp.MustCompile(`-+`)
	name = re.ReplaceAllString(name, "-")

	// Trim leading/trailing hyphens
	name = strings.Trim(name, "-")

	// Ensure it's not empty
	if name == "" {
		name = "app"
	}

	return name
}
