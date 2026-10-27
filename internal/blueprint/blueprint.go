package blueprint

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Blueprint represents a deployado.yaml configuration file
type Blueprint struct {
	// Name of the project (optional, defaults to directory name)
	Name string `yaml:"name,omitempty"`

	// Repository URL (e.g., github.com/user/repo)
	Repository string `yaml:"repository,omitempty"`

	// Branch to deploy from (default: main)
	Branch string `yaml:"branch,omitempty"`

	// Project ID to deploy to (optional, can be set via CLI)
	Project string `yaml:"project,omitempty"`

	// Services defines the apps to deploy
	Services []Service `yaml:"services"`

	// Databases defines the PostgreSQL databases to create
	Databases []Database `yaml:"databases,omitempty"`

	// EnvGroups defines shared environment variable groups
	EnvGroups []EnvGroup `yaml:"envGroups,omitempty"`
}

// Service represents an app/service definition
type Service struct {
	// Name of the service (required)
	Name string `yaml:"name"`

	// Type of service: web, worker, cron (default: web)
	Type string `yaml:"type,omitempty"`

	// Path to the service source code relative to blueprint (default: ".")
	Path string `yaml:"path,omitempty"`

	// Runtime/stack: auto, node, python, go, elixir, dotnet, static, docker
	Runtime string `yaml:"runtime,omitempty"`

	// RuntimeVersion specifies the version of the runtime (e.g., "9.0" for .NET, "20" for Node.js)
	RuntimeVersion string `yaml:"runtimeVersion,omitempty"`

	// Port the service listens on (default: auto-detected or 8080)
	Port int `yaml:"port,omitempty"`

	// Build settings
	Build *BuildConfig `yaml:"build,omitempty"`

	// Environment variables
	EnvVars []EnvVar `yaml:"envVars,omitempty"`

	// Reference to environment groups
	EnvGroups []string `yaml:"envGroups,omitempty"`

	// Health check configuration
	HealthCheck *HealthCheck `yaml:"healthCheck,omitempty"`

	// Pre-deploy command (e.g., migrations)
	PreDeployCommand string `yaml:"preDeployCommand,omitempty"`

	// Server ID to deploy to (optional, uses default if not set)
	Server string `yaml:"server,omitempty"`

	// Availability tier: basic, ha, high_capacity
	AvailabilityTier string `yaml:"availabilityTier,omitempty"`

	// Links to other services (for API URL configuration)
	Links []ServiceLink `yaml:"links,omitempty"`
}

// BuildConfig defines build settings
type BuildConfig struct {
	// Dockerfile path relative to service path
	Dockerfile string `yaml:"dockerfile,omitempty"`

	// Build context relative to service path
	Context string `yaml:"context,omitempty"`

	// Build command (for non-Docker builds)
	Command string `yaml:"command,omitempty"`
}

// EnvVar represents an environment variable
type EnvVar struct {
	// Key name
	Key string `yaml:"key"`

	// Static value (mutually exclusive with other options)
	Value string `yaml:"value,omitempty"`

	// Generate a random secret value
	GenerateSecret bool `yaml:"generateSecret,omitempty"`

	// Reference to a database connection
	FromDatabase *DatabaseRef `yaml:"fromDatabase,omitempty"`

	// Reference to another service
	FromService *ServiceRef `yaml:"fromService,omitempty"`

	// Prompt user for value during deploy (for secrets)
	Prompt bool `yaml:"prompt,omitempty"`

	// Description for prompted values
	Description string `yaml:"description,omitempty"`
}

// DatabaseRef references a database property
type DatabaseRef struct {
	// Name of the database in this blueprint
	Name string `yaml:"name"`

	// Property to use: connectionString, host, port, user, password, database
	Property string `yaml:"property"`

	// Driver format: auto, psycopg2, asyncpg, ecto, pg, jdbc
	Driver string `yaml:"driver,omitempty"`
}

// ServiceRef references another service
type ServiceRef struct {
	// Name of the service in this blueprint
	Name string `yaml:"name"`

	// Property to use: url, host, port
	Property string `yaml:"property"`
}

// ServiceLink defines a link between services (frontend -> api)
type ServiceLink struct {
	// Name of the service to link to
	Service string `yaml:"service"`

	// Environment variable to set with the service URL
	EnvVar string `yaml:"envVar,omitempty"`
}

// Database represents a PostgreSQL database
type Database struct {
	// Name of the database (required)
	Name string `yaml:"name"`

	// PostgreSQL version: 14, 15, 16, 17, 18 (default: 16)
	Version string `yaml:"version,omitempty"`

	// Server ID to create database on (optional)
	Server string `yaml:"server,omitempty"`

	// Maximum connections (default: 100)
	MaxConnections int `yaml:"maxConnections,omitempty"`

	// Enable backups (default: true)
	BackupEnabled *bool `yaml:"backupEnabled,omitempty"`
}

// EnvGroup defines a reusable group of environment variables
type EnvGroup struct {
	// Name of the group
	Name string `yaml:"name"`

	// Environment variables in this group
	EnvVars []EnvVar `yaml:"envVars"`
}

// HealthCheck defines health check configuration
type HealthCheck struct {
	// Path to check (default: /health)
	Path string `yaml:"path,omitempty"`

	// Timeout in seconds (default: 10)
	Timeout int `yaml:"timeout,omitempty"`
}

// DefaultBlueprintNames are the filenames to look for
var DefaultBlueprintNames = []string{
	"deployado.yaml",
	"deployado.yml",
	".deployado.yaml",
	".deployado.yml",
}

// Load reads and parses a blueprint file
func Load(path string) (*Blueprint, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read blueprint: %w", err)
	}

	var bp Blueprint
	if err := yaml.Unmarshal(data, &bp); err != nil {
		return nil, fmt.Errorf("failed to parse blueprint: %w", err)
	}

	// Set defaults
	bp.setDefaults(filepath.Dir(path))

	// Validate
	if err := bp.Validate(); err != nil {
		return nil, err
	}

	return &bp, nil
}

// Find looks for a blueprint file in the given directory
func Find(dir string) (string, error) {
	for _, name := range DefaultBlueprintNames {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("no blueprint file found (looking for: %s)", strings.Join(DefaultBlueprintNames, ", "))
}

// LoadFromDir finds and loads a blueprint from a directory
func LoadFromDir(dir string) (*Blueprint, error) {
	path, err := Find(dir)
	if err != nil {
		return nil, err
	}
	return Load(path)
}

func (bp *Blueprint) setDefaults(baseDir string) {
	// Default name from directory
	if bp.Name == "" {
		bp.Name = filepath.Base(baseDir)
	}

	// Set service defaults
	for i := range bp.Services {
		svc := &bp.Services[i]

		if svc.Type == "" {
			svc.Type = "web"
		}

		if svc.Path == "" {
			svc.Path = "."
		}

		if svc.Runtime == "" {
			svc.Runtime = "auto"
		}

		if svc.AvailabilityTier == "" {
			svc.AvailabilityTier = "basic"
		}
	}

	// Set database defaults
	for i := range bp.Databases {
		db := &bp.Databases[i]

		if db.Version == "" {
			db.Version = "18"
		}

		if db.MaxConnections == 0 {
			db.MaxConnections = 100
		}

		if db.BackupEnabled == nil {
			enabled := true
			db.BackupEnabled = &enabled
		}
	}
}

// Validate checks the blueprint for errors
func (bp *Blueprint) Validate() error {
	if len(bp.Services) == 0 {
		return fmt.Errorf("blueprint must define at least one service")
	}

	// Check for duplicate service names
	serviceNames := make(map[string]bool)
	for _, svc := range bp.Services {
		if svc.Name == "" {
			return fmt.Errorf("all services must have a name")
		}
		if serviceNames[svc.Name] {
			return fmt.Errorf("duplicate service name: %s", svc.Name)
		}
		serviceNames[svc.Name] = true

		// Validate service type
		validTypes := map[string]bool{"web": true, "worker": true, "cron": true}
		if !validTypes[svc.Type] {
			return fmt.Errorf("invalid service type '%s' for service '%s'", svc.Type, svc.Name)
		}

		// Validate links reference valid services
		for _, link := range svc.Links {
			if !serviceNames[link.Service] && !bp.hasService(link.Service) {
				return fmt.Errorf("service '%s' links to unknown service '%s'", svc.Name, link.Service)
			}
		}
	}

	// Check for duplicate database names
	dbNames := make(map[string]bool)
	for _, db := range bp.Databases {
		if db.Name == "" {
			return fmt.Errorf("all databases must have a name")
		}
		if dbNames[db.Name] {
			return fmt.Errorf("duplicate database name: %s", db.Name)
		}
		dbNames[db.Name] = true
	}

	// Validate database references in env vars
	for _, svc := range bp.Services {
		for _, env := range svc.EnvVars {
			if env.FromDatabase != nil {
				if !dbNames[env.FromDatabase.Name] {
					return fmt.Errorf("service '%s' references unknown database '%s'", svc.Name, env.FromDatabase.Name)
				}
			}
			if env.FromService != nil {
				if !serviceNames[env.FromService.Name] {
					return fmt.Errorf("service '%s' references unknown service '%s'", svc.Name, env.FromService.Name)
				}
			}
		}
	}

	return nil
}

func (bp *Blueprint) hasService(name string) bool {
	for _, svc := range bp.Services {
		if svc.Name == name {
			return true
		}
	}
	return false
}

// GetService returns a service by name
func (bp *Blueprint) GetService(name string) *Service {
	for i := range bp.Services {
		if bp.Services[i].Name == name {
			return &bp.Services[i]
		}
	}
	return nil
}

// GetDatabase returns a database by name
func (bp *Blueprint) GetDatabase(name string) *Database {
	for i := range bp.Databases {
		if bp.Databases[i].Name == name {
			return &bp.Databases[i]
		}
	}
	return nil
}
