package cmd

import (
	"fmt"
	"time"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var logsCmd = &cobra.Command{
	Use:   "logs [app-id]",
	Short: "View app logs",
	Long: `View logs for an application.

By default, shows the most recent 100 lines.

Use -f to follow log output in real-time (polls every 2 seconds).`,
	Args: cobra.ExactArgs(1),
	RunE: runLogs,
}

func init() {
	logsCmd.Flags().IntP("lines", "n", 100, "Number of lines to show")
	logsCmd.Flags().BoolP("follow", "f", false, "Follow log output")
}

func runLogs(cmd *cobra.Command, args []string) error {
	client, err := getClient()
	if err != nil {
		return err
	}

	appID := args[0]
	lines, _ := cmd.Flags().GetInt("lines")
	follow, _ := cmd.Flags().GetBool("follow")

	// Get app info
	app, err := client.GetApp(appID)
	if err != nil {
		return fmt.Errorf("failed to get app: %w", err)
	}

	fmt.Printf("Logs for %s:\n", app.Name)
	fmt.Println("---")

	// Get logs
	logsResp, err := client.GetLogs(appID, lines)
	if err != nil {
		return fmt.Errorf("failed to get logs: %w", err)
	}

	if logsResp.Message != "" && len(logsResp.Logs) == 0 {
		color.New(color.FgYellow).Println(logsResp.Message)
		fmt.Println("\nTip: View logs in the Deployado dashboard for now.")
		return nil
	}

	// Print logs
	for _, entry := range logsResp.Logs {
		printLogEntry(entry.Timestamp, entry.Message, entry.Stream)
	}

	if !follow {
		return nil
	}

	// Follow mode - poll for new logs
	fmt.Println("\n--- Following logs (Ctrl+C to stop) ---")

	lastTimestamp := ""
	if len(logsResp.Logs) > 0 {
		lastTimestamp = logsResp.Logs[len(logsResp.Logs)-1].Timestamp
	}

	for {
		time.Sleep(2 * time.Second)

		logsResp, err := client.GetLogs(appID, 50)
		if err != nil {
			continue // Ignore errors in follow mode
		}

		// Print only new logs
		foundLast := lastTimestamp == ""
		for _, entry := range logsResp.Logs {
			if foundLast {
				printLogEntry(entry.Timestamp, entry.Message, entry.Stream)
				lastTimestamp = entry.Timestamp
			} else if entry.Timestamp == lastTimestamp {
				foundLast = true
			}
		}
	}
}

func printLogEntry(timestamp, message, stream string) {
	timeColor := color.New(color.FgHiBlack)
	var msgColor *color.Color

	if stream == "stderr" {
		msgColor = color.New(color.FgRed)
	} else {
		msgColor = color.New(color.FgWhite)
	}

	if timestamp != "" {
		timeColor.Printf("[%s] ", formatTimestamp(timestamp))
	}
	msgColor.Println(message)
}

func formatTimestamp(ts string) string {
	// Try to parse and format nicely
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ts
	}
	return t.Format("15:04:05")
}
