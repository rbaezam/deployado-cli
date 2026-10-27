package cmd

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var envCmd = &cobra.Command{
	Use:   "env",
	Short: "Manage environment variables",
	Long:  "Commands for viewing and modifying application environment variables.",
}

var envListCmd = &cobra.Command{
	Use:   "list [app-id]",
	Short: "List environment variables",
	Args:  cobra.RangeArgs(0, 1),
	RunE:  runEnvList,
}

var envSetCmd = &cobra.Command{
	Use:   "set [app-id] KEY=value [KEY2=value2 ...]",
	Short: "Set environment variables",
	Long: `Set one or more environment variables.

Examples:
  deployado env set my-app DATABASE_URL=postgres://...
  deployado env set my-app PORT=3000 DEBUG=true`,
	Args: cobra.ArbitraryArgs,
	RunE: runEnvSet,
}

var envUnsetCmd = &cobra.Command{
	Use:   "unset [app-id] KEY [KEY2 ...]",
	Short: "Unset environment variables",
	Args:  cobra.ArbitraryArgs,
	RunE:  runEnvUnset,
}

var envImportCmd = &cobra.Command{
	Use:   "import [app-id] [file]",
	Short: "Import environment variables from a .env file",
	Long: `Import environment variables from a .env file.

If no file is specified, it reads from .env in the current directory.

Examples:
  deployado env import my-app              # imports from .env
  deployado env import my-app .env.prod    # imports from .env.prod
  deployado env import my-app --dry-run    # preview without applying`,
	Args: cobra.RangeArgs(0, 2),
	RunE: runEnvImport,
}

var envImportDryRun bool
var envImportSkipExisting bool
var envSetScope string
var envImportScope string
var envListAppID string
var envSetAppID string
var envUnsetAppID string
var envImportAppID string

func init() {
	envCmd.AddCommand(envListCmd)
	envCmd.AddCommand(envSetCmd)
	envCmd.AddCommand(envUnsetCmd)
	envCmd.AddCommand(envImportCmd)

	envListCmd.Flags().StringVarP(&envListAppID, "app", "a", "", "App ID")
	envSetCmd.Flags().StringVarP(&envSetAppID, "app", "a", "", "App ID")
	envUnsetCmd.Flags().StringVarP(&envUnsetAppID, "app", "a", "", "App ID")
	envImportCmd.Flags().StringVarP(&envImportAppID, "app", "a", "", "App ID")

	envSetCmd.Flags().StringVar(&envSetScope, "scope", "runtime", "Scope for variables: runtime or build")
	envImportCmd.Flags().BoolVar(&envImportDryRun, "dry-run", false, "Preview changes without applying them")
	envImportCmd.Flags().BoolVar(&envImportSkipExisting, "skip-existing", false, "Skip variables that already exist in the app")
	envImportCmd.Flags().StringVar(&envImportScope, "scope", "runtime", "Scope for imported variables: runtime or build")
}

func runEnvList(cmd *cobra.Command, args []string) error {
	client, err := getClient()
	if err != nil {
		return err
	}

	appID, _, err := resolveEnvAppAndValues(args, envListAppID, 0, "no app id provided. Use [app-id] or --app")
	if err != nil {
		return err
	}

	env, err := client.GetEnv(appID)
	if err != nil {
		return fmt.Errorf("failed to get environment variables: %w", err)
	}

	if len(env) == 0 {
		fmt.Println("No environment variables set.")
		return nil
	}

	// Sort keys
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)

	for _, k := range keys {
		v := env[k]
		// Mask sensitive values
		if isSensitive(k) {
			v = maskValue(v)
		}
		fmt.Fprintf(w, "%s\t%s\n", k, v)
	}

	w.Flush()
	return nil
}

func runEnvSet(cmd *cobra.Command, args []string) error {
	client, err := getClient()
	if err != nil {
		return err
	}

	appID, pairs, err := resolveEnvAppAndValues(args, envSetAppID, 1, "usage: deployado env set [app-id] KEY=value [KEY2=value2 ...] or deployado env set --app <app-id> KEY=value")
	if err != nil {
		return err
	}
	scope := strings.TrimSpace(envSetScope)

	if err := validateEnvScope(scope); err != nil {
		return err
	}

	env := make(map[string]string)
	for _, pair := range pairs {
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid format: %s (expected KEY=value)", pair)
		}
		env[parts[0]] = parts[1]
	}

	if err := client.SetEnvWithScope(appID, env, scope); err != nil {
		return fmt.Errorf("failed to set environment variables: %w", err)
	}

	color.New(color.FgGreen).Printf("Set %d environment variable(s) in %s scope\n", len(env), scope)

	for k := range env {
		fmt.Printf("  %s\n", k)
	}

	fmt.Println("\nNote: Redeploy to apply changes.")

	return nil
}

func runEnvUnset(cmd *cobra.Command, args []string) error {
	client, err := getClient()
	if err != nil {
		return err
	}

	appID, keys, err := resolveEnvAppAndValues(args, envUnsetAppID, 1, "usage: deployado env unset [app-id] KEY [KEY2 ...] or deployado env unset --app <app-id> KEY [KEY2 ...]")
	if err != nil {
		return err
	}

	for _, key := range keys {
		if err := client.DeleteEnv(appID, key); err != nil {
			return fmt.Errorf("failed to unset %s: %w", key, err)
		}
		color.New(color.FgGreen).Printf("Unset %s\n", key)
	}

	fmt.Println("\nNote: Redeploy to apply changes.")

	return nil
}

func runEnvImport(cmd *cobra.Command, args []string) error {
	client, err := getClient()
	if err != nil {
		return err
	}

	appID, tailArgs, err := resolveEnvAppAndValues(args, envImportAppID, 0, "usage: deployado env import [app-id] [file] or deployado env import --app <app-id> [file]")
	if err != nil {
		return err
	}
	scope := strings.TrimSpace(envImportScope)

	if err := validateEnvScope(scope); err != nil {
		return err
	}

	// Determine file to import
	envFile := ".env"
	if len(tailArgs) > 0 {
		envFile = tailArgs[0]
	}

	// Check if file exists
	if _, err := os.Stat(envFile); os.IsNotExist(err) {
		return fmt.Errorf("file not found: %s", envFile)
	}

	// Parse the .env file
	envVars, err := parseEnvFile(envFile)
	if err != nil {
		return fmt.Errorf("failed to parse %s: %w", envFile, err)
	}

	if len(envVars) == 0 {
		fmt.Println("No environment variables found in", envFile)
		return nil
	}

	// If skip-existing, filter out existing variables
	if envImportSkipExisting {
		existingEnv, err := client.GetEnv(appID)
		if err != nil {
			return fmt.Errorf("failed to get existing environment variables: %w", err)
		}

		filtered := make(map[string]string)
		skipped := 0
		for k, v := range envVars {
			if _, exists := existingEnv[k]; exists {
				skipped++
			} else {
				filtered[k] = v
			}
		}

		if skipped > 0 {
			color.New(color.FgYellow).Printf("Skipping %d existing variable(s)\n", skipped)
		}
		envVars = filtered
	}

	if len(envVars) == 0 {
		fmt.Println("No new variables to import.")
		return nil
	}

	// Sort keys for display
	keys := make([]string, 0, len(envVars))
	for k := range envVars {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Dry run - just show what would be imported
	if envImportDryRun {
		color.New(color.FgCyan).Println("Dry run - the following variables would be imported:")
		fmt.Println()
		for _, k := range keys {
			v := envVars[k]
			if isSensitive(k) {
				v = maskValue(v)
			}
			fmt.Printf("  %s=%s\n", k, v)
		}
		fmt.Printf("\nTotal: %d variable(s)\n", len(envVars))
		return nil
	}

	// Actually import
	fmt.Printf("Importing %d variable(s) from %s...\n", len(envVars), envFile)

	if err := client.SetEnvWithScope(appID, envVars, scope); err != nil {
		return fmt.Errorf("failed to import environment variables: %w", err)
	}

	color.New(color.FgGreen).Printf("Successfully imported %d environment variable(s) into %s scope\n", len(envVars), scope)
	fmt.Println()
	for _, k := range keys {
		fmt.Printf("  %s\n", k)
	}

	fmt.Println("\nNote: Redeploy to apply changes.")

	return nil
}

func resolveEnvAppAndValues(args []string, appIDFlag string, minValues int, usageErr string) (string, []string, error) {
	flagApp := strings.TrimSpace(appIDFlag)
	if flagApp != "" {
		if len(args) < minValues {
			return "", nil, errors.New(usageErr)
		}
		return flagApp, args, nil
	}

	if len(args) < 1+minValues {
		return "", nil, errors.New(usageErr)
	}

	return args[0], args[1:], nil
}

func validateEnvScope(scope string) error {
	if scope == "runtime" || scope == "build" {
		return nil
	}

	return fmt.Errorf("invalid --scope %q (allowed: runtime, build)", scope)
}

// parseEnvFile reads and parses a .env file
func parseEnvFile(filename string) (map[string]string, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	env := make(map[string]string)
	lines := strings.Split(string(data), "\n")

	for _, line := range lines {
		// Trim whitespace
		line = strings.TrimSpace(line)

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Skip export prefix if present (export KEY=value)
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimPrefix(line, "export ")
		}

		// Split on first = only
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue // Skip malformed lines
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		// Remove surrounding quotes if present
		value = trimQuotes(value)

		// Skip empty keys
		if key == "" {
			continue
		}

		env[key] = value
	}

	return env, nil
}

// trimQuotes removes surrounding single or double quotes from a string
func trimQuotes(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

func isSensitive(key string) bool {
	sensitive := []string{
		"PASSWORD", "SECRET", "KEY", "TOKEN", "CREDENTIAL",
		"PRIVATE", "API_KEY", "AUTH",
	}

	keyUpper := strings.ToUpper(key)
	for _, s := range sensitive {
		if strings.Contains(keyUpper, s) {
			return true
		}
	}
	return false
}

func maskValue(v string) string {
	if len(v) <= 4 {
		return "****"
	}
	return v[:2] + "****" + v[len(v)-2:]
}
