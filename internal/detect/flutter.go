package detect

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// FlutterDetector detects Flutter projects
type FlutterDetector struct{}

func (d *FlutterDetector) Detect(dir string) (*StackInfo, bool) {
	pubspecPath := filepath.Join(dir, "pubspec.yaml")
	if !fileExists(pubspecPath) {
		return nil, false
	}

	// Read pubspec.yaml to confirm it's Flutter
	data, err := os.ReadFile(pubspecPath)
	if err != nil {
		return nil, false
	}

	var pubspec struct {
		Name         string                 `yaml:"name"`
		Dependencies map[string]interface{} `yaml:"dependencies"`
	}

	if err := yaml.Unmarshal(data, &pubspec); err != nil {
		return nil, false
	}

	// Check if it has flutter SDK dependency
	if _, hasFlutter := pubspec.Dependencies["flutter"]; !hasFlutter {
		return nil, false
	}

	appName := pubspec.Name
	if appName == "" {
		appName = getBaseDirName(dir)
	}

	return &StackInfo{
		Stack:       StackFlutter,
		Name:        "Flutter",
		Description: "Flutter mobile/web application (not deployable as web service)",
		AppName:     appName,
		Port:        0, // Flutter apps are not web services
	}, true
}

// IsFlutterWebSubdir checks if a directory is a Flutter web build directory
// (specifically the 'web' or 'build/web' folder inside a Flutter project)
func IsFlutterWebSubdir(dir string) bool {
	dirName := filepath.Base(dir)

	// Only block the 'web' directory (Flutter's web platform folder)
	// Don't block other directories like 'website', 'www', etc.
	if dirName != "web" {
		return false
	}

	// Check if parent directory is a Flutter project
	parentDir := filepath.Dir(dir)
	pubspecPath := filepath.Join(parentDir, "pubspec.yaml")

	if !fileExists(pubspecPath) {
		return false
	}

	data, err := os.ReadFile(pubspecPath)
	if err != nil {
		return false
	}

	var pubspec struct {
		Dependencies map[string]interface{} `yaml:"dependencies"`
	}

	if err := yaml.Unmarshal(data, &pubspec); err != nil {
		return false
	}

	_, hasFlutter := pubspec.Dependencies["flutter"]
	return hasFlutter
}
