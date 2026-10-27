package detect

import (
	"os"
	"path/filepath"
	"regexp"
)

// PythonDetector detects Python projects
type PythonDetector struct{}

func (d *PythonDetector) Detect(dir string) (*StackInfo, bool) {
	// Check for requirements.txt or pyproject.toml
	hasRequirements := fileExists(filepath.Join(dir, "requirements.txt"))
	hasPyproject := fileExists(filepath.Join(dir, "pyproject.toml"))

	if !hasRequirements && !hasPyproject {
		return nil, false
	}

	appName := getBaseDirName(dir)

	// Detect specific frameworks
	if d.isDjango(dir) {
		return &StackInfo{
			Stack:       StackDjango,
			Name:        "Django",
			Description: "Python Django web framework",
			AppName:     appName,
			Port:        8000,
		}, true
	}

	if d.isFastAPI(dir) {
		return &StackInfo{
			Stack:       StackFastAPI,
			Name:        "FastAPI",
			Description: "Python FastAPI framework",
			AppName:     appName,
			Port:        8000,
		}, true
	}

	if d.isFlask(dir) {
		return &StackInfo{
			Stack:       StackFlask,
			Name:        "Flask",
			Description: "Python Flask framework",
			AppName:     appName,
			Port:        5000,
		}, true
	}

	return &StackInfo{
		Stack:       StackPython,
		Name:        "Python",
		Description: "Python application",
		AppName:     appName,
		Port:        8000,
	}, true
}

func (d *PythonDetector) isDjango(dir string) bool {
	// Check manage.py
	if fileExists(filepath.Join(dir, "manage.py")) {
		if fileContains(filepath.Join(dir, "manage.py"), "django") {
			return true
		}
	}

	// Check requirements for django
	if d.hasPackage(dir, "django") || d.hasPackage(dir, "Django") {
		return true
	}

	return false
}

func (d *PythonDetector) isFastAPI(dir string) bool {
	if d.hasPackage(dir, "fastapi") {
		return true
	}

	// Check for common FastAPI patterns in main.py
	mainFile := filepath.Join(dir, "main.py")
	if fileContains(mainFile, "from fastapi import") || fileContains(mainFile, "import fastapi") {
		return true
	}

	return false
}

func (d *PythonDetector) isFlask(dir string) bool {
	if d.hasPackage(dir, "flask") || d.hasPackage(dir, "Flask") {
		return true
	}

	// Check for Flask patterns in app.py or main.py
	for _, filename := range []string{"app.py", "main.py"} {
		file := filepath.Join(dir, filename)
		if fileContains(file, "from flask import") || fileContains(file, "import flask") {
			return true
		}
	}

	return false
}

func (d *PythonDetector) hasPackage(dir, pkg string) bool {
	// Check requirements.txt
	reqFile := filepath.Join(dir, "requirements.txt")
	if fileExists(reqFile) {
		data, _ := os.ReadFile(reqFile)
		re := regexp.MustCompile(`(?i)^` + regexp.QuoteMeta(pkg) + `[=<>~\s]?`)
		if re.Match(data) {
			return true
		}
		// Simple contains check as fallback
		if fileContains(reqFile, pkg) {
			return true
		}
	}

	// Check pyproject.toml
	pyprojectFile := filepath.Join(dir, "pyproject.toml")
	if fileContains(pyprojectFile, pkg) {
		return true
	}

	return false
}
