package detect

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// NodeDetector detects Node.js projects
type NodeDetector struct{}

func (d *NodeDetector) Detect(dir string) (*StackInfo, bool) {
	packageJSON := filepath.Join(dir, "package.json")
	if !fileExists(packageJSON) {
		return nil, false
	}

	// Frontend frameworks (Vite/Vue/React/etc.) should be handled by StaticDetector.
	if IsStaticSite(dir) {
		return nil, false
	}

	appName, nodeVersion := d.extractPackageInfo(packageJSON)
	if appName == "" {
		appName = getBaseDirName(dir)
	}

	return &StackInfo{
		Stack:          StackNode,
		Name:           "Node.js",
		Description:    "Node.js application",
		AppName:        appName,
		Port:           3000,
		RuntimeVersion: nodeVersion,
	}, true
}

func (d *NodeDetector) extractAppName(packageJSON string) string {
	name, _ := d.extractPackageInfo(packageJSON)
	return name
}

func (d *NodeDetector) extractPackageInfo(packageJSON string) (string, string) {
	data, err := os.ReadFile(packageJSON)
	if err != nil {
		return "", ""
	}

	var pkg struct {
		Name    string `json:"name"`
		Engines struct {
			Node string `json:"node"`
		} `json:"engines"`
	}

	if err := json.Unmarshal(data, &pkg); err != nil {
		return "", ""
	}

	// Extract major version from engines.node (e.g., ">=18.0.0" -> "18", "20.x" -> "20")
	nodeVersion := extractNodeVersion(pkg.Engines.Node)

	return pkg.Name, nodeVersion
}

// extractNodeVersion extracts the major version from a node engine specification
func extractNodeVersion(engineSpec string) string {
	if engineSpec == "" {
		return "" // Let Nixpacks use default
	}

	// Remove common prefixes like >=, ^, ~, =
	spec := engineSpec
	for _, prefix := range []string{">=", "<=", "^", "~", ">", "<", "="} {
		if len(spec) > len(prefix) && spec[:len(prefix)] == prefix {
			spec = spec[len(prefix):]
			break
		}
	}

	// Extract just the major version number
	var major string
	for _, c := range spec {
		if c >= '0' && c <= '9' {
			major += string(c)
		} else if c == '.' || c == 'x' || c == '*' {
			break
		}
	}

	return major
}

// GetNodeBuildCommand returns the build command from package.json
func GetNodeBuildCommand(dir string) string {
	packageJSON := filepath.Join(dir, "package.json")
	data, err := os.ReadFile(packageJSON)
	if err != nil {
		return ""
	}

	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}

	if err := json.Unmarshal(data, &pkg); err != nil {
		return ""
	}

	// Check for common build scripts
	if _, ok := pkg.Scripts["build"]; ok {
		return "npm run build"
	}

	return ""
}

// GetNodeStartCommand returns the start command from package.json
func GetNodeStartCommand(dir string) string {
	packageJSON := filepath.Join(dir, "package.json")
	data, err := os.ReadFile(packageJSON)
	if err != nil {
		return "node index.js"
	}

	var pkg struct {
		Scripts map[string]string `json:"scripts"`
		Main    string            `json:"main"`
	}

	if err := json.Unmarshal(data, &pkg); err != nil {
		return "node index.js"
	}

	// Check for start script
	if _, ok := pkg.Scripts["start"]; ok {
		return "npm start"
	}

	// Use main field
	if pkg.Main != "" {
		return "node " + pkg.Main
	}

	return "node index.js"
}

// IsStaticSite checks if the Node.js project is a static site (React, Vue, etc)
func IsStaticSite(dir string) bool {
	packageJSON := filepath.Join(dir, "package.json")
	data, err := os.ReadFile(packageJSON)
	if err != nil {
		return false
	}

	var pkg struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}

	if err := json.Unmarshal(data, &pkg); err != nil {
		return false
	}

	// Check for static site frameworks
	staticFrameworks := []string{
		"react", "react-dom", "vue", "nuxt", "next",
		"@angular/core", "svelte", "gatsby", "astro", "vite",
	}

	allDeps := make(map[string]string)
	for k, v := range pkg.Dependencies {
		allDeps[k] = v
	}
	for k, v := range pkg.DevDependencies {
		allDeps[k] = v
	}

	for _, framework := range staticFrameworks {
		if _, ok := allDeps[framework]; ok {
			return true
		}
	}

	return false
}
