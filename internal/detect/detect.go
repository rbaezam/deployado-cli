package detect

import (
	"os"
	"path/filepath"
)

// Stack represents a detected project stack
type Stack string

const (
	StackUnknown Stack = "unknown"
	StackElixir  Stack = "elixir"
	StackPhoenix Stack = "phoenix"
	StackPython  Stack = "python"
	StackDjango  Stack = "django"
	StackFastAPI Stack = "fastapi"
	StackFlask   Stack = "flask"
	StackRails   Stack = "rails"
	StackRuby    Stack = "ruby"
	StackDotNet  Stack = "dotnet"
	StackNode    Stack = "node"
	StackStatic  Stack = "static"
	StackFlutter Stack = "flutter" // Not deployable as web service
)

// StackInfo contains information about a detected stack
type StackInfo struct {
	Stack          Stack
	Name           string
	Description    string
	AppName        string
	Port           int
	RuntimeVersion string // Version of the runtime (e.g., "9.0" for .NET, "20" for Node.js)
}

// Detector is the interface for stack detectors
type Detector interface {
	Detect(dir string) (*StackInfo, bool)
}

// DetectStack attempts to detect the project stack
func DetectStack(dir string) *StackInfo {
	detectors := []Detector{
		&FlutterDetector{}, // Check Flutter first to avoid false positives
		&ElixirDetector{},
		&PythonDetector{},
		&RubyDetector{},
		&DotNetDetector{},
		&NodeDetector{},
		&StaticDetector{},
	}

	for _, d := range detectors {
		if info, ok := d.Detect(dir); ok {
			return info
		}
	}

	return &StackInfo{
		Stack:       StackUnknown,
		Name:        "Unknown",
		Description: "Could not detect project stack",
	}
}

// Helper functions

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func fileContains(path, substr string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return containsString(string(data), substr)
}

func containsString(s, substr string) bool {
	return len(s) >= len(substr) &&
		(s == substr || len(s) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func getBaseDirName(dir string) string {
	return filepath.Base(dir)
}

// GetPreDeployCommand returns the recommended pre-deploy command for a stack.
// This is typically used for database migrations.
func GetPreDeployCommand(stack Stack) string {
	switch stack {
	case StackPhoenix, StackElixir:
		// Phoenix/Elixir: run Ecto migrations
		return "/app/bin/migrate"
	case StackDjango:
		// Django: run migrations
		return "python manage.py migrate --noinput"
	case StackRails:
		// Rails: run migrations
		return "bundle exec rails db:migrate"
	default:
		return ""
	}
}

// EnvVar represents an environment variable with metadata
type EnvVar struct {
	Key         string
	Value       string // Default or generated value
	Description string
	Required    bool
	Secret      bool // If true, value should be generated securely
}

// GetRequiredEnvVars returns the environment variables needed for a stack
func GetRequiredEnvVars(stack Stack, appName string) []EnvVar {
	// Generate default Deployado subdomain for the app
	deployadoHost := appName + ".deployado.com"

	switch stack {
	case StackPhoenix, StackElixir:
		return []EnvVar{
			{
				Key:         "PHX_HOST",
				Value:       deployadoHost,
				Description: "The host name for your Phoenix app",
				Required:    true,
				Secret:      false,
			},
			{
				Key:         "PHX_SERVER",
				Value:       "true",
				Description: "Enable the Phoenix server (required for production)",
				Required:    true,
				Secret:      false,
			},
			{
				Key:         "SECRET_KEY_BASE",
				Value:       "", // Will be generated
				Description: "Secret key for signing/encryption (auto-generated)",
				Required:    true,
				Secret:      true,
			},
			{
				Key:         "CHECK_ORIGIN",
				Value:       "https://" + deployadoHost,
				Description: "Allowed origins for WebSocket connections (LiveView)",
				Required:    false,
				Secret:      false,
			},
			{
				Key:         "POOL_SIZE",
				Value:       "10",
				Description: "Database connection pool size",
				Required:    false,
				Secret:      false,
			},
		}

	case StackDjango:
		return []EnvVar{
			{
				Key:         "DJANGO_SECRET_KEY",
				Value:       "", // Will be generated
				Description: "Django secret key (auto-generated)",
				Required:    true,
				Secret:      true,
			},
			{
				Key:         "DJANGO_ALLOWED_HOSTS",
				Value:       "*",
				Description: "Comma-separated list of allowed hosts",
				Required:    true,
				Secret:      false,
			},
			{
				Key:         "DJANGO_DEBUG",
				Value:       "False",
				Description: "Debug mode (should be False in production)",
				Required:    false,
				Secret:      false,
			},
		}

	case StackFastAPI, StackFlask:
		return []EnvVar{
			{
				Key:         "SECRET_KEY",
				Value:       "", // Will be generated
				Description: "Application secret key (auto-generated)",
				Required:    true,
				Secret:      true,
			},
		}

	case StackRails:
		return []EnvVar{
			{
				Key:         "RAILS_ENV",
				Value:       "production",
				Description: "Rails environment",
				Required:    true,
				Secret:      false,
			},
			{
				Key:         "SECRET_KEY_BASE",
				Value:       "", // Will be generated
				Description: "Rails secret key base (auto-generated)",
				Required:    true,
				Secret:      true,
			},
			{
				Key:         "RAILS_SERVE_STATIC_FILES",
				Value:       "true",
				Description: "Serve static files from Rails",
				Required:    false,
				Secret:      false,
			},
		}

	case StackNode:
		return []EnvVar{
			{
				Key:         "NODE_ENV",
				Value:       "production",
				Description: "Node environment",
				Required:    true,
				Secret:      false,
			},
		}

	case StackDotNet:
		// Start with base .NET env vars
		envVars := []EnvVar{
			{
				Key:         "ASPNETCORE_ENVIRONMENT",
				Value:       "Production",
				Description: ".NET environment",
				Required:    true,
				Secret:      false,
			},
			{
				Key:         "ASPNETCORE_URLS",
				Value:       "http://+:8080",
				Description: "URLs to listen on",
				Required:    true,
				Secret:      false,
			},
		}

		// Add CORS origin pointing to frontend if it looks like an API
		envVars = append(envVars, EnvVar{
			Key:         "Cors__AllowedOrigins",
			Value:       "https://" + appName + ".deployado.com",
			Description: "Allowed CORS origins for frontend",
			Required:    false,
			Secret:      false,
		})

		return envVars

	default:
		return []EnvVar{}
	}
}
