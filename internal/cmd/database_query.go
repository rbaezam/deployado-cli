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

var (
	dbQueryApp      string
	dbQueryJSON     bool
	dbQueryReadonly bool
)

// `deployado db query --app X "SELECT ..."` — run one-shot SQL against an
// app's database and print the result to stdout. No psql binary, no SSH
// tunnel, no rpc/tmp dance: the control plane forwards the query to the DB
// server's agent, which runs psql locally.
var databaseQueryCmd = &cobra.Command{
	Use:   "query <sql>",
	Short: "Run one-shot SQL against an app's database",
	Long: `Execute a single SQL statement against the database connected to an app
and print the result.

The query runs on the database server (via its agent) — you do NOT need a
local psql, an SSH tunnel, or network access to the private DB.

Examples:
  deployado db query --app insurance-board "select count(*) from agencies"
  deployado db query --app insurance-board --json "select id, name from plans limit 5"
  deployado db query --app insurance-board --readonly "select * from users limit 1"`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := getClient()
		if err != nil {
			return err
		}

		app, err := resolveApp(client, dbQueryApp)
		if err != nil {
			return err
		}

		sql := strings.Join(args, " ")

		resp, err := client.QueryAppDB(app.ID, sql, dbQueryReadonly, dbQueryJSON)
		if err != nil {
			return fmt.Errorf("query failed: %w", err)
		}

		printQueryResult(resp)
		if resp.ExitCode != 0 {
			os.Exit(resp.ExitCode)
		}
		return nil
	},
}

func printQueryResult(resp *api.DBQueryResponse) {
	if resp.Stdout != "" {
		fmt.Print(resp.Stdout)
		if !strings.HasSuffix(resp.Stdout, "\n") {
			fmt.Println()
		}
	}
	if resp.Stderr != "" {
		fmt.Fprint(os.Stderr, resp.Stderr)
		if !strings.HasSuffix(resp.Stderr, "\n") {
			fmt.Fprintln(os.Stderr)
		}
	}
}

// runDbPsqlRepl is the `db psql --app X` interactive loop. It mirrors the
// `deployado ssh` REPL: each statement is sent to the one-shot db query
// endpoint. Statements accumulate across lines until a terminating ';';
// backslash meta-commands (\dt, \d table, \l) are sent immediately since
// psql -c runs them too.
func runDbPsqlRepl(client *api.Client, app *api.App, readonly bool) error {
	cyan := color.New(color.FgCyan)
	dim := color.New(color.FgHiBlack)

	label := app.Slug
	if label == "" {
		label = app.Name
	}

	cyan.Printf("Connected to %s database", app.Name)
	if readonly {
		cyan.Print(" (read-only)")
	}
	fmt.Println()
	dim.Println("End statements with ';'. Type \\q, exit, or Ctrl+D to quit.")
	fmt.Println()

	scanner := bufio.NewScanner(os.Stdin)
	// Allow large pasted statements (default token limit is 64KB).
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var buf strings.Builder

	for {
		if buf.Len() == 0 {
			fmt.Printf("%s=# ", label)
		} else {
			fmt.Printf("%s-# ", label) // continuation
		}

		if !scanner.Scan() {
			fmt.Println()
			break // EOF (Ctrl+D)
		}

		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if buf.Len() == 0 {
			if trimmed == "" {
				continue
			}
			if trimmed == "\\q" || trimmed == "exit" || trimmed == "quit" {
				break
			}
		}

		if buf.Len() > 0 {
			buf.WriteString("\n")
		}
		buf.WriteString(line)

		full := strings.TrimSpace(buf.String())
		complete := strings.HasSuffix(full, ";") || strings.HasPrefix(full, "\\")
		if !complete {
			continue
		}

		resp, err := client.QueryAppDB(app.ID, full, readonly, false)
		buf.Reset()
		if err != nil {
			color.New(color.FgRed).Fprintf(os.Stderr, "error: %v\n", err)
			continue
		}
		printQueryResult(resp)
	}

	dim.Println("Disconnected.")
	return nil
}

func init() {
	databaseQueryCmd.Flags().StringVar(&dbQueryApp, "app", "", "App ID or slug (required)")
	databaseQueryCmd.Flags().BoolVar(&dbQueryJSON, "json", false, "Return rows as a JSON array")
	databaseQueryCmd.Flags().BoolVar(&dbQueryReadonly, "readonly", false,
		"Run in a read-only transaction")
	databaseQueryCmd.MarkFlagRequired("app")

	databaseCmd.AddCommand(databaseQueryCmd)
}
