package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/deployado/cli/internal/api"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// releasesCmd groups release-scoped commands. Right now the only
// subcommand is `logs`, but the group exists so we have a natural
// home for `show`, `list-all`, etc. when those become useful — and
// so the noun matches the URL hierarchy users see in the API
// (`/api/v1/releases/...`).
var releasesCmd = &cobra.Command{
	Use:     "releases",
	Aliases: []string{"release"},
	Short:   "Inspect releases (builds)",
	Long: `Commands for working with releases — the build artifacts that
deployments are rolled out from.

A release id is the full UUID shown by --json output. The 8-character
prefix shown in tables is just for display; commands here require the
full id.`,
}

var releasesLogsCmd = &cobra.Command{
	Use:   "logs [release-id]",
	Short: "Print the build output for a release",
	Long: `Fetches the docker build transcript for a release.

Works for both finished and in-progress builds: for a running build,
you'll see whatever output has been captured so far. For one that
crashed mid-build, the same payload includes the error message that
the build pipeline recorded.

This is what 'deployado doctor' points at when its "Latest build"
check reports build_failed — copy the release id from doctor's
output and pass it here for the full transcript.`,
	Args: cobra.ExactArgs(1),
	RunE: runReleasesLogs,
}

var (
	releasesLogsJSON bool
	releasesLogsTail int
)

func init() {
	rootCmd.AddCommand(releasesCmd)
	releasesCmd.AddCommand(releasesLogsCmd)

	releasesLogsCmd.Flags().BoolVar(&releasesLogsJSON, "json", false,
		"Emit the raw API payload (logs + status + timing) as JSON")
	// --tail mirrors the convention of GNU tail: print the last N lines.
	// Default 0 means "print everything"; we keep that because build
	// transcripts are typically only a few hundred lines and the user
	// asked for them on purpose.
	releasesLogsCmd.Flags().IntVar(&releasesLogsTail, "tail", 0,
		"Only print the last N lines (0 = all)")
}

func runReleasesLogs(_ *cobra.Command, args []string) error {
	releaseID := args[0]

	client, err := getClient()
	if err != nil {
		return err
	}

	logs, err := client.GetBuildLogsByRelease(releaseID)
	if err != nil {
		return fmt.Errorf("failed to fetch build logs: %w", err)
	}
	if logs == nil {
		return fmt.Errorf("no build logs found for release %s", releaseID)
	}

	if releasesLogsJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(logs)
	}

	printBuildLogHeader(logs)

	body := logs.Logs
	if releasesLogsTail > 0 {
		body = lastNLines(body, releasesLogsTail)
	}
	if body == "" {
		fmt.Println("  (no output captured)")
	} else {
		fmt.Println(body)
	}

	printBuildLogFooter(logs)
	return nil
}

// printBuildLogHeader writes the status/timing/error block above the
// build transcript. We surface it twice (header + footer) because for
// long transcripts the user typically scrolls past the header and
// loses sight of "wait, did this actually succeed".
func printBuildLogHeader(logs *api.BuildLogsResponse) {
	color.New(color.FgCyan, color.Bold).Println("─── build status ───")
	fmt.Printf("  Status:   %s\n", logs.Status)
	if logs.DurationSeconds != nil {
		fmt.Printf("  Duration: %ds\n", *logs.DurationSeconds)
	}
	if logs.StartedAt != nil {
		fmt.Printf("  Started:  %s\n", *logs.StartedAt)
	}
	if logs.FinishedAt != nil {
		fmt.Printf("  Finished: %s\n", *logs.FinishedAt)
	}
	if logs.ErrorMessage != nil && *logs.ErrorMessage != "" {
		color.New(color.FgRed).Printf("  Error:    %s\n", *logs.ErrorMessage)
	}
	color.New(color.FgCyan).Println("─── build output ───")
}

func printBuildLogFooter(logs *api.BuildLogsResponse) {
	switch logs.Status {
	case "succeeded", "ready":
		color.New(color.FgGreen, color.Bold).Println("─── build succeeded ───")
	case "build_failed", "failed":
		color.New(color.FgRed, color.Bold).Println("─── build failed ───")
	case "building", "pending":
		color.New(color.FgYellow).Println("─── build in progress ───")
	default:
		color.New(color.FgWhite).Printf("─── status: %s ───\n", logs.Status)
	}
}

// lastNLines returns the trailing n newline-delimited lines of s.
// Defensive on n <= 0 (returns full input) and on inputs shorter than
// n lines (returns full input rather than blowing up).
//
// Subtle bit: walking backwards we want to stop AFTER seeing one more
// newline than n if the input ends with \n (so the trailing line
// counts as a separator, not as a line); if the input has a partial
// final line, that line already counts as one of the n, so we stop
// one newline earlier. The test for "trims correctly when last line
// has no trailing newline" pins this exact edge.
func lastNLines(s string, n int) string {
	if n <= 0 || s == "" {
		return s
	}
	target := n
	if len(s) > 0 && s[len(s)-1] != '\n' {
		target--
	}
	// Walk backwards from the end counting newlines. Cheap; avoids
	// the alloc of strings.Split on multi-MB transcripts.
	count := 0
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '\n' {
			count++
			if count > target {
				return s[i+1:]
			}
		}
	}
	return s
}
