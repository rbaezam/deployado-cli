package cmd

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/deployado/cli/internal/api"
	"github.com/deployado/cli/internal/blueprint"
	"github.com/deployado/cli/internal/detect"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var upCmd = &cobra.Command{
	Use:   "up",
	Short: "Deploy your application stack from a blueprint",
	Long: `Deploy your entire application stack defined in a deployado.yaml file.

This command reads the blueprint file and:
  1. Creates any databases defined
  2. Creates or updates services (apps)
  3. Configures environment variables
  4. Links services together
  5. Deploys everything

Examples:
  # Deploy from current directory
  deployado up

  # Deploy from a specific directory
  deployado up --path ./my-project

  # Deploy to a specific project
  deployado up --project proj-123

  # Preview changes without applying
  deployado up --dry-run`,
	RunE: runUp,
}

var (
	upPath      string
	upProjectID string
	upServerID  string
	upDryRun    bool
	upYes       bool
)

func init() {
	upCmd.Flags().StringVarP(&upPath, "path", "p", ".", "Path to project directory or blueprint file")
	upCmd.Flags().StringVar(&upProjectID, "project", "", "Project ID to deploy to")
	upCmd.Flags().StringVarP(&upServerID, "server", "s", "", "Default server ID for services")
	upCmd.Flags().BoolVar(&upDryRun, "dry-run", false, "Preview changes without applying")
	upCmd.Flags().BoolVarP(&upYes, "yes", "y", false, "Skip confirmation prompts")

	rootCmd.AddCommand(upCmd)
}

// deployState tracks created resources for the deployment
type deployState struct {
	client       *api.Client
	blueprint    *blueprint.Blueprint
	projectID    string
	serverID     string
	baseDir      string
	dryRun       bool
	databases    map[string]*api.Database     // name -> database
	services     map[string]*api.App          // name -> app
	envVars      map[string]map[string]string // service name -> env vars
	promptedVars map[string]string            // prompted variable values
}

func runUp(cmd *cobra.Command, args []string) error {
	client, err := getClient()
	if err != nil {
		return err
	}

	// Find blueprint file
	var bp *blueprint.Blueprint
	var baseDir string

	info, err := os.Stat(upPath)
	if err != nil {
		return fmt.Errorf("path not found: %s", upPath)
	}

	if info.IsDir() {
		baseDir = upPath
		bp, err = blueprint.LoadFromDir(upPath)
	} else {
		baseDir = filepath.Dir(upPath)
		bp, err = blueprint.Load(upPath)
	}

	if err != nil {
		return fmt.Errorf("failed to load blueprint: %w", err)
	}

	// Auto-detect git repository and branch if not specified in blueprint
	if bp.Repository == "" {
		if repo, branch := detectGitRepository(baseDir); repo != "" {
			bp.Repository = repo
			fmt.Printf("Auto-detected repository: %s\n", repo)
			if bp.Branch == "" && branch != "" {
				bp.Branch = branch
			}
		}
	}

	// Default branch to main if still not set
	if bp.Branch == "" {
		bp.Branch = "main"
	}

	// Initialize state
	state := &deployState{
		client:       client,
		blueprint:    bp,
		projectID:    upProjectID,
		serverID:     upServerID,
		baseDir:      baseDir,
		dryRun:       upDryRun,
		databases:    make(map[string]*api.Database),
		services:     make(map[string]*api.App),
		envVars:      make(map[string]map[string]string),
		promptedVars: make(map[string]string),
	}

	// Use project from blueprint if not specified via CLI
	if state.projectID == "" && bp.Project != "" {
		state.projectID = bp.Project
	}

	// Validate project exists
	if state.projectID == "" {
		return fmt.Errorf("project ID is required. Use --project or set 'project' in the blueprint")
	}

	// Print summary
	printBlueprintSummary(bp)

	// Collect prompted values
	if err := state.collectPromptedValues(); err != nil {
		return err
	}

	// Confirm
	if !upYes && !upDryRun {
		fmt.Print("\nProceed with deployment? (y/N): ")
		var confirm string
		fmt.Scanln(&confirm)
		if strings.ToLower(confirm) != "y" {
			fmt.Println("Deployment cancelled.")
			return nil
		}
	}

	if upDryRun {
		color.New(color.FgYellow).Println("[DRY RUN] No changes will be made.")
		fmt.Println()
	}

	// Execute deployment steps
	fmt.Println()

	// Step 1: Create databases
	if len(bp.Databases) > 0 {
		color.New(color.FgCyan, color.Bold).Println("Step 1: Creating databases...")
		if err := state.createDatabases(); err != nil {
			return err
		}
	}

	// Step 2: Create services
	color.New(color.FgCyan, color.Bold).Println("\nStep 2: Creating services...")
	if err := state.createServices(); err != nil {
		return err
	}

	// Step 3: Configure environment variables
	color.New(color.FgCyan, color.Bold).Println("\nStep 3: Configuring environment variables...")
	if err := state.configureEnvVars(); err != nil {
		return err
	}

	// Step 3.5: Sync managed database connections from `fromDatabase`
	// references. The blueprint already set the env var values; the
	// managed connection makes the relationship visible in the UI
	// ("Connected Apps") and lets Deployado re-inject the env var on
	// future deploys without depending on `up` running again.
	if err := state.syncDatabaseConnections(); err != nil {
		return err
	}

	// Step 4: Deploy services
	color.New(color.FgCyan, color.Bold).Println("\nStep 4: Deploying services...")
	if err := state.deployServices(); err != nil {
		return err
	}

	// Success
	fmt.Println()
	color.New(color.FgGreen, color.Bold).Println("Deployment complete!")
	fmt.Println()

	// Print service URLs
	printDeployedServices(state)

	return nil
}

func printBlueprintSummary(bp *blueprint.Blueprint) {
	color.New(color.FgCyan, color.Bold).Printf("Blueprint: %s\n", bp.Name)
	fmt.Println()

	if len(bp.Databases) > 0 {
		fmt.Printf("Databases (%d):\n", len(bp.Databases))
		for _, db := range bp.Databases {
			fmt.Printf("  - %s (PostgreSQL %s)\n", db.Name, db.Version)
		}
		fmt.Println()
	}

	fmt.Printf("Services (%d):\n", len(bp.Services))
	for _, svc := range bp.Services {
		svcType := svc.Type
		if svcType == "" {
			svcType = "web"
		}
		runtime := svc.Runtime
		if runtime == "" || runtime == "auto" {
			runtime = "auto-detect"
		}
		fmt.Printf("  - %s (%s, %s)\n", svc.Name, svcType, runtime)
	}
}

func (s *deployState) collectPromptedValues() error {
	reader := bufio.NewReader(os.Stdin)

	for _, svc := range s.blueprint.Services {
		for _, env := range svc.EnvVars {
			if env.Prompt {
				key := fmt.Sprintf("%s.%s", svc.Name, env.Key)
				if _, exists := s.promptedVars[key]; exists {
					continue
				}

				desc := env.Description
				if desc == "" {
					desc = env.Key
				}

				fmt.Printf("\nEnter value for %s (%s): ", env.Key, desc)
				value, err := reader.ReadString('\n')
				if err != nil {
					return fmt.Errorf("failed to read input: %w", err)
				}
				s.promptedVars[key] = strings.TrimSpace(value)
			}
		}
	}

	return nil
}

func (s *deployState) createDatabases() error {
	for _, dbDef := range s.blueprint.Databases {
		fmt.Printf("  Creating database '%s'... ", dbDef.Name)

		if s.dryRun {
			color.New(color.FgYellow).Println("[would create]")
			continue
		}

		serverID := dbDef.Server
		if serverID == "" {
			serverID = s.serverID
		}

		// If no server specified, let the API auto-select
		params := map[string]interface{}{
			"name":            dbDef.Name,
			"db_version":      dbDef.Version,
			"max_connections": dbDef.MaxConnections,
			"backup_enabled":  *dbDef.BackupEnabled,
			"project_id":      s.projectID,
		}

		if serverID != "" {
			params["server_id"] = serverID
		}

		db, err := s.client.CreateDatabase(params)
		if err != nil {
			// Check if database already exists
			if strings.Contains(err.Error(), "already exists") || strings.Contains(err.Error(), "name already exists") {
				fmt.Printf("exists, looking up... ")
				// Try to find the existing database - list all databases (no filter)
				// because project_id filter expects UUID, not project name
				existingDBs, listErr := s.client.ListDatabases("")
				if listErr == nil {
					for i := range existingDBs {
						if existingDBs[i].Name == dbDef.Name {
							db = &existingDBs[i]
							s.databases[dbDef.Name] = db
							color.New(color.FgGreen).Println("found")
							goto nextDatabase
						}
					}
				}
				// If we couldn't find it, report the original error
				color.New(color.FgRed).Printf("failed: %s\n", err)
				return fmt.Errorf("failed to create database '%s': %w", dbDef.Name, err)
			}
			color.New(color.FgRed).Printf("failed: %s\n", err)
			return fmt.Errorf("failed to create database '%s': %w", dbDef.Name, err)
		}

		s.databases[dbDef.Name] = db
		color.New(color.FgGreen).Println("done")

		// Wait for database to be ready
		fmt.Printf("    Waiting for database to be ready... ")
		if err := s.waitForDatabase(db.ID); err != nil {
			color.New(color.FgYellow).Printf("warning: %s\n", err)
		} else {
			color.New(color.FgGreen).Println("ready")
		}

	nextDatabase:
	}

	return nil
}

func (s *deployState) waitForDatabase(dbID string) error {
	maxAttempts := 30
	for i := 0; i < maxAttempts; i++ {
		db, err := s.client.GetDatabase(dbID)
		if err != nil {
			return err
		}

		if db.Status == "ready" {
			return nil
		}

		if db.Status == "failed" {
			return fmt.Errorf("database creation failed: %s", db.ErrorMessage)
		}

		time.Sleep(2 * time.Second)
	}

	return fmt.Errorf("timeout waiting for database to be ready")
}

func (s *deployState) createServices() error {
	for _, svcDef := range s.blueprint.Services {
		fmt.Printf("  Creating service '%s'... ", svcDef.Name)

		if s.dryRun {
			color.New(color.FgYellow).Println("[would create]")
			continue
		}

		// Detect stack if auto
		svcPath := filepath.Join(s.baseDir, svcDef.Path)
		runtime := svcDef.Runtime
		port := svcDef.Port
		detectedStack := detect.StackUnknown

		if runtime == "auto" || runtime == "" {
			detected := detect.DetectStack(svcPath)
			if detected != nil && detected.Stack != detect.StackUnknown {
				detectedStack = detected.Stack
				runtime = stackToRuntime(detected.Stack)
				if port == 0 {
					port = detected.Port
				}
			}
		} else if runtime == "static" {
			detectedStack = detect.StackStatic
		}

		if port == 0 {
			// For `runtime: static`, the auto-generated Dockerfile uses
			// nginx:alpine which listens on port 80. Defaulting to 8080
			// here used to ship apps with a port mismatch that broke
			// every deploy with "connection reset by peer" (post-mortem
			// 2026-05-13, GFC-RH).
			if runtime == "static" {
				port = 80
			} else {
				port = 8080
			}
		} else if runtime == "static" && port != 80 && !hasCustomDockerfile(svcDef, filepath.Join(s.baseDir, svcDef.Path)) {
			// User explicitly set a non-80 port for a static SPA but isn't
			// providing a custom Dockerfile — the autogenerated nginx
			// config listens on 80, so this will deploy then fail
			// healthcheck. Warn loudly. The server-side normalizer also
			// catches this, but a CLI-side heads-up makes the cause
			// obvious before the user spends a build cycle.
			color.New(color.FgYellow).Printf(
				"  ⚠  '%s' is runtime: static with port: %d but no custom Dockerfile.\n",
				svcDef.Name, port,
			)
			color.New(color.FgYellow).Println(
				"      The autogenerated nginx config listens on port 80 — your config")
			color.New(color.FgYellow).Println(
				"      will be reset to 80. Add a `Dockerfile` if you need a different port.")
			port = 80
		}

		// nginx hardening check — fires when the build context has a
		// custom Dockerfile that uses nginx but the colocated nginx
		// config doesn't include scanner-path protection. Catches the
		// booky-web pattern (/.env, /.git/config etc. returning 200
		// via the SPA fallback). Warning only — we don't block the
		// deploy because the user might legitimately want the fallback
		// behaviour, or have their config in an unusual location.
		for _, warning := range nginxHardeningWarnings(svcPath) {
			color.New(color.FgYellow).Printf("  ⚠  %s: %s\n", svcDef.Name, warning)
		}

		serverID := svcDef.Server
		if serverID == "" {
			serverID = s.serverID
		}

		// Determine health check path
		healthCheckPath := defaultHealthCheckPath(runtime, detectedStack, svcPath)
		if svcDef.HealthCheck != nil && svcDef.HealthCheck.Path != "" {
			healthCheckPath = svcDef.HealthCheck.Path
		}

		// ASP.NET templates don't expose /health by default — the Traefik probe
		// will fail the first deploy with a 404 unless the app adds the route.
		//
		// Previously this was a warning the user could miss; now we grep the
		// .NET source for a /health route (MapGet / MapHealthChecks /
		// UseHealthChecks / [Route]) and fail `up` fast if absent. Saves
		// the user a ~2-minute build+healthcheck cycle that was always
		// going to red. They can override with an explicit
		// `healthCheck.path` in the blueprint if their app uses a
		// different probe path.
		if runtime == "dotnet" && healthCheckPath == "/health" &&
			(svcDef.HealthCheck == nil || svcDef.HealthCheck.Path == "") {
			hasHealth, hint := detect.HasDotNetHealthEndpoint(svcPath)
			if !hasHealth {
				color.New(color.FgRed, color.Bold).Printf(
					"\n  ✗ '%s' is a .NET service and has no /health route in its source\n\n",
					svcDef.Name)
				color.New(color.FgWhite).Println(indentLines(hint, "    "))
				fmt.Println()
				color.New(color.FgWhite).Println(
					"    Or override the probe path in deployado.yml:")
				color.New(color.FgWhite).Println("        services:")
				color.New(color.FgWhite).Printf("          - name: %s\n", svcDef.Name)
				color.New(color.FgWhite).Println("            healthCheck:")
				color.New(color.FgWhite).Println("              path: /api/your-existing-route")
				fmt.Println()
				return fmt.Errorf("'%s': missing /health route", svcDef.Name)
			}
		}

		// Generate app name: combine blueprint name with service name
		// to avoid generic names like "website.deployado.com"
		appName := s.generateAppName(svcDef.Name)

		params := map[string]interface{}{
			"name":               appName,
			"project_id":         s.projectID,
			"port":               port,
			"pre_deploy_command": svcDef.PreDeployCommand,
			"health_check_path":  healthCheckPath,
		}

		// Add runtime if detected
		if runtime != "" && runtime != "auto" {
			params["runtime"] = runtime
		}

		// Add repository URL and branch from blueprint
		if s.blueprint.Repository != "" {
			params["repository_url"] = s.blueprint.Repository
		}
		if s.blueprint.Branch != "" {
			params["branch"] = s.blueprint.Branch
		}

		watchPaths := watchPathsForServicePath(svcDef.Path)
		params["watch_paths"] = watchPaths

		// Only include type if it's not the default "web"
		if svcDef.Type != "" && svcDef.Type != "web" {
			params["type"] = svcDef.Type
		}

		if serverID != "" {
			params["server_id"] = serverID
		}

		if svcDef.Build != nil {
			if svcDef.Build.Dockerfile != "" {
				params["dockerfile_path"] = svcDef.Build.Dockerfile
			}
			if svcDef.Build.Context != "" {
				params["build_context"] = svcDef.Build.Context
			}
		}

		// For services with a Path (subdirectory), use it as build_context if not already set
		// This is especially important for static sites where the content is in a subdirectory
		if svcDef.Path != "" && svcDef.Path != "." {
			if _, hasBuildContext := params["build_context"]; !hasBuildContext {
				params["build_context"] = svcDef.Path
			}
		}

		app, err := s.client.CreateApp(params)
		if err != nil {
			// Check if app already exists (update instead)
			if strings.Contains(err.Error(), "already exists") || strings.Contains(err.Error(), "slug") {
				fmt.Printf("exists, updating... ")
				// Try to find and update
				apps, _ := s.client.ListApps()
				for _, existingApp := range apps {
					// Match by generated app name or its slug
					if existingApp.Name == appName || existingApp.Slug == slugify(appName) {
						// Update the existing app with the new params (especially runtime)
						updateParams := map[string]interface{}{}
						if runtime != "" && runtime != "auto" {
							updateParams["runtime"] = runtime
						}
						if healthCheckPath != "" {
							updateParams["health_check_path"] = healthCheckPath
						}
						if svcDef.PreDeployCommand != "" {
							updateParams["pre_deploy_command"] = svcDef.PreDeployCommand
						}
						// Update build_context from Path if set
						if svcDef.Path != "" && svcDef.Path != "." {
							updateParams["build_context"] = svcDef.Path
						}
						updateParams["watch_paths"] = watchPaths
						if len(updateParams) > 0 {
							updatedApp, updateErr := s.client.UpdateApp(existingApp.ID, updateParams)
							if updateErr == nil {
								app = updatedApp
							} else {
								app = &existingApp
							}
						} else {
							app = &existingApp
						}
						s.services[svcDef.Name] = app
						color.New(color.FgGreen).Println("updated")
						goto nextService
					}
				}
			}

			color.New(color.FgRed).Printf("failed: %s\n", err)
			return fmt.Errorf("failed to create service '%s': %w", svcDef.Name, err)
		}

		s.services[svcDef.Name] = app
		color.New(color.FgGreen).Println("done")

	nextService:
	}

	return nil
}

func (s *deployState) configureEnvVars() error {
	for _, svcDef := range s.blueprint.Services {
		app := s.services[svcDef.Name]
		if app == nil && !s.dryRun {
			continue
		}

		envVars := make(map[string]string)

		// Process env vars
		for _, env := range svcDef.EnvVars {
			value, err := s.resolveEnvVar(svcDef.Name, &env)
			if err != nil {
				return fmt.Errorf("failed to resolve env var '%s' for service '%s': %w", env.Key, svcDef.Name, err)
			}
			if value != "" {
				envVars[env.Key] = value
			}
		}

		// Process links (generate API URLs)
		for _, link := range svcDef.Links {
			linkedApp := s.services[link.Service]
			if linkedApp != nil {
				envKey := link.EnvVar
				if envKey == "" {
					envKey = fmt.Sprintf("%s_URL", strings.ToUpper(strings.ReplaceAll(link.Service, "-", "_")))
				}
				envVars[envKey] = fmt.Sprintf("https://%s.deployado.com", linkedApp.Slug)
			}
		}

		// Auto-inject `ConnectionStrings__DefaultConnection` for .NET
		// services that have a database reference but didn't declare the
		// connection string env var explicitly. The most common case —
		// .NET app + linked DB — should be zero-config.
		if injected := s.maybeAutoInjectDotNetConnectionString(&svcDef, envVars); injected != "" {
			color.New(color.FgCyan).Printf(
				"  ✓ Auto-injected ConnectionStrings__DefaultConnection for '%s' (from database '%s')\n",
				svcDef.Name, injected,
			)
		}

		if len(envVars) == 0 {
			continue
		}

		s.envVars[svcDef.Name] = envVars

		fmt.Printf("  Configuring %d env vars for '%s'... ", len(envVars), svcDef.Name)

		if s.dryRun {
			color.New(color.FgYellow).Println("[would configure]")
			for k, v := range envVars {
				masked := v
				if len(masked) > 20 {
					masked = masked[:10] + "..." + masked[len(masked)-5:]
				}
				fmt.Printf("    %s=%s\n", k, masked)
			}
			continue
		}

		runtimeCount, buildCount, err := setEnvWithAutoScope(s.client, app.ID, envVars)
		if err != nil {
			color.New(color.FgRed).Printf("failed: %s\n", err)
			return fmt.Errorf("failed to set env vars for '%s': %w", svcDef.Name, err)
		}

		color.New(color.FgGreen).Println("done")
		if buildCount > 0 {
			fmt.Printf("    Scope split: %d runtime, %d build\n", runtimeCount, buildCount)
		}
	}

	return nil
}

func (s *deployState) resolveEnvVar(svcName string, env *blueprint.EnvVar) (string, error) {
	// Static value
	if env.Value != "" {
		return env.Value, nil
	}

	// Generated secret
	if env.GenerateSecret {
		return generateSecret(32), nil
	}

	// Prompted value
	if env.Prompt {
		key := fmt.Sprintf("%s.%s", svcName, env.Key)
		if val, ok := s.promptedVars[key]; ok {
			return val, nil
		}
		return "", nil
	}

	// Database reference
	if env.FromDatabase != nil {
		db := s.databases[env.FromDatabase.Name]
		if db == nil {
			// Database might have been created in a previous run
			// Try to find it
			if !s.dryRun {
				dbs, _ := s.client.ListDatabases("")
				for i := range dbs {
					if dbs[i].Name == env.FromDatabase.Name {
						db = &dbs[i]
						s.databases[env.FromDatabase.Name] = db
						break
					}
				}
			}
		}

		if db == nil {
			if s.dryRun {
				return fmt.Sprintf("${%s.%s}", env.FromDatabase.Name, env.FromDatabase.Property), nil
			}
			return "", fmt.Errorf("database '%s' not found", env.FromDatabase.Name)
		}

		return getDatabaseProperty(db, env.FromDatabase.Property, env.FromDatabase.Driver), nil
	}

	// Service reference
	if env.FromService != nil {
		app := s.services[env.FromService.Name]
		if app == nil {
			if s.dryRun {
				return fmt.Sprintf("${%s.%s}", env.FromService.Name, env.FromService.Property), nil
			}
			return "", fmt.Errorf("service '%s' not found", env.FromService.Name)
		}

		return getServiceProperty(app, env.FromService.Property), nil
	}

	return "", nil
}

func getDatabaseProperty(db *api.Database, property, driver string) string {
	switch property {
	case "connectionString", "url":
		return buildConnectionString(db, driver)
	case "host":
		return db.DBHost
	case "port":
		return fmt.Sprintf("%d", db.DBPort)
	case "user":
		return db.DBUser
	case "database", "name":
		return db.DBName
	default:
		return ""
	}
}

// buildConnectionString formats a database connection string for the requested
// driver. The platform stores credentials in db.ConnectionURL (postgresql://
// format with the password embedded); we re-format for drivers that need a
// different shape — most notably .NET's Npgsql which uses ADO.NET key=value.
func buildConnectionString(db *api.Database, driver string) string {
	password := extractPasswordFromConnectionURL(db.ConnectionURL)

	switch driver {
	case "dotnet":
		// ADO.NET / Npgsql key=value format (consumed by .NET's
		// IConfiguration.GetConnectionString and similar)
		return fmt.Sprintf("Host=%s;Port=%d;Database=%s;Username=%s;Password=%s",
			db.DBHost, db.DBPort, db.DBName, db.DBUser, password)
	case "ecto":
		return buildURLConnectionString("ecto://", db, password)
	case "pg":
		return buildURLConnectionString("postgres://", db, password)
	default:
		// Prefer the platform-provided URL so we get the password as stored.
		if db.ConnectionURL != "" {
			return db.ConnectionURL
		}
		return buildURLConnectionString("postgresql://", db, password)
	}
}

func buildURLConnectionString(prefix string, db *api.Database, password string) string {
	user := db.DBUser
	if password != "" {
		user = fmt.Sprintf("%s:%s", db.DBUser, password)
	}
	return fmt.Sprintf("%s%s@%s:%d/%s", prefix, user, db.DBHost, db.DBPort, db.DBName)
}

// extractPasswordFromConnectionURL parses a URL-shaped connection string and
// returns the embedded password. Returns "" if the URL is missing, malformed,
// or has no password component.
func extractPasswordFromConnectionURL(connectionURL string) string {
	if connectionURL == "" {
		return ""
	}
	u, err := url.Parse(connectionURL)
	if err != nil || u.User == nil {
		return ""
	}
	pw, _ := u.User.Password()
	return pw
}

func getServiceProperty(app *api.App, property string) string {
	switch property {
	case "url":
		return fmt.Sprintf("https://%s.deployado.com", app.Slug)
	case "host":
		return fmt.Sprintf("%s.deployado.com", app.Slug)
	case "port":
		return "443"
	default:
		return ""
	}
}

// syncDatabaseConnections creates (or updates) managed
// database_connection records for each `fromDatabase` reference in
// the blueprint. The endpoint is idempotent — re-running `up` reconciles
// the connection's env_var_name and driver with the current blueprint.
//
// This used to be missing: blueprints declared the env var via
// `fromDatabase`, the value got injected at create time, but the UI's
// "Connected Apps" section showed nothing because no
// `database_connections` row was created. That left users confused about
// why an app that clearly uses a DB doesn't appear "connected".
//
// We dedupe by (app, db) — if the blueprint has multiple env vars for
// the same (app, db) pair, only the first one becomes the managed
// connection. The others stay as regular env vars set by the user.
func (s *deployState) syncDatabaseConnections() error {
	type pair struct {
		appName    string
		dbName     string
		envVarKey  string
		driver     string
	}

	// Build the list of pairs to sync, deduped.
	var pairs []pair
	seen := map[string]bool{}

	for _, svcDef := range s.blueprint.Services {
		for _, env := range svcDef.EnvVars {
			if env.FromDatabase == nil {
				continue
			}
			key := svcDef.Name + "|" + env.FromDatabase.Name
			if seen[key] {
				continue
			}
			seen[key] = true

			driver := env.FromDatabase.Driver
			if driver == "" {
				driver = "auto"
			}

			pairs = append(pairs, pair{
				appName:   svcDef.Name,
				dbName:    env.FromDatabase.Name,
				envVarKey: env.Key,
				driver:    driver,
			})
		}
	}

	if len(pairs) == 0 {
		return nil
	}

	fmt.Println()
	color.New(color.FgCyan, color.Bold).Println("Step 3.5: Syncing managed database connections...")

	for _, p := range pairs {
		app := s.services[p.appName]
		db := s.databases[p.dbName]

		if app == nil || db == nil {
			if s.dryRun {
				color.New(color.FgYellow).Printf(
					"  [dry-run] would link '%s' to '%s' (env var: %s, driver: %s)\n",
					p.appName, p.dbName, p.envVarKey, p.driver,
				)
				continue
			}
			color.New(color.FgYellow).Printf(
				"  Skipping link for '%s' → '%s' (app or db not found in state)\n",
				p.appName, p.dbName,
			)
			continue
		}

		if s.dryRun {
			color.New(color.FgYellow).Printf(
				"  [dry-run] would link '%s' to '%s' (env var: %s, driver: %s)\n",
				p.appName, p.dbName, p.envVarKey, p.driver,
			)
			continue
		}

		params := map[string]interface{}{
			"app_id":       app.ID,
			"env_var_name": p.envVarKey,
			"driver":       p.driver,
		}

		_, err := s.client.ConnectDatabase(db.ID, params)
		if err != nil {
			// Don't fail the whole deploy for a cosmetic UI fix — log
			// and continue. The env var is already set so the app
			// works regardless.
			color.New(color.FgYellow).Printf(
				"  ⚠  Could not link '%s' to '%s': %s\n",
				p.appName, p.dbName, err,
			)
			continue
		}

		color.New(color.FgGreen).Printf(
			"  ✓ Linked '%s' to '%s' (env var: %s)\n",
			p.appName, p.dbName, p.envVarKey,
		)
	}

	return nil
}

func (s *deployState) deployServices() error {
	for _, svcDef := range s.blueprint.Services {
		app := s.services[svcDef.Name]
		if app == nil && !s.dryRun {
			continue
		}

		fmt.Printf("  Deploying '%s'... ", svcDef.Name)

		if s.dryRun {
			color.New(color.FgYellow).Println("[would deploy]")
			continue
		}

		_, err := s.client.DeployApp(app.ID, nil)
		if err != nil {
			color.New(color.FgYellow).Printf("skipped: %s\n", err)
			continue
		}

		color.New(color.FgGreen).Println("started")
	}

	return nil
}

func printDeployedServices(s *deployState) {
	fmt.Println("Services:")
	for name, app := range s.services {
		url := fmt.Sprintf("https://%s.deployado.com", app.Slug)
		fmt.Printf("  %s: %s\n", name, color.CyanString(url))
	}

	if len(s.databases) > 0 {
		fmt.Println("\nDatabases:")
		for name, db := range s.databases {
			fmt.Printf("  %s: %s@%s:%d/%s\n", name, db.DBUser, db.DBHost, db.DBPort, db.DBName)
		}
	}
}

func generateSecret(length int) string {
	// For Phoenix SECRET_KEY_BASE, we need at least 64 bytes
	// Generate enough random bytes to produce the desired length after base64 encoding
	if length < 64 {
		length = 64
	}
	bytes := make([]byte, length)
	rand.Read(bytes)
	encoded := base64.URLEncoding.EncodeToString(bytes)
	if len(encoded) > length {
		return encoded[:length]
	}
	return encoded
}

func stackToRuntime(stack detect.Stack) string {
	switch stack {
	case detect.StackNode:
		return "node"
	case detect.StackPython, detect.StackDjango, detect.StackFastAPI, detect.StackFlask:
		return "python"
	case detect.StackPhoenix, detect.StackElixir:
		return "elixir"
	case detect.StackDotNet:
		return "dotnet"
	case detect.StackStatic:
		return "static"
	case detect.StackRails, detect.StackRuby:
		return "ruby"
	default:
		return "docker"
	}
}

func slugify(name string) string {
	name = strings.ToLower(name)
	name = strings.ReplaceAll(name, " ", "-")
	name = strings.ReplaceAll(name, "_", "-")
	return name
}

func watchPathsForServicePath(servicePath string) []string {
	if servicePath == "" || servicePath == "." || servicePath == "./" {
		return []string{}
	}

	normalized := filepath.ToSlash(strings.TrimSpace(servicePath))
	normalized = strings.TrimPrefix(normalized, "./")
	normalized = strings.Trim(normalized, "/")
	if normalized == "" {
		return []string{}
	}

	return []string{normalized + "/**"}
}

// maybeAutoInjectDotNetConnectionString fills in the
// `ConnectionStrings__DefaultConnection` env var for .NET services
// that have a `fromDatabase` reference but didn't declare the
// connection string explicitly.
//
// Returns the source database name when an injection happened,
// "" otherwise. The injected value uses the database's properly-formatted
// ADO.NET connection string (driver: dotnet) so the user doesn't have
// to repeat that detail in the blueprint.
//
// Guarded by:
//   - runtime is `dotnet` (explicit OR auto-detected)
//   - the user has NOT already set `ConnectionStrings__DefaultConnection`
//   - exactly one fromDatabase reference exists in the service's env
//     vars (multiple DBs require explicit per-key control — we don't
//     want to guess which one is the "default" connection)
//
// Mutates envVars in place.
func (s *deployState) maybeAutoInjectDotNetConnectionString(
	svcDef *blueprint.Service, envVars map[string]string,
) string {
	const targetKey = "ConnectionStrings__DefaultConnection"

	// 1. Runtime must be .NET
	if !isDotNetRuntime(svcDef.Runtime, svcDef.Path, s.baseDir) {
		return ""
	}

	// 2. User shouldn't have already set it
	if _, exists := envVars[targetKey]; exists {
		return ""
	}

	// 3. Find the (single) fromDatabase reference
	var dbName string
	dbRefCount := 0
	for _, env := range svcDef.EnvVars {
		if env.FromDatabase != nil {
			dbRefCount++
			dbName = env.FromDatabase.Name
		}
	}
	if dbRefCount != 1 {
		return ""
	}

	// 4. Resolve the database (must be ready / loadable)
	db := s.databases[dbName]
	if db == nil {
		if s.dryRun {
			envVars[targetKey] = fmt.Sprintf("${%s.connectionString}", dbName)
			return dbName
		}
		dbs, _ := s.client.ListDatabases("")
		for i := range dbs {
			if dbs[i].Name == dbName {
				db = &dbs[i]
				s.databases[dbName] = db
				break
			}
		}
	}
	if db == nil {
		return ""
	}

	envVars[targetKey] = getDatabaseProperty(db, "connectionString", "dotnet")
	return dbName
}

// hasCustomDockerfile returns true if the service's build context
// contains a Dockerfile checked into the source tree. We only consider
// the standard name (`Dockerfile`) at the root of the build context —
// matches what the agent's static-Dockerfile generator already does
// before falling back to autogeneration.
func hasCustomDockerfile(svc blueprint.Service, svcAbsPath string) bool {
	// Honour explicit dockerfile_path if set (rare for static apps,
	// but legal). Absence of the field falls through to the default
	// `Dockerfile` check.
	candidate := "Dockerfile"
	info, err := os.Stat(filepath.Join(svcAbsPath, candidate))
	return err == nil && !info.IsDir()
}

// isDotNetRuntime returns true when the service is a .NET app — either
// declared explicitly or detected from the source tree (so blueprints
// using `runtime: auto` get the benefit too).
func isDotNetRuntime(declared, svcPath, baseDir string) bool {
	if declared == "dotnet" {
		return true
	}
	if declared != "" && declared != "auto" {
		return false
	}
	full := filepath.Join(baseDir, svcPath)
	detected := detect.DetectStack(full)
	return detected != nil && detected.Stack == detect.StackDotNet
}

// indentLines prefixes every line in s with prefix. Used to align
// multi-line code snippets under a header in CLI output.
func indentLines(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = prefix + line
		}
	}
	return strings.Join(lines, "\n")
}

// generateAppName creates a unique app name by combining blueprint name with service name.
// This avoids generic subdomain names like "website.deployado.com" or "api.deployado.com".
// Rules:
//   - If there's only one service AND the service name equals the blueprint name, use the blueprint name
//   - If the service name is generic (website, app, api, web, backend, frontend, server),
//     use "{blueprint-name}-{service-name}" or just "{blueprint-name}" if single service
//   - Otherwise, use "{blueprint-name}-{service-name}" to ensure uniqueness
func (s *deployState) generateAppName(serviceName string) string {
	blueprintName := s.blueprint.Name
	if blueprintName == "" {
		// Fallback to service name if no blueprint name
		return serviceName
	}

	// Generic service names that should be combined with blueprint name
	genericNames := map[string]bool{
		"website":  true,
		"web":      true,
		"app":      true,
		"api":      true,
		"backend":  true,
		"frontend": true,
		"server":   true,
		"service":  true,
		"main":     true,
	}

	// If there's only one service
	if len(s.blueprint.Services) == 1 {
		// If service name equals blueprint name, just use blueprint name
		if strings.EqualFold(serviceName, blueprintName) {
			return blueprintName
		}
		// If service name is generic, just use blueprint name
		if genericNames[strings.ToLower(serviceName)] {
			return blueprintName
		}
		// Otherwise combine them
		return fmt.Sprintf("%s-%s", blueprintName, serviceName)
	}

	// Multiple services: always combine blueprint name with service name
	// But if service name is very generic, the blueprint name gives context
	return fmt.Sprintf("%s-%s", blueprintName, serviceName)
}

// detectGitRepository tries to get the remote origin URL from a git repository
