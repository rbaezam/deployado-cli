package detect

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// DotNetDetector detects .NET projects
type DotNetDetector struct{}

// DotNetProjectInfo contains detailed information about a .NET project
type DotNetProjectInfo struct {
	CsprojPath      string // Full path to .csproj file
	CsprojDir       string // Directory containing .csproj (relative to root)
	AppName         string // Project name (from .csproj filename)
	TargetFramework string // e.g., net9.0, net8.0
	SDKVersion      string // e.g., 9.0, 8.0
	IsWebAPI        bool   // True if it's a web API project
	HasSolution     bool   // True if .sln file exists
	SolutionPath    string // Path to .sln file if exists
}

func (d *DotNetDetector) Detect(dir string) (*StackInfo, bool) {
	projectInfo := DetectDotNetProject(dir)
	if projectInfo == nil {
		return nil, false
	}

	description := ".NET " + projectInfo.SDKVersion + " application"
	if projectInfo.IsWebAPI {
		description = ".NET " + projectInfo.SDKVersion + " Web API"
	}

	return &StackInfo{
		Stack:          StackDotNet,
		Name:           ".NET",
		Description:    description,
		AppName:        projectInfo.AppName,
		Port:           8080,
		RuntimeVersion: projectInfo.SDKVersion,
	}, true
}

// DetectDotNetProject returns detailed information about a .NET project
func DetectDotNetProject(dir string) *DotNetProjectInfo {
	// Look for .csproj files in root first
	csprojFiles, err := filepath.Glob(filepath.Join(dir, "*.csproj"))
	csprojDir := ""

	if err != nil || len(csprojFiles) == 0 {
		// Check in subdirectories (common pattern: src/MyApp/MyApp.csproj)
		patterns := []string{
			filepath.Join(dir, "*", "*.csproj"),
			filepath.Join(dir, "src", "*", "*.csproj"),
			filepath.Join(dir, "src", "*.csproj"),
		}

		for _, pattern := range patterns {
			matches, _ := filepath.Glob(pattern)
			if len(matches) > 0 {
				csprojFiles = matches
				break
			}
		}

		if len(csprojFiles) == 0 {
			return nil
		}
	}

	csprojPath := csprojFiles[0]

	// Calculate relative directory from root
	csprojDir, _ = filepath.Rel(dir, filepath.Dir(csprojPath))
	if csprojDir == "." {
		csprojDir = ""
	}

	// Get app name from csproj file
	appName := extractDotNetAppName(csprojPath)
	if appName == "" {
		appName = getBaseDirName(dir)
	}

	// Read csproj content for analysis
	data, _ := os.ReadFile(csprojPath)
	content := string(data)

	// Extract target framework
	targetFramework := extractTargetFramework(content)
	sdkVersion := GetDotNetSDKVersion(targetFramework)

	// Detect if it's a Web API project
	isWebAPI := strings.Contains(content, "Microsoft.NET.Sdk.Web") ||
		strings.Contains(content, "Microsoft.AspNetCore")

	// Check for solution file
	hasSolution := false
	solutionPath := ""
	slnFiles, _ := filepath.Glob(filepath.Join(dir, "*.sln"))
	if len(slnFiles) > 0 {
		hasSolution = true
		solutionPath, _ = filepath.Rel(dir, slnFiles[0])
	}

	return &DotNetProjectInfo{
		CsprojPath:      csprojPath,
		CsprojDir:       csprojDir,
		AppName:         appName,
		TargetFramework: targetFramework,
		SDKVersion:      sdkVersion,
		IsWebAPI:        isWebAPI,
		HasSolution:     hasSolution,
		SolutionPath:    solutionPath,
	}
}

func extractTargetFramework(content string) string {
	re := regexp.MustCompile(`<TargetFramework>([^<]+)</TargetFramework>`)
	matches := re.FindStringSubmatch(content)
	if len(matches) > 1 {
		return matches[1]
	}
	return "net8.0"
}

func extractDotNetAppName(csprojPath string) string {
	// Get the filename without extension
	base := filepath.Base(csprojPath)
	return strings.TrimSuffix(base, ".csproj")
}

// GetEntryPoint returns the DLL name for the .NET app
func GetDotNetEntryPoint(dir string) string {
	projectInfo := DetectDotNetProject(dir)
	if projectInfo != nil {
		return projectInfo.AppName + ".dll"
	}
	return "app.dll"
}

// GetDotNetTargetFramework extracts the target framework from csproj
func GetDotNetTargetFramework(dir string) string {
	projectInfo := DetectDotNetProject(dir)
	if projectInfo != nil {
		return projectInfo.TargetFramework
	}
	return "net8.0"
}

// GetDotNetProjectDir returns the subdirectory containing the .csproj file
// Returns empty string if .csproj is in root
func GetDotNetProjectDir(dir string) string {
	projectInfo := DetectDotNetProject(dir)
	if projectInfo != nil {
		return projectInfo.CsprojDir
	}
	return ""
}

// GetDotNetSDKVersion extracts the SDK version from target framework
// net8.0 -> 8.0, net9.0 -> 9.0, net10.0 -> 10.0
func GetDotNetSDKVersion(targetFramework string) string {
	// Extract version number from netX.Y format
	re := regexp.MustCompile(`net(\d+)\.(\d+)`)
	matches := re.FindStringSubmatch(targetFramework)
	if len(matches) > 2 {
		major := matches[1]
		minor := matches[2]
		return major + "." + minor
	}
	return "8.0" // Default to 8.0
}

// GetDotNetAppSettings extracts configuration from appsettings.json
func GetDotNetAppSettings(dir string) map[string]interface{} {
	settings := make(map[string]interface{})

	// Try to find appsettings.json
	appSettingsPath := filepath.Join(dir, "appsettings.json")
	if _, err := os.Stat(appSettingsPath); os.IsNotExist(err) {
		// Try in subdirectories
		matches, _ := filepath.Glob(filepath.Join(dir, "*", "appsettings.json"))
		if len(matches) > 0 {
			appSettingsPath = matches[0]
		} else {
			return settings
		}
	}

	data, err := os.ReadFile(appSettingsPath)
	if err != nil {
		return settings
	}

	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		return settings
	}

	return config
}

// ExtractDotNetEnvVars extracts environment variables from appsettings.json
func ExtractDotNetEnvVars(dir string) []EnvVar {
	envVars := []EnvVar{}
	settings := GetDotNetAppSettings(dir)

	// Extract ConnectionStrings
	if connStrings, ok := settings["ConnectionStrings"].(map[string]interface{}); ok {
		for key := range connStrings {
			envVarKey := "ConnectionStrings__" + key
			envVars = append(envVars, EnvVar{
				Key:         envVarKey,
				Value:       "",
				Description: "Connection string for " + key,
				Required:    true,
				Secret:      true,
			})
		}
	}

	// Extract common ASP.NET settings
	if _, ok := settings["Logging"]; ok {
		envVars = append(envVars, EnvVar{
			Key:         "Logging__LogLevel__Default",
			Value:       "Information",
			Description: "Default logging level",
			Required:    false,
			Secret:      false,
		})
	}

	// Extract JWT settings if present
	if jwtSettings, ok := settings["Jwt"].(map[string]interface{}); ok {
		if _, hasKey := jwtSettings["Key"]; hasKey {
			envVars = append(envVars, EnvVar{
				Key:         "Jwt__Key",
				Value:       "",
				Description: "JWT signing key",
				Required:    true,
				Secret:      true,
			})
		}
		if _, hasIssuer := jwtSettings["Issuer"]; hasIssuer {
			envVars = append(envVars, EnvVar{
				Key:         "Jwt__Issuer",
				Value:       "",
				Description: "JWT issuer",
				Required:    true,
				Secret:      false,
			})
		}
		if _, hasAudience := jwtSettings["Audience"]; hasAudience {
			envVars = append(envVars, EnvVar{
				Key:         "Jwt__Audience",
				Value:       "",
				Description: "JWT audience",
				Required:    true,
				Secret:      false,
			})
		}
	}

	// Extract CORS origins if present
	if corsSettings, ok := settings["Cors"].(map[string]interface{}); ok {
		if _, hasOrigins := corsSettings["AllowedOrigins"]; hasOrigins {
			envVars = append(envVars, EnvVar{
				Key:         "Cors__AllowedOrigins",
				Value:       "",
				Description: "Allowed CORS origins (comma-separated)",
				Required:    false,
				Secret:      false,
			})
		}
	}

	// Extract custom app settings
	if appSettings, ok := settings["AppSettings"].(map[string]interface{}); ok {
		for key, value := range appSettings {
			// Determine if it's a secret based on common patterns
			isSecret := strings.Contains(strings.ToLower(key), "secret") ||
				strings.Contains(strings.ToLower(key), "password") ||
				strings.Contains(strings.ToLower(key), "key") ||
				strings.Contains(strings.ToLower(key), "token")

			envVarKey := "AppSettings__" + key
			defaultValue := ""
			if !isSecret && value != nil {
				defaultValue = toString(value)
			}

			envVars = append(envVars, EnvVar{
				Key:         envVarKey,
				Value:       defaultValue,
				Description: "Application setting: " + key,
				Required:    false,
				Secret:      isSecret,
			})
		}
	}

	return envVars
}

// HasDotNetHealthEndpoint returns true if the .NET project source code
// declares a `/health` route. Recognises the common patterns:
//
//   - Minimal API: app.MapGet("/health", ...) / app.MapHealthChecks(...)
//   - Health Checks middleware: app.UseHealthChecks("/health")
//   - Controller attribute: [Route("health")] or [HttpGet("health")]
//
// We walk all .cs files under `dir` (capped depth to avoid escaping a
// monorepo). The match is conservative — false negatives are tolerable
// (we'll warn even if the user has an exotic registration), but false
// positives would defeat the whole point of the pre-flight check.
//
// Returns (true, "") when found, (false, hint) when not — hint is a
// short, actionable code snippet.
func HasDotNetHealthEndpoint(dir string) (bool, string) {
	// Walk up to 5 levels deep. .NET projects can be deeply nested
	// (src/Project/Controllers/...) but 5 is more than enough for any
	// realistic structure.
	const maxDepth = 5

	patterns := []*regexp.Regexp{
		// app.MapGet("/health"  or  app.MapGet(@"/health"  or  app.MapGet($"/health"
		// Word boundary at end so "/healthz" doesn't match — those apps
		// would override the probe path in the blueprint and never hit
		// this code path anyway.
		regexp.MustCompile(`(?i)\.MapGet\s*\(\s*[@$]?"\s*/?health"`),
		// app.MapHealthChecks("/health") — the most idiomatic in modern .NET
		regexp.MustCompile(`(?i)\.MapHealthChecks\s*\(`),
		// app.UseHealthChecks("/health")
		regexp.MustCompile(`(?i)\.UseHealthChecks\s*\(`),
		// [Route("health")] / [Route("/health")] / [HttpGet("health")] — controller-style
		regexp.MustCompile(`(?i)\[\s*(Route|HttpGet)\s*\(\s*"\s*/?health"`),
	}

	found := false

	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || found {
			return nil
		}

		// Skip common large directories that won't contain source.
		if d.IsDir() {
			name := d.Name()
			switch name {
			case "bin", "obj", "node_modules", ".git", "publish", "dist":
				return filepath.SkipDir
			}
			// Cap depth.
			rel, _ := filepath.Rel(dir, path)
			if strings.Count(rel, string(filepath.Separator)) > maxDepth {
				return filepath.SkipDir
			}
			return nil
		}

		if !strings.HasSuffix(d.Name(), ".cs") {
			return nil
		}

		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}

		for _, p := range patterns {
			if p.Match(data) {
				found = true
				return filepath.SkipAll
			}
		}

		return nil
	})

	if err != nil || !found {
		return false, dotNetHealthHint()
	}

	return true, ""
}

// dotNetHealthHint returns the snippet we print when no /health
// endpoint is found in a .NET project. Kept in one place so the
// wording stays consistent if we polish it later.
func dotNetHealthHint() string {
	return `Add a /health route to Program.cs before the deploy:

    app.MapGet("/health", () => Results.Ok("OK"));

If you keep UseHttpsRedirection, exclude /health from it (or
remove redirection — Traefik handles TLS):

    app.UseWhen(
        ctx => !ctx.Request.Path.StartsWithSegments("/health"),
        b => b.UseHttpsRedirection()
    );`
}

// Helper function to convert interface{} to string
func toString(v interface{}) string {
	switch val := v.(type) {
	case string:
		return val
	case int:
		return strconv.Itoa(val)
	case float64:
		return strconv.FormatFloat(val, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(val)
	default:
		return ""
	}
}
