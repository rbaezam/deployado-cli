package detect

import (
	"path/filepath"
)

// RubyDetector detects Ruby/Rails projects
type RubyDetector struct{}

func (d *RubyDetector) Detect(dir string) (*StackInfo, bool) {
	gemfile := filepath.Join(dir, "Gemfile")
	if !fileExists(gemfile) {
		return nil, false
	}

	appName := getBaseDirName(dir)

	// Check if it's Rails
	if d.isRails(dir) {
		return &StackInfo{
			Stack:       StackRails,
			Name:        "Ruby on Rails",
			Description: "Ruby on Rails web application",
			AppName:     appName,
			Port:        3000,
		}, true
	}

	return &StackInfo{
		Stack:       StackRuby,
		Name:        "Ruby",
		Description: "Ruby application",
		AppName:     appName,
		Port:        3000,
	}, true
}

func (d *RubyDetector) isRails(dir string) bool {
	// Check for Rails gems in Gemfile
	gemfile := filepath.Join(dir, "Gemfile")
	if fileContains(gemfile, "rails") {
		return true
	}

	// Check for config/application.rb (Rails marker)
	if fileExists(filepath.Join(dir, "config", "application.rb")) {
		return true
	}

	// Check for bin/rails
	if fileExists(filepath.Join(dir, "bin", "rails")) {
		return true
	}

	return false
}
