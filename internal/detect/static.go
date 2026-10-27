package detect

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// StaticDetector detects static site projects
type StaticDetector struct{}

// StaticFramework represents the detected frontend framework
type StaticFramework string

const (
	FrameworkVue     StaticFramework = "vue"
	FrameworkReact   StaticFramework = "react"
	FrameworkNext    StaticFramework = "next"
	FrameworkNuxt    StaticFramework = "nuxt"
	FrameworkAngular StaticFramework = "angular"
	FrameworkSvelte  StaticFramework = "svelte"
	FrameworkAstro   StaticFramework = "astro"
	FrameworkVite    StaticFramework = "vite"
	FrameworkUnknown StaticFramework = "unknown"
)

// StaticSiteInfo contains detailed information about a static site project
type StaticSiteInfo struct {
	Framework      StaticFramework
	FrameworkName  string
	BuildDir       string
	PackageManager string
	HasTypeScript  bool
	EnvPrefix      string // e.g., VITE_ for Vite, REACT_APP_ for CRA
}

func (d *StaticDetector) Detect(dir string) (*StackInfo, bool) {
	// Check if it's a Node project that builds to static
	if fileExists(filepath.Join(dir, "package.json")) {
		if IsStaticSite(dir) {
			siteInfo := DetectStaticSite(dir)
			description := "Static site"
			if siteInfo != nil {
				description = siteInfo.FrameworkName + " application"
			}

			return &StackInfo{
				Stack:       StackStatic,
				Name:        "Static Site",
				Description: description,
				AppName:     getBaseDirName(dir),
				Port:        80,
			}, true
		}
	}

	// Check for plain HTML
	if fileExists(filepath.Join(dir, "index.html")) {
		return &StackInfo{
			Stack:       StackStatic,
			Name:        "Static Site",
			Description: "Plain HTML static site",
			AppName:     getBaseDirName(dir),
			Port:        80,
		}, true
	}

	return nil, false
}

// DetectStaticSite returns detailed information about a static site project
func DetectStaticSite(dir string) *StaticSiteInfo {
	packageJSON := filepath.Join(dir, "package.json")
	if !fileExists(packageJSON) {
		return nil
	}

	data, err := os.ReadFile(packageJSON)
	if err != nil {
		return nil
	}

	var pkg struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}

	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil
	}

	allDeps := make(map[string]string)
	for k, v := range pkg.Dependencies {
		allDeps[k] = v
	}
	for k, v := range pkg.DevDependencies {
		allDeps[k] = v
	}

	info := &StaticSiteInfo{
		Framework:      FrameworkUnknown,
		FrameworkName:  "Static Site",
		BuildDir:       "dist",
		PackageManager: GetPackageManager(dir),
		HasTypeScript:  fileExists(filepath.Join(dir, "tsconfig.json")),
		EnvPrefix:      "",
	}

	// Detect framework (order matters - more specific first)
	if _, ok := allDeps["nuxt"]; ok {
		info.Framework = FrameworkNuxt
		info.FrameworkName = "Nuxt.js"
		info.BuildDir = ".output/public"
		info.EnvPrefix = "NUXT_PUBLIC_"
	} else if _, ok := allDeps["next"]; ok {
		info.Framework = FrameworkNext
		info.FrameworkName = "Next.js"
		info.BuildDir = ".next"
		info.EnvPrefix = "NEXT_PUBLIC_"
	} else if _, ok := allDeps["vue"]; ok {
		info.Framework = FrameworkVue
		info.FrameworkName = "Vue.js"
		info.BuildDir = "dist"
		// Check if using Vite (modern Vue projects)
		if _, hasVite := allDeps["vite"]; hasVite {
			info.EnvPrefix = "VITE_"
		} else {
			info.EnvPrefix = "VUE_APP_"
		}
	} else if _, ok := allDeps["react"]; ok {
		info.Framework = FrameworkReact
		info.FrameworkName = "React"
		info.BuildDir = "build"
		// Check if using Vite or CRA
		if _, hasVite := allDeps["vite"]; hasVite {
			info.EnvPrefix = "VITE_"
			info.BuildDir = "dist"
		} else {
			info.EnvPrefix = "REACT_APP_"
		}
	} else if _, ok := allDeps["@angular/core"]; ok {
		info.Framework = FrameworkAngular
		info.FrameworkName = "Angular"
		info.BuildDir = "dist"
		info.EnvPrefix = "NG_"
	} else if _, ok := allDeps["svelte"]; ok {
		info.Framework = FrameworkSvelte
		info.FrameworkName = "Svelte"
		info.BuildDir = "build"
		info.EnvPrefix = "PUBLIC_"
	} else if _, ok := allDeps["astro"]; ok {
		info.Framework = FrameworkAstro
		info.FrameworkName = "Astro"
		info.BuildDir = "dist"
		info.EnvPrefix = "PUBLIC_"
	} else if _, ok := allDeps["vite"]; ok {
		info.Framework = FrameworkVite
		info.FrameworkName = "Vite"
		info.BuildDir = "dist"
		info.EnvPrefix = "VITE_"
	}

	return info
}

// ExtractStaticEnvVars extracts environment variables from .env files
func ExtractStaticEnvVars(dir string) []EnvVar {
	envVars := []EnvVar{}

	siteInfo := DetectStaticSite(dir)
	if siteInfo == nil {
		return envVars
	}

	// Look for .env.example or .env.local.example files
	envFiles := []string{
		".env.example",
		".env.local.example",
		".env.production.example",
		".env",
	}

	for _, envFile := range envFiles {
		envPath := filepath.Join(dir, envFile)
		if !fileExists(envPath) {
			continue
		}

		file, err := os.Open(envPath)
		if err != nil {
			continue
		}
		defer file.Close()

		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())

			// Skip comments and empty lines
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}

			// Parse KEY=value
			parts := strings.SplitN(line, "=", 2)
			if len(parts) != 2 {
				continue
			}

			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])

			// Remove quotes from value
			value = strings.Trim(value, "\"'")

			// Check if it's a public variable (prefixed correctly)
			isPublic := siteInfo.EnvPrefix != "" && strings.HasPrefix(key, siteInfo.EnvPrefix)

			// Determine if it's a secret
			keyLower := strings.ToLower(key)
			isSecret := strings.Contains(keyLower, "secret") ||
				strings.Contains(keyLower, "password") ||
				strings.Contains(keyLower, "key") ||
				strings.Contains(keyLower, "token") ||
				strings.Contains(keyLower, "api_key")

			// Only include public variables or API URLs
			if isPublic || strings.Contains(keyLower, "api") || strings.Contains(keyLower, "url") {
				envVars = append(envVars, EnvVar{
					Key:         key,
					Value:       value,
					Description: "Environment variable from " + envFile,
					Required:    isPublic,
					Secret:      isSecret,
				})
			}
		}
		break // Only process the first found env file
	}

	return envVars
}

// GetStaticBuildDir returns the expected build output directory
func GetStaticBuildDir(dir string) string {
	siteInfo := DetectStaticSite(dir)
	if siteInfo != nil {
		return siteInfo.BuildDir
	}

	// Common build directories as fallback
	buildDirs := []string{
		"dist",
		"build",
		"public",
		"out",
		".next",
		".output",
	}

	for _, bd := range buildDirs {
		if dirExists(filepath.Join(dir, bd)) {
			return bd
		}
	}

	// Default to dist
	return "dist"
}

// GetPackageManager returns the package manager to use
func GetPackageManager(dir string) string {
	if fileExists(filepath.Join(dir, "pnpm-lock.yaml")) {
		return "pnpm"
	}
	if fileExists(filepath.Join(dir, "yarn.lock")) {
		return "yarn"
	}
	if fileExists(filepath.Join(dir, "bun.lockb")) {
		return "bun"
	}
	return "npm"
}
