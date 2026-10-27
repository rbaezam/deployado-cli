package detect

import (
	"os"
	"path/filepath"
	"regexp"
)

// ElixirDetector detects Elixir/Phoenix projects
type ElixirDetector struct{}

func (d *ElixirDetector) Detect(dir string) (*StackInfo, bool) {
	mixFile := filepath.Join(dir, "mix.exs")
	if !fileExists(mixFile) {
		return nil, false
	}

	appName := extractElixirAppName(mixFile)
	if appName == "" {
		appName = getBaseDirName(dir)
	}

	// Check if it's Phoenix
	if d.isPhoenix(dir) {
		return &StackInfo{
			Stack:       StackPhoenix,
			Name:        "Phoenix",
			Description: "Elixir Phoenix web framework",
			AppName:     appName,
			Port:        4000,
		}, true
	}

	return &StackInfo{
		Stack:       StackElixir,
		Name:        "Elixir",
		Description: "Elixir application",
		AppName:     appName,
		Port:        4000,
	}, true
}

func (d *ElixirDetector) isPhoenix(dir string) bool {
	mixFile := filepath.Join(dir, "mix.exs")
	if fileContains(mixFile, ":phoenix") {
		return true
	}

	// Check for Phoenix directory structure
	if dirExists(filepath.Join(dir, "lib", "*_web")) {
		return true
	}

	// Check config directory for Phoenix
	configFile := filepath.Join(dir, "config", "config.exs")
	if fileContains(configFile, "Phoenix") {
		return true
	}

	return false
}

func extractElixirAppName(mixFile string) string {
	data, err := os.ReadFile(mixFile)
	if err != nil {
		return ""
	}

	// Look for: app: :app_name
	re := regexp.MustCompile(`app:\s*:(\w+)`)
	matches := re.FindSubmatch(data)
	if len(matches) > 1 {
		return string(matches[1])
	}

	return ""
}
