package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/deployado/cli/internal/api"
	"github.com/deployado/cli/internal/config"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var (
	importFile      string
	importFormat    string
	importDropFirst bool
	importWait      bool
)

var databaseImportCmd = &cobra.Command{
	Use:   "import <database-id>",
	Short: "Import a SQL dump into a managed database",
	Long: `Import a SQL file (plain pg_dump or custom -Fc) into an existing
managed PostgreSQL database.

The file is uploaded to the control plane, staged on disk, and applied
by the database server's agent using psql or pg_restore. The CLI
polls and reports status until the restore completes.

Examples:
  # Plain SQL dump (psql)
  deployado database import a24e3a94 --file ./prod-dump.sql

  # Custom-format dump (pg_restore)
  deployado database import a24e3a94 --file ./prod.dump --format custom

  # Drop tables before restoring (use with care)
  deployado database import a24e3a94 --file ./prod.sql --drop-first

  # Don't wait — return as soon as the upload finishes
  deployado database import a24e3a94 --file ./prod.sql --no-wait`,
	Args: cobra.ExactArgs(1),
	RunE: runDatabaseImport,
}

func init() {
	databaseImportCmd.Flags().StringVarP(&importFile, "file", "f", "", "Path to the SQL dump (required)")
	databaseImportCmd.Flags().StringVar(&importFormat, "format", "auto", "Dump format: auto | plain | custom")
	databaseImportCmd.Flags().BoolVar(&importDropFirst, "drop-first", false, "Drop existing tables before restore (--clean --if-exists)")
	databaseImportCmd.Flags().BoolVar(&importWait, "wait", true, "Wait for the restore to finish before exiting")
	databaseImportCmd.Flags().Bool("no-wait", false, "Exit as soon as the upload finishes (do not poll)")
	_ = databaseImportCmd.MarkFlagRequired("file")

	databaseCmd.AddCommand(databaseImportCmd)
}

func runDatabaseImport(cmd *cobra.Command, args []string) error {
	noWait, _ := cmd.Flags().GetBool("no-wait")
	if noWait {
		importWait = false
	}

	databaseID := args[0]

	switch importFormat {
	case "auto", "plain", "custom":
	default:
		return fmt.Errorf("invalid --format %q (use auto, plain, or custom)", importFormat)
	}

	info, err := os.Stat(importFile)
	if err != nil {
		return fmt.Errorf("cannot read --file: %w", err)
	}
	if info.Size() == 0 {
		return errors.New("--file is empty")
	}

	client, err := getClient()
	if err != nil {
		return err
	}

	// Resolve prefix → full UUID if needed, mirroring `apps update`.
	fullID, err := resolveDatabaseID(client, databaseID)
	if err != nil {
		return err
	}

	file, err := os.Open(importFile)
	if err != nil {
		return fmt.Errorf("open file: %w", err)
	}
	defer file.Close()

	fileName := filepath.Base(importFile)

	fmt.Printf("Importing %s (%s) into database %s...\n",
		fileName, humanBytes(info.Size()), shortID(fullID))

	opts := map[string]string{
		"format":     importFormat,
		"drop_first": fmt.Sprintf("%v", importDropFirst),
	}

	bar := newProgressTracker(info.Size())

	importRecord, err := client.ImportDatabaseDump(fullID, fileName, file, info.Size(), opts, bar.update)
	bar.finish()
	if err != nil {
		return fmt.Errorf("upload failed: %w", err)
	}

	color.New(color.FgGreen).Printf("Upload complete. Import ID: %s\n", importRecord.ID)
	fmt.Printf("Status: %s\n", importRecord.Status)

	if !importWait {
		fmt.Println("Use `deployado database import-status` to check progress.")
		return nil
	}

	fmt.Println()
	fmt.Println("Waiting for restore to finish...")
	return pollImportUntilTerminal(client, fullID, importRecord.ID)
}

func pollImportUntilTerminal(client *api.Client, databaseID, importID string) error {
	deadline := time.Now().Add(30 * time.Minute)
	lastStatus := ""

	for {
		current, err := client.GetDatabaseImport(databaseID, importID)
		if err != nil {
			return fmt.Errorf("poll: %w", err)
		}

		if current.Status != lastStatus {
			fmt.Printf("  %s\n", current.Status)
			lastStatus = current.Status
		}

		switch current.Status {
		case "succeeded":
			color.New(color.FgGreen, color.Bold).Println("\nImport finished successfully.")
			return nil
		case "failed":
			color.New(color.FgRed, color.Bold).Println("\nImport failed.")
			if current.ErrorMessage != "" {
				fmt.Printf("\n%s\n", current.ErrorMessage)
			}
			return fmt.Errorf("import failed")
		case "cancelled":
			color.New(color.FgYellow).Println("\nImport cancelled.")
			return errors.New("import cancelled")
		}

		if time.Now().After(deadline) {
			return errors.New("polling timed out after 30 minutes — check status with `database import-status`")
		}

		time.Sleep(3 * time.Second)
	}
}

func resolveDatabaseID(client *api.Client, prefix string) (string, error) {
	// Already a full UUID
	if len(prefix) == 36 && strings.Count(prefix, "-") == 4 {
		return prefix, nil
	}

	// Need an account scope — but list-by-account is what the CLI has.
	dbs, err := client.ListDatabases("")
	if err != nil {
		return "", fmt.Errorf("list databases: %w", err)
	}

	var matches []string
	for _, db := range dbs {
		if strings.HasPrefix(db.ID, prefix) {
			matches = append(matches, db.ID)
		}
	}

	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no database matches %q", prefix)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("%q matches multiple databases — use the full UUID", prefix)
	}
}

// ---------------------------------------------------------------------------
// Progress helper
// ---------------------------------------------------------------------------

type progressTracker struct {
	total     int64
	last      int64
	lastPrint time.Time
}

func newProgressTracker(total int64) *progressTracker {
	return &progressTracker{total: total, lastPrint: time.Now()}
}

func (p *progressTracker) update(written int64) {
	// Refresh every ~500ms to keep terminal usable without spamming.
	if time.Since(p.lastPrint) < 500*time.Millisecond && written < p.total {
		return
	}
	p.last = written
	p.lastPrint = time.Now()

	percent := 0
	if p.total > 0 {
		percent = int((written * 100) / p.total)
		if percent > 100 {
			percent = 100
		}
	}
	fmt.Printf("\r  uploading: %s / %s (%d%%)",
		humanBytes(written), humanBytes(p.total), percent)
}

func (p *progressTracker) finish() {
	fmt.Println()
}

func humanBytes(n int64) string {
	const k = 1024
	if n < k {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(k), 0
	for x := n / k; x >= k; x /= k {
		div *= k
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// Avoid the unused import warning when config isn't yet referenced.
var _ = config.DefaultHost
