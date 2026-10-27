package cmd

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"strings"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// `deployado db psql <db-id>` opens an interactive psql session
// against a Deployado-managed database, using the credentials
// returned by the API.
//
// Why this exists:
//
// The daily workflow without it: `database get <id>` → copy
// connection string from the output → paste into `psql ...`. Three
// steps for a single intent. With this command: one step.
//
// Why we pass the password via PGPASSWORD env var instead of inline
// in the connection string:
//
//   - PGPASSWORD doesn't show up in `ps auxe` output for other
//     users on the same workstation (env is private to the process
//     and its children).
//   - Inline `postgresql://user:pass@host:port/db` IS visible in
//     `ps`. Bad for shared dev machines.
//   - It's also the convention Postgres docs recommend.
//
// We `exec.Command(...).Run()` rather than syscall.Exec so the
// CLI's deferred cleanup (transport close, etc.) runs after psql
// exits.
var (
	dbPsqlForce    bool
	dbPsqlApp      string
	dbPsqlReadonly bool
)

var databasePsqlCmd = &cobra.Command{
	Use:   "psql [db-id]",
	Short: "Open an interactive psql session against a database",
	Long: `Spawn a local psql process configured to connect to a Deployado-
managed PostgreSQL database. Credentials are fetched from the API
and passed to psql via PGPASSWORD (NOT inline in the connection
string, so the password doesn't leak via 'ps auxe').

Requires the local 'psql' binary on $PATH. Install with:

  - macOS:         brew install libpq && brew link --force libpq
  - Debian/Ubuntu: apt-get install postgresql-client
  - Fedora/RHEL:   dnf install postgresql

Most Deployado-managed databases listen on a private IP (10.x.x.x,
172.16-31.x.x, 192.168.x.x) and are only reachable from inside the
platform's private network. When the API reports a private host,
this command refuses to connect and tells you so up-front, instead
of letting psql fail with "Connection refused" 30 seconds later.
Pair with an SSH tunnel through your app server if you need access
from outside, or use --force to attempt the connection anyway (use
this only when you know your machine is on the private network).

Any extra arguments after a '--' separator are passed through to
psql, so you can do:

  deployado db psql <id> -- -c "SELECT count(*) FROM users;"
  deployado db psql <id> -- -f migration.sql

For an interactive session against an app's database WITHOUT a local psql
or a tunnel (it runs server-side via the DB agent), use --app:

  deployado db psql --app insurance-board
  deployado db psql --app insurance-board --readonly`,
	Args: cobra.ArbitraryArgs,
	RunE: runDatabasePsql,
}

func init() {
	databasePsqlCmd.Flags().BoolVar(&dbPsqlForce, "force", false,
		"Attempt the connection even if the host looks private (use only inside the private network)")
	databasePsqlCmd.Flags().StringVar(&dbPsqlApp, "app", "",
		"App ID or slug — open a server-side REPL against the app's database (no local psql needed)")
	databasePsqlCmd.Flags().BoolVar(&dbPsqlReadonly, "readonly", false,
		"Read-only session (only with --app)")
	databaseCmd.AddCommand(databasePsqlCmd)
}

func runDatabasePsql(_ *cobra.Command, args []string) error {
	// --app mode: server-side REPL via the DB agent (no local psql/tunnel).
	if dbPsqlApp != "" {
		client, err := getClient()
		if err != nil {
			return err
		}
		app, err := resolveApp(client, dbPsqlApp)
		if err != nil {
			return err
		}
		return runDbPsqlRepl(client, app, dbPsqlReadonly)
	}

	if len(args) == 0 {
		return fmt.Errorf("provide a database id, or use --app <app> for a server-side REPL")
	}

	dbID := args[0]
	psqlArgs := args[1:] // anything after the id is forwarded to psql

	client, err := getClient()
	if err != nil {
		return err
	}

	db, err := client.GetDatabase(dbID)
	if err != nil {
		return fmt.Errorf("failed to fetch database: %w", err)
	}

	// Parse the connection URL from the API so we can split out the
	// password (for PGPASSWORD) and pass the rest as discrete flags.
	// The API gives us a full postgresql:// URL — easier to parse it
	// once than to assemble from the discrete fields, which might
	// differ in subtle ways from what the server-side considers
	// canonical.
	if db.ConnectionURL == "" {
		return fmt.Errorf("database has no connection URL — is it provisioning? (status=%s)", db.Status)
	}

	u, err := url.Parse(db.ConnectionURL)
	if err != nil {
		return fmt.Errorf("malformed connection URL from API: %w", err)
	}

	password, _ := u.User.Password()
	username := u.User.Username()
	host := u.Hostname()
	port := u.Port()
	if port == "" {
		port = fmt.Sprintf("%d", db.DBPort)
	}
	dbName := db.DBName

	// Pre-flight: most Deployado-managed DBs live on private IPs
	// (10.0.0.x in the Hetzner deployment). Surfacing the
	// reachability problem before spawning psql saves the user a
	// confusing 30-second "connection refused" stall. --force is the
	// escape hatch for when you ARE inside the private network (an
	// SSH tunnel session, the dashboard server itself, etc.).
	if isPrivateHost(host) && !dbPsqlForce {
		color.New(color.FgRed).Printf("✗ Database host %s is private — not reachable from outside the platform network.\n", host)
		fmt.Println()
		fmt.Println("Common solutions:")
		fmt.Println("  • SSH tunnel through an app server:")
		fmt.Printf("      ssh -L 15432:%s:%s root@<app-server-ip>\n", host, port)
		fmt.Printf("      psql -h 127.0.0.1 -p 15432 -U %s -d %s\n", username, dbName)
		fmt.Println("  • Or run this command from a machine inside the private network")
		fmt.Println("  • Or pass --force if you know you're on the private network")
		return fmt.Errorf("private host %s unreachable", host)
	}

	// `psql -h ... -p ... -U ... -d ...` then any user-supplied
	// passthrough args. Order matters: positional dbname (-d) must
	// come before the passthrough so user flags can override it if
	// they really want a different one.
	cmdArgs := []string{
		"-h", host,
		"-p", port,
		"-U", username,
		"-d", dbName,
	}
	cmdArgs = append(cmdArgs, psqlArgs...)

	// Friendly preamble — useful when you're juggling multiple DBs
	// and want to confirm before typing destructive SQL.
	color.New(color.FgCyan).Printf("→ Connecting to %s ", db.Name)
	color.New(color.FgWhite, color.Faint).Printf("(%s @ %s:%s/%s)\n", username, host, port, dbName)

	psql := exec.Command("psql", cmdArgs...)
	psql.Env = append(os.Environ(),
		"PGPASSWORD="+password,
		// Sane defaults that make psql feel less hostile to
		// occasional users. Don't override if the user already set
		// them — pass-through wins.
		envDefault("PSQL_PAGER", "less -SRX"),
		envDefault("PSQL_HISTORY", os.Getenv("HOME")+"/.deployado_psql_history"),
	)
	psql.Stdin = os.Stdin
	psql.Stdout = os.Stdout
	psql.Stderr = os.Stderr

	if err := psql.Run(); err != nil {
		// psql exits non-zero for any client-side issue (network,
		// auth, query error in -c mode). Propagate that exit code
		// rather than wrapping into a generic Go error — scripts
		// invoking `deployado db psql ... -c '...'` rely on the
		// real exit code.
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		// Real error (psql not installed, etc.)
		return fmt.Errorf("failed to spawn psql: %w", err)
	}

	return nil
}

// envDefault returns "KEY=value" only if KEY is not already set in
// the parent's environment. Lets us provide nicer defaults (pager,
// history file) without clobbering user customizations.
func envDefault(key, def string) string {
	if existing, ok := os.LookupEnv(key); ok {
		return key + "=" + existing
	}
	return key + "=" + def
}

// isPrivateHost answers "could a typical laptop reach this host
// without a VPN/tunnel?". Yes for routable internet IPs and DNS
// names that look like FQDNs; no for RFC 1918 private ranges and
// loopback (which would only happen via tunnel).
//
// We deliberately do NOT actually DNS-resolve the host here —
// resolution is slow and noisy, and the goal is a fast pre-flight
// hint, not a definitive routability test. The psql attempt itself
// is the source of truth if --force is used.
func isPrivateHost(host string) bool {
	// Loopback string short-circuits.
	if host == "localhost" || strings.HasPrefix(host, "127.") {
		return true
	}

	ip := net.ParseIP(host)
	if ip == nil {
		// Not an IP — probably a hostname. Hostnames are typically
		// public (the API surface usually resolves them to public
		// IPs); when they don't (e.g. ".internal." TLDs), the user
		// will see the failure when psql tries.
		return false
	}

	return ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast()
}
