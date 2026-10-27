package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/deployado/cli/internal/api"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var sshCmd = &cobra.Command{
	Use:   "ssh <app>",
	Short: "Open an interactive shell in an application container",
	Long: `Opens an interactive shell session in the running container.

The command connects through the Deployado API — no direct SSH access
to the server is required. Commands are executed via the agent running
on the server that hosts the app.

Examples:
  # Interactive shell for an app
  deployado ssh my-app

  # Run a single command (non-interactive)
  deployado ssh my-app -- ls -la /app

  # Use a custom working directory
  deployado ssh my-app --workdir /app`,
	Args: cobra.MinimumNArgs(1),
	RunE: runSSH,
}

func init() {
	sshCmd.Flags().StringP("workdir", "w", "", "Working directory inside the container")
}

func runSSH(cmd *cobra.Command, args []string) error {
	client, err := getClient()
	if err != nil {
		return err
	}

	appIdentifier := args[0]
	workdir, _ := cmd.Flags().GetString("workdir")

	// Resolve app
	app, err := resolveApp(client, appIdentifier)
	if err != nil {
		return err
	}

	// If extra args after --, run as a single command
	if len(args) > 1 {
		command := strings.Join(args[1:], " ")
		return execSingleCommand(client, app, command, workdir)
	}

	// Interactive REPL
	return execInteractive(client, app, workdir)
}

func execSingleCommand(client *api.Client, app *api.App, command, workdir string) error {
	result, err := client.ExecCommand(app.ID, command, workdir)
	if err != nil {
		return fmt.Errorf("exec failed: %w", err)
	}

	if result.Stdout != "" {
		fmt.Print(result.Stdout)
	}
	if result.Stderr != "" {
		fmt.Fprint(os.Stderr, result.Stderr)
	}

	if result.ExitCode != 0 {
		os.Exit(result.ExitCode)
	}

	return nil
}

func execInteractive(client *api.Client, app *api.App, workdir string) error {
	cyan := color.New(color.FgCyan)
	dim := color.New(color.FgHiBlack)

	cyan.Printf("Connected to %s\n", app.Name)
	dim.Println("Type 'exit' or press Ctrl+D to disconnect.")
	fmt.Println()

	scanner := bufio.NewScanner(os.Stdin)
	currentDir := workdir

	for {
		// Print prompt
		prompt := app.Slug
		if prompt == "" {
			prompt = app.Name
		}
		if currentDir != "" {
			prompt += ":" + currentDir
		}
		fmt.Printf("%s$ ", prompt)

		if !scanner.Scan() {
			// EOF (Ctrl+D)
			fmt.Println()
			break
		}

		command := strings.TrimSpace(scanner.Text())
		if command == "" {
			continue
		}

		if command == "exit" || command == "quit" {
			break
		}

		// Handle cd locally to maintain working directory state
		if strings.HasPrefix(command, "cd ") || command == "cd" {
			newDir := strings.TrimPrefix(command, "cd ")
			newDir = strings.TrimSpace(newDir)

			if newDir == "" || newDir == "~" {
				currentDir = ""
				continue
			}

			// Resolve the path on the remote container. shellQuote protects
			// against directories with spaces or shell metacharacters.
			resolveCmd := fmt.Sprintf("cd %s && pwd", shellQuote(joinCdPath(currentDir, newDir)))
			result, err := client.ExecCommand(app.ID, resolveCmd, "")
			if err != nil {
				color.New(color.FgRed).Fprintf(os.Stderr, "error: %v\n", err)
				continue
			}

			if result.ExitCode != 0 {
				if result.Stderr != "" {
					fmt.Fprint(os.Stderr, result.Stderr)
				} else {
					fmt.Fprintf(os.Stderr, "cd: no such directory: %s\n", newDir)
				}
				continue
			}

			currentDir = strings.TrimSpace(result.Stdout)
			continue
		}

		// Execute command
		result, err := client.ExecCommand(app.ID, command, currentDir)
		if err != nil {
			color.New(color.FgRed).Fprintf(os.Stderr, "error: %v\n", err)
			continue
		}

		if result.Stdout != "" {
			fmt.Print(result.Stdout)
			// Ensure newline at end of output
			if !strings.HasSuffix(result.Stdout, "\n") {
				fmt.Println()
			}
		}

		if result.Stderr != "" {
			fmt.Fprint(os.Stderr, result.Stderr)
			if !strings.HasSuffix(result.Stderr, "\n") {
				fmt.Fprintln(os.Stderr)
			}
		}
	}

	dim.Println("Disconnected.")
	return nil
}

// joinCdPath resolves a `cd` target against the current directory. Absolute
// paths win as-is; relative paths get prefixed with currentDir. The result
// is still a logical path — callers must pass it through shellQuote before
// embedding into a shell command string.
func joinCdPath(currentDir, target string) string {
	if strings.HasPrefix(target, "/") {
		// Absolute path
		return target
	}
	if currentDir == "" {
		return target
	}
	return currentDir + "/" + target
}

// resolveApp finds an app by ID, name, or slug
func resolveApp(client *api.Client, identifier string) (*api.App, error) {
	// First, try to get by ID directly
	app, err := client.GetApp(identifier)
	if err == nil {
		return app, nil
	}

	// If that fails, search by name/slug
	apps, err := client.ListApps()
	if err != nil {
		return nil, fmt.Errorf("failed to list apps: %w", err)
	}

	identifier = strings.ToLower(identifier)
	for _, a := range apps {
		if strings.ToLower(a.Name) == identifier || strings.ToLower(a.Slug) == identifier {
			return &a, nil
		}
	}

	return nil, fmt.Errorf("app '%s' not found", identifier)
}
