package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var execCmd = &cobra.Command{
	Use:   "exec <app> -- <command>",
	Short: "Execute a command in an application container",
	Long: `Execute a one-off command inside the running container.

Unlike 'ssh', this command runs a single command and exits immediately.
Useful for running migrations, one-off scripts, or debugging.

The command must be specified after '--' to separate it from flags.

Examples:
  # Run database migrations
  deployado exec my-app -- rails db:migrate

  # Check environment variables
  deployado exec my-app -- env

  # Run a shell command
  deployado exec my-app -- sh -c "ls -la /app"

  # Check Node.js version
  deployado exec my-app -- node --version

  # Interactive mode (if needed)
  deployado exec my-app -i -- /bin/sh`,
	Args: cobra.MinimumNArgs(1),
	RunE: runExec,
}

func init() {
	execCmd.Flags().BoolP("interactive", "i", false, "Keep STDIN open for interactive commands")
	execCmd.Flags().BoolP("tty", "t", false, "Allocate a pseudo-TTY")
	execCmd.Flags().StringP("user", "u", "root", "SSH user for connecting to the server")
	rootCmd.AddCommand(execCmd)
}

func runExec(cmd *cobra.Command, args []string) error {
	client, err := getClient()
	if err != nil {
		return err
	}

	// Parse arguments - app identifier is first, rest is the command
	appIdentifier := args[0]

	// Find the command after the app identifier
	// The command comes after '--' in the args
	var commandArgs []string
	dashIndex := -1
	for i, arg := range os.Args {
		if arg == "--" {
			dashIndex = i
			break
		}
	}

	if dashIndex != -1 && dashIndex < len(os.Args)-1 {
		commandArgs = os.Args[dashIndex+1:]
	} else if len(args) > 1 {
		// Fallback: use remaining args as command
		commandArgs = args[1:]
	}

	if len(commandArgs) == 0 {
		return fmt.Errorf("no command specified. Use: deployado exec <app> -- <command>")
	}

	interactive, _ := cmd.Flags().GetBool("interactive")
	tty, _ := cmd.Flags().GetBool("tty")
	sshUser, _ := cmd.Flags().GetString("user")

	// Step 1: Find the app
	app, err := resolveApp(client, appIdentifier)
	if err != nil {
		return err
	}

	// Step 2: Get current deployment
	deployment, err := client.GetCurrentDeployment(app.ID)
	if err != nil {
		return fmt.Errorf("failed to get deployment: %w", err)
	}

	if deployment == nil {
		return fmt.Errorf("no active deployment for '%s'", app.Name)
	}

	if deployment.ContainerID == nil || *deployment.ContainerID == "" {
		return fmt.Errorf("no running container for '%s'", app.Name)
	}

	containerID := *deployment.ContainerID

	// Step 3: Find the server IP
	servers, err := client.ListServers()
	if err != nil {
		return fmt.Errorf("failed to list servers: %w", err)
	}

	var serverIP string
	for _, server := range servers {
		if server.Name == deployment.ServerName || server.ID == app.TargetServerID {
			serverIP = server.IPAddress
			break
		}
	}

	if serverIP == "" {
		return fmt.Errorf("could not find server for deployment")
	}

	// Build docker exec flags
	dockerFlags := ""
	if interactive {
		dockerFlags += "-i "
	}
	if tty {
		dockerFlags += "-t "
	}
	if dockerFlags == "" {
		// Default: non-interactive, no tty for script-friendly output
		dockerFlags = ""
	}

	// Build the docker exec command.
	//
	// The command travels through several layers of shell parsing:
	//   local shell → cobra → ssh → remote shell → docker exec
	// Each layer strips quotes per its own rules, so a naive
	// strings.Join(args, " ") loses any quoting the user typed locally.
	// We re-quote every arg with POSIX single-quote escaping so the remote
	// shell parses it back into the same argv the user originally typed.
	//
	// Note: docker exec uses positional syntax (CONTAINER COMMAND [ARG...])
	// and does NOT accept `--` as a flag/positional separator — passing `--`
	// would make docker treat it as the command itself.
	quotedArgs := make([]string, len(commandArgs))
	for i, a := range commandArgs {
		quotedArgs[i] = shellQuote(a)
	}
	commandStr := strings.Join(quotedArgs, " ")
	dockerCmd := fmt.Sprintf("docker exec %s%s %s", dockerFlags, containerID, commandStr)

	// Show what we're running. Print the human-readable version (un-quoted
	// args joined with spaces) so the user sees what they typed, not the
	// shell-escaped wire form.
	color.New(color.FgHiBlack).Printf("Running on %s: %s\n", deployment.ServerName, strings.Join(commandArgs, " "))

	// Build SSH command
	sshArgs := []string{
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
	}

	if tty {
		sshArgs = append([]string{"-t"}, sshArgs...)
	}

	sshArgs = append(sshArgs, fmt.Sprintf("%s@%s", sshUser, serverIP), dockerCmd)

	sshCommand := exec.Command("ssh", sshArgs...)

	if interactive {
		sshCommand.Stdin = os.Stdin
	}
	sshCommand.Stdout = os.Stdout
	sshCommand.Stderr = os.Stderr

	err = sshCommand.Run()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		return fmt.Errorf("command failed: %w", err)
	}

	return nil
}

// shellQuote escapes a string so a POSIX shell parses it back as a single
// literal argument, regardless of the characters it contains. We wrap the
// value in single quotes and escape any embedded single quote with the
// classic `'\''` dance (close-quote, escaped-quote, open-quote). Strings
// that contain only "safe" characters are returned as-is to keep the wire
// form readable.
//
// Safe characters: ASCII letters, digits, and `_-./:@+=,%`. Anything else
// (spaces, $, ', ", *, ?, |, ;, &, (, ), {, }, [, ], <, >, \, !, #, ~,
// backtick, newline, etc.) triggers quoting.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if isShellSafe(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// isShellSafe reports whether s consists entirely of characters that a
// POSIX shell will leave untouched without quoting. Conservative: when in
// doubt, return false so the caller quotes.
func isShellSafe(s string) bool {
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case strings.ContainsRune("_-./:@+=,%", r):
		default:
			return false
		}
	}
	return true
}
