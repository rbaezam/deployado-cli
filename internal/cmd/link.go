package cmd

import (
	"fmt"
	"strings"

	"github.com/deployado/cli/internal/api"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var linkToCmd = &cobra.Command{
	Use:   "link [frontend-app-id] --to [api-app-id]",
	Short: "Link a frontend app to an API backend",
	Long: `Link a frontend app to an API backend.

This will automatically configure:
  - API URL environment variable on the frontend (e.g., VITE_API_URL)
  - CORS settings on the API to allow requests from the frontend

The frontend app will need to be redeployed after linking for changes to take effect.

Examples:
  # Link a Vue.js frontend to a .NET API
  deployado link my-frontend --to my-api

  # Link with custom environment variable name
  deployado link my-frontend --to my-api --env-key VITE_BACKEND_URL

  # Unlink apps
  deployado link my-frontend --to my-api --unlink`,
	Args: cobra.ExactArgs(1),
	RunE: runLink,
}

var (
	linkToAPI  string
	linkEnvKey string
	linkUnlink bool
)

func init() {
	linkToCmd.Flags().StringVar(&linkToAPI, "to", "", "API app ID to link to (required)")
	linkToCmd.Flags().StringVar(&linkEnvKey, "env-key", "", "Custom environment variable name for API URL (default: auto-detected based on framework)")
	linkToCmd.Flags().BoolVar(&linkUnlink, "unlink", false, "Remove the link between apps")

	linkToCmd.MarkFlagRequired("to")

	rootCmd.AddCommand(linkToCmd)
}

func runLink(cmd *cobra.Command, args []string) error {
	client, err := getClient()
	if err != nil {
		return err
	}

	frontendAppID := args[0]
	apiAppID := linkToAPI

	// Get both apps
	frontendApp, err := client.GetApp(frontendAppID)
	if err != nil {
		return fmt.Errorf("failed to get frontend app: %w", err)
	}

	apiApp, err := client.GetApp(apiAppID)
	if err != nil {
		return fmt.Errorf("failed to get API app: %w", err)
	}

	if linkUnlink {
		return unlinkApps(client, frontendApp, apiApp)
	}

	return linkApps(client, frontendApp, apiApp)
}

func linkApps(client *api.Client, frontendApp, apiApp *api.App) error {
	frontendSlug := frontendApp.Slug
	apiSlug := apiApp.Slug
	frontendID := frontendApp.ID
	apiID := apiApp.ID

	// Construct URLs
	apiURL := fmt.Sprintf("https://%s.deployado.com", apiSlug)
	frontendURL := fmt.Sprintf("https://%s.deployado.com", frontendSlug)

	// Determine the API URL env key
	envKey := linkEnvKey
	if envKey == "" {
		// Auto-detect based on common patterns
		existingEnv, _ := client.GetEnv(frontendID)
		envKey = detectApiUrlEnvKey(existingEnv)
	}

	fmt.Println("Linking apps...")
	fmt.Printf("  Frontend: %s (%s)\n", frontendSlug, frontendID)
	fmt.Printf("  API:      %s (%s)\n", apiSlug, apiID)
	fmt.Println()

	// Step 1: Set API URL on frontend
	frontendEnv := map[string]string{
		envKey: apiURL,
	}
	frontendScope := envScopeForKey(envKey)

	color.New(color.FgCyan).Printf("Setting %s=%s on frontend (%s scope)...\n", envKey, apiURL, frontendScope)
	if err := client.SetEnvWithScope(frontendID, frontendEnv, frontendScope); err != nil {
		return fmt.Errorf("failed to set API URL on frontend: %w", err)
	}

	// Step 2: Configure CORS on API
	apiEnv, _ := client.GetEnv(apiID)

	corsKey := detectCorsEnvKey(apiEnv)
	existingOrigins := apiEnv[corsKey]

	// Add frontend URL to CORS origins
	newOrigins := addCorsOrigin(existingOrigins, frontendURL)

	apiEnvUpdate := map[string]string{
		corsKey: newOrigins,
	}

	color.New(color.FgCyan).Printf("Setting %s=%s on API...\n", corsKey, newOrigins)
	if err := client.SetEnv(apiID, apiEnvUpdate); err != nil {
		return fmt.Errorf("failed to set CORS origins on API: %w", err)
	}

	fmt.Println()
	color.New(color.FgGreen, color.Bold).Println("Apps linked successfully!")
	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Printf("  1. Redeploy the frontend: deployado deploy %s\n", frontendID)
	fmt.Printf("  2. Redeploy the API:      deployado deploy %s\n", apiID)
	fmt.Println()
	fmt.Println("The frontend will now be able to make API requests to the backend.")

	return nil
}

func unlinkApps(client *api.Client, frontendApp, apiApp *api.App) error {
	frontendSlug := frontendApp.Slug
	apiSlug := apiApp.Slug
	frontendID := frontendApp.ID
	apiID := apiApp.ID

	frontendURL := fmt.Sprintf("https://%s.deployado.com", frontendSlug)

	fmt.Println("Unlinking apps...")
	fmt.Printf("  Frontend: %s (%s)\n", frontendSlug, frontendID)
	fmt.Printf("  API:      %s (%s)\n", apiSlug, apiID)
	fmt.Println()

	// Step 1: Remove API URL from frontend
	frontendEnv, _ := client.GetEnv(frontendID)
	envKey := detectApiUrlEnvKey(frontendEnv)

	if _, exists := frontendEnv[envKey]; exists {
		color.New(color.FgCyan).Printf("Removing %s from frontend...\n", envKey)
		if err := client.DeleteEnv(frontendID, envKey); err != nil {
			return fmt.Errorf("failed to remove API URL from frontend: %w", err)
		}
	}

	// Step 2: Remove frontend from CORS on API
	apiEnv, _ := client.GetEnv(apiID)
	corsKey := detectCorsEnvKey(apiEnv)
	existingOrigins := apiEnv[corsKey]

	if existingOrigins != "" {
		newOrigins := removeCorsOrigin(existingOrigins, frontendURL)
		if newOrigins != existingOrigins {
			color.New(color.FgCyan).Printf("Updating %s on API...\n", corsKey)
			if newOrigins == "" {
				if err := client.DeleteEnv(apiID, corsKey); err != nil {
					return fmt.Errorf("failed to update CORS on API: %w", err)
				}
			} else {
				if err := client.SetEnv(apiID, map[string]string{corsKey: newOrigins}); err != nil {
					return fmt.Errorf("failed to update CORS on API: %w", err)
				}
			}
		}
	}

	fmt.Println()
	color.New(color.FgGreen, color.Bold).Println("Apps unlinked successfully!")
	fmt.Println()
	fmt.Println("Remember to redeploy both apps for changes to take effect.")

	return nil
}

// detectApiUrlEnvKey tries to detect the appropriate env key for API URL
func detectApiUrlEnvKey(existingEnv map[string]string) string {
	// Check for existing API URL patterns
	patterns := []string{
		"VITE_API_URL",
		"VITE_BACKEND_URL",
		"REACT_APP_API_URL",
		"NEXT_PUBLIC_API_URL",
		"NUXT_PUBLIC_API_URL",
		"VUE_APP_API_URL",
		"API_URL",
	}

	for _, pattern := range patterns {
		if _, exists := existingEnv[pattern]; exists {
			return pattern
		}
	}

	// Check for any key containing API and URL
	for key := range existingEnv {
		keyUpper := strings.ToUpper(key)
		if strings.Contains(keyUpper, "API") && strings.Contains(keyUpper, "URL") {
			return key
		}
	}

	// Default to VITE_API_URL (most common for modern Vue apps)
	return "VITE_API_URL"
}

// detectCorsEnvKey tries to detect the CORS configuration env key
func detectCorsEnvKey(existingEnv map[string]string) string {
	patterns := []string{
		"Cors__AllowedOrigins", // .NET
		"CORS_ALLOWED_ORIGINS", // Generic
		"CORS_ORIGINS",         // Generic
		"ALLOWED_ORIGINS",      // Generic
		"CORS_ORIGIN",          // Singular
	}

	for _, pattern := range patterns {
		if _, exists := existingEnv[pattern]; exists {
			return pattern
		}
	}

	// Default to .NET pattern (since that's the main use case)
	return "Cors__AllowedOrigins"
}

// addCorsOrigin adds an origin to a comma-separated list
func addCorsOrigin(existing, newOrigin string) string {
	if existing == "" {
		return newOrigin
	}

	origins := strings.Split(existing, ",")
	for _, origin := range origins {
		if strings.TrimSpace(origin) == newOrigin {
			return existing
		}
	}

	return existing + "," + newOrigin
}

// removeCorsOrigin removes an origin from a comma-separated list
func removeCorsOrigin(existing, originToRemove string) string {
	if existing == "" {
		return ""
	}

	origins := strings.Split(existing, ",")
	var filtered []string
	for _, origin := range origins {
		if strings.TrimSpace(origin) != originToRemove {
			filtered = append(filtered, strings.TrimSpace(origin))
		}
	}

	return strings.Join(filtered, ",")
}
