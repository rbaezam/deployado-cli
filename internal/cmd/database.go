package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/deployado/cli/internal/api"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var databaseCmd = &cobra.Command{
	Use:     "database",
	Aliases: []string{"db", "databases"},
	Short:   "Manage PostgreSQL databases",
	Long: `Manage PostgreSQL databases.

Use subcommands to create, list, and connect databases to your apps.

Examples:
  # List all databases
  deployado database list

  # Create a new database
  deployado database create --name mydb

  # Connect a database to an app
  deployado database link <database-id> --to <app-id>

  # Show database details
  deployado database show <database-id>`,
}

// database list
var databaseListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all databases",
	Long: `List all databases for your account.

Examples:
  # List all databases
  deployado database list

  # List databases for a specific project
  deployado database list --project <project-id>`,
	RunE: runDatabaseList,
}

// database show
var databaseShowCmd = &cobra.Command{
	Use:   "show <database-id>",
	Short: "Show database details",
	Long: `Show detailed information about a database.

Examples:
  deployado database show abc123`,
	Args: cobra.ExactArgs(1),
	RunE: runDatabaseShow,
}

// database create
var databaseCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new database",
	Long: `Create a new PostgreSQL database.

The database will be created on a db-role server and will be ready
for use within a few seconds.

Examples:
  # Create a database (auto-selects server)
  deployado database create --name mydb

  # Create a database on a specific server
  deployado database create --name mydb --server <server-id>

  # Create a database with specific settings
  deployado database create --name mydb --version 16 --max-connections 200`,
	RunE: runDatabaseCreate,
}

// database delete
var databaseDeleteCmd = &cobra.Command{
	Use:   "delete <database-id>",
	Short: "Delete a database",
	Long: `Delete a database.

WARNING: This will permanently delete the database and all its data.
Make sure you have a backup before proceeding.

Examples:
  deployado database delete abc123`,
	Args: cobra.ExactArgs(1),
	RunE: runDatabaseDelete,
}

// database link
var databaseLinkCmd = &cobra.Command{
	Use:   "link <database-id> --to <app-id>",
	Short: "Connect a database to an app",
	Long: `Connect a database to an app.

This will add the database connection URL as an environment variable
on the app. By default, the variable is named DATABASE_URL.

Examples:
  # Link database to app with default env var
  deployado database link mydb-id --to myapp-id

  # Link with custom env var name
  deployado database link mydb-id --to myapp-id --env ConnectionStrings__DefaultConnection

  # Specify the database driver for the connection URL
  deployado database link mydb-id --to myapp-id --driver ecto`,
	Args: cobra.ExactArgs(1),
	RunE: runDatabaseLink,
}

// database unlink
var databaseUnlinkCmd = &cobra.Command{
	Use:   "unlink <database-id> --connection <connection-id>",
	Short: "Disconnect an app from a database",
	Long: `Disconnect an app from a database.

This removes the database connection, but does not delete the database.

Examples:
  deployado database unlink mydb-id --connection conn-id`,
	Args: cobra.ExactArgs(1),
	RunE: runDatabaseUnlink,
}

// database servers
var databaseServersCmd = &cobra.Command{
	Use:   "servers",
	Short: "List available database servers",
	Long: `List servers that can host databases (db-role servers).

Examples:
  deployado database servers`,
	RunE: runDatabaseServers,
}

var (
	dbProjectID    string
	dbServerID     string
	dbName         string
	dbVersion      string
	dbMaxConns     int
	dbNoBackup     bool
	dbLinkTo       string
	dbEnvVarName   string
	dbDriver       string
	dbConnectionID string
	dbForceDelete  bool
	dbListJSON     bool
	dbShowJSON     bool
)

func init() {
	// database list flags
	databaseListCmd.Flags().StringVar(&dbProjectID, "project", "", "Filter by project ID")
	databaseListCmd.Flags().BoolVar(&dbListJSON, "json", false, "Output as JSON")

	// database create flags
	databaseCreateCmd.Flags().StringVarP(&dbName, "name", "n", "", "Database name (required)")
	databaseCreateCmd.Flags().StringVarP(&dbServerID, "server", "s", "", "Server ID (auto-selects if not provided)")
	databaseCreateCmd.Flags().StringVar(&dbProjectID, "project", "", "Project ID to associate with")
	databaseCreateCmd.Flags().StringVar(&dbVersion, "version", "18", "PostgreSQL version (14, 15, 16, 17, 18)")
	databaseCreateCmd.Flags().IntVar(&dbMaxConns, "max-connections", 100, "Maximum connections")
	databaseCreateCmd.Flags().BoolVar(&dbNoBackup, "no-backup", false, "Disable automatic backups")
	databaseCreateCmd.MarkFlagRequired("name")

	// database show flags
	databaseShowCmd.Flags().BoolVar(&dbShowJSON, "json", false, "Output as JSON")

	// database delete flags
	databaseDeleteCmd.Flags().BoolVarP(&dbForceDelete, "force", "f", false, "Skip confirmation prompt")

	// database link flags
	databaseLinkCmd.Flags().StringVar(&dbLinkTo, "to", "", "App ID to connect to (required)")
	databaseLinkCmd.Flags().StringVar(&dbEnvVarName, "env", "DATABASE_URL", "Environment variable name")
	databaseLinkCmd.Flags().StringVar(&dbDriver, "driver", "auto", "Database driver (auto, psycopg2, asyncpg, ecto, pg, jdbc)")
	databaseLinkCmd.MarkFlagRequired("to")

	// database unlink flags
	databaseUnlinkCmd.Flags().StringVar(&dbConnectionID, "connection", "", "Connection ID to remove (required)")
	databaseUnlinkCmd.MarkFlagRequired("connection")

	// Add subcommands
	databaseCmd.AddCommand(databaseListCmd)
	databaseCmd.AddCommand(databaseShowCmd)
	databaseCmd.AddCommand(databaseCreateCmd)
	databaseCmd.AddCommand(databaseDeleteCmd)
	databaseCmd.AddCommand(databaseLinkCmd)
	databaseCmd.AddCommand(databaseUnlinkCmd)
	databaseCmd.AddCommand(databaseServersCmd)

	rootCmd.AddCommand(databaseCmd)
}

func runDatabaseList(cmd *cobra.Command, args []string) error {
	client, err := getClient()
	if err != nil {
		return err
	}

	databases, err := client.ListDatabases(dbProjectID)
	if err != nil {
		return fmt.Errorf("failed to list databases: %w", err)
	}

	if dbListJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]interface{}{"databases": databases})
	}

	if len(databases) == 0 {
		fmt.Println("No databases found.")
		fmt.Println()
		fmt.Println("Create one with: deployado database create --name <name>")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tSTATUS\tSERVER\tVERSION\tCONNECTIONS\tCREATED")
	fmt.Fprintln(w, "--\t----\t------\t------\t-------\t-----------\t-------")

	for _, db := range databases {
		status := formatDBStatus(db.Status)
		created := formatTimeAgo(db.CreatedAt)
		serverName := db.ServerName
		if serverName == "" {
			serverName = "-"
		}

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%d\t%s\n",
			truncateID(db.ID),
			db.Name,
			status,
			serverName,
			db.DBVersion,
			db.ConnectionCount,
			created,
		)
	}

	return w.Flush()
}

func runDatabaseShow(cmd *cobra.Command, args []string) error {
	client, err := getClient()
	if err != nil {
		return err
	}

	database, err := resolveDatabase(client, args[0])
	if err != nil {
		return err
	}

	if dbShowJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]interface{}{"database": database})
	}

	// Header
	color.New(color.FgCyan, color.Bold).Printf("Database: %s\n", database.Name)
	fmt.Println()

	// Details
	fmt.Printf("  ID:              %s\n", database.ID)
	fmt.Printf("  Status:          %s\n", formatDBStatus(database.Status))
	fmt.Printf("  PostgreSQL:      %s\n", database.DBVersion)
	fmt.Printf("  Server:          %s (%s)\n", database.ServerName, database.ServerID)
	if database.ProjectName != "" {
		fmt.Printf("  Project:         %s\n", database.ProjectName)
	}
	fmt.Println()

	// Connection info
	color.New(color.FgYellow).Println("Connection Info:")
	fmt.Printf("  Host:            %s\n", database.DBHost)
	fmt.Printf("  Port:            %d\n", database.DBPort)
	fmt.Printf("  Database:        %s\n", database.DBName)
	fmt.Printf("  User:            %s\n", database.DBUser)
	fmt.Printf("  Max Connections: %d\n", database.MaxConnections)
	fmt.Printf("  SSL:             %v\n", database.SSLEnabled)
	fmt.Println()

	// Connection URL (masked)
	if database.ConnectionURL != "" {
		color.New(color.FgYellow).Println("Connection URL:")
		fmt.Printf("  %s\n", maskPassword(database.ConnectionURL))
		fmt.Println()
	}

	// Backups
	color.New(color.FgYellow).Println("Backups:")
	if database.BackupEnabled {
		fmt.Printf("  Enabled:         Yes\n")
	} else {
		fmt.Printf("  Enabled:         No\n")
	}
	fmt.Println()

	// Error message if any
	if database.ErrorMessage != "" {
		color.New(color.FgRed).Printf("Error: %s\n", database.ErrorMessage)
		fmt.Println()
	}

	// Connected apps would be shown here if we had that data
	// For now, we just show connection count
	fmt.Printf("Connected Apps: %d\n", database.ConnectionCount)

	return nil
}

func runDatabaseCreate(cmd *cobra.Command, args []string) error {
	client, err := getClient()
	if err != nil {
		return err
	}

	params := map[string]interface{}{
		"name":            dbName,
		"db_version":      dbVersion,
		"max_connections": dbMaxConns,
		"backup_enabled":  !dbNoBackup,
	}

	// Let the control plane auto-select an online DB server when not provided.
	if dbServerID != "" {
		params["server_id"] = dbServerID
	}

	if dbProjectID != "" {
		params["project_id"] = dbProjectID
	}

	fmt.Printf("Creating database '%s'...\n", dbName)

	database, err := client.CreateDatabase(params)
	if err != nil {
		return fmt.Errorf("failed to create database: %w", err)
	}

	fmt.Println()
	color.New(color.FgGreen, color.Bold).Println("Database created!")
	fmt.Println()
	fmt.Printf("  ID:       %s\n", database.ID)
	fmt.Printf("  Name:     %s\n", database.Name)
	fmt.Printf("  Database: %s\n", database.DBName)
	fmt.Printf("  User:     %s\n", database.DBUser)
	fmt.Printf("  Host:     %s\n", database.DBHost)
	fmt.Printf("  Port:     %d\n", database.DBPort)
	fmt.Printf("  Status:   %s\n", formatDBStatus(database.Status))
	fmt.Println()

	if database.Status == "initializing" {
		color.New(color.FgYellow).Println("The database is being created. This usually takes a few seconds.")
		fmt.Println("Run 'deployado database show " + truncateID(database.ID) + "' to check the status.")
	}

	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Printf("  1. Connect to an app: deployado database link %s --to <app-id>\n", truncateID(database.ID))
	fmt.Printf("  2. Redeploy the app:  deployado deploy <app-id>\n")

	return nil
}

func runDatabaseDelete(cmd *cobra.Command, args []string) error {
	client, err := getClient()
	if err != nil {
		return err
	}

	// Resolve by ID, ID prefix, or name — same UX as `apps`. The CLI's
	// `database list` shows truncated 8-char IDs, so accepting prefixes
	// here lets users copy what they see and have it Just Work.
	database, err := resolveDatabase(client, args[0])
	if err != nil {
		return err
	}
	databaseID := database.ID

	// Confirm deletion
	if !dbForceDelete {
		color.New(color.FgRed, color.Bold).Printf("WARNING: This will permanently delete the database '%s' and all its data.\n", database.Name)
		fmt.Println()
		fmt.Printf("Database: %s\n", database.DBName)
		fmt.Printf("Server:   %s\n", database.ServerName)
		fmt.Printf("Apps:     %d connected\n", database.ConnectionCount)
		fmt.Println()

		if database.ConnectionCount > 0 {
			color.New(color.FgYellow).Println("Warning: This database has connected apps. They will lose access to the database.")
			fmt.Println()
		}

		if database.DBName != "" && database.DBName != database.Name {
			fmt.Printf("Type the database name to confirm (%s or %s): ", database.Name, database.DBName)
		} else {
			fmt.Print("Type the database name to confirm: ")
		}
		var confirm string
		fmt.Scanln(&confirm)

		if !matchesDatabaseDeleteConfirmation(confirm, database.Name, database.DBName) {
			fmt.Println("Deletion cancelled.")
			return nil
		}
	}

	fmt.Printf("Deleting database '%s'...\n", database.Name)

	if err := client.DeleteDatabase(databaseID); err != nil {
		return fmt.Errorf("failed to delete database: %w", err)
	}

	color.New(color.FgGreen).Println("Database deletion started.")

	return nil
}

func matchesDatabaseDeleteConfirmation(input, databaseName, dbName string) bool {
	confirm := strings.TrimSpace(input)
	if confirm == "" {
		return false
	}

	if confirm == databaseName {
		return true
	}

	return dbName != "" && confirm == dbName
}

func runDatabaseLink(cmd *cobra.Command, args []string) error {
	client, err := getClient()
	if err != nil {
		return err
	}

	appID := dbLinkTo

	// Resolve database by ID, prefix, or name.
	database, err := resolveDatabase(client, args[0])
	if err != nil {
		return err
	}
	databaseID := database.ID

	// Get app info
	app, err := client.GetApp(appID)
	if err != nil {
		return fmt.Errorf("failed to get app: %w", err)
	}

	fmt.Println("Connecting database to app...")
	fmt.Printf("  Database: %s (%s)\n", database.Name, database.DBName)
	fmt.Printf("  App:      %s\n", app.Name)
	fmt.Printf("  Env Var:  %s\n", dbEnvVarName)
	fmt.Println()

	params := map[string]interface{}{
		"app_id":       appID,
		"env_var_name": dbEnvVarName,
		"driver":       dbDriver,
	}

	connection, err := client.ConnectDatabase(databaseID, params)
	if err != nil {
		return fmt.Errorf("failed to connect database: %w", err)
	}

	color.New(color.FgGreen, color.Bold).Println("Database connected!")
	fmt.Println()
	fmt.Printf("Connection ID: %s\n", connection.ID)
	fmt.Printf("Environment variable '%s' will be set on the app.\n", connection.EnvVarName)
	fmt.Println()

	color.New(color.FgYellow).Println("Remember to redeploy the app for changes to take effect:")
	fmt.Printf("  deployado deploy %s\n", app.ID)

	return nil
}

func runDatabaseUnlink(cmd *cobra.Command, args []string) error {
	client, err := getClient()
	if err != nil {
		return err
	}

	database, err := resolveDatabase(client, args[0])
	if err != nil {
		return err
	}
	databaseID := database.ID

	fmt.Println("Disconnecting app from database...")

	if err := client.DisconnectDatabase(databaseID, dbConnectionID); err != nil {
		return fmt.Errorf("failed to disconnect: %w", err)
	}

	color.New(color.FgGreen).Println("App disconnected from database.")
	fmt.Println()
	fmt.Println("Remember to redeploy the app for changes to take effect.")

	return nil
}

func runDatabaseServers(cmd *cobra.Command, args []string) error {
	client, err := getClient()
	if err != nil {
		return err
	}

	servers, err := client.ListDBServers()
	if err != nil {
		return fmt.Errorf("failed to list database servers: %w", err)
	}

	if len(servers) == 0 {
		fmt.Println("No database servers available.")
		fmt.Println()
		fmt.Println("Add a server with the 'db' role to host databases.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tIP ADDRESS\tSTATUS")
	fmt.Fprintln(w, "--\t----\t----------\t------")

	for _, server := range servers {
		status := formatServerStatus(server.Status)
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
			truncateID(server.ID),
			server.Name,
			server.IPAddress,
			status,
		)
	}

	return w.Flush()
}

// Helper functions

func formatDBStatus(status string) string {
	switch status {
	case "ready":
		return color.GreenString("ready")
	case "initializing":
		return color.YellowString("initializing")
	case "failed":
		return color.RedString("failed")
	case "deleting":
		return color.YellowString("deleting")
	default:
		return status
	}
}

func formatServerStatus(status string) string {
	switch status {
	case "online":
		return color.GreenString("online")
	case "offline":
		return color.RedString("offline")
	default:
		return status
	}
}

func truncateID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func formatTimeAgo(timestamp string) string {
	t, err := time.Parse(time.RFC3339, timestamp)
	if err != nil {
		// Try alternative format
		t, err = time.Parse("2006-01-02T15:04:05.000000Z", timestamp)
		if err != nil {
			return timestamp
		}
	}

	duration := time.Since(t)

	if duration.Hours() >= 24*30 {
		months := int(duration.Hours() / (24 * 30))
		return fmt.Sprintf("%dmo ago", months)
	} else if duration.Hours() >= 24 {
		days := int(duration.Hours() / 24)
		return fmt.Sprintf("%dd ago", days)
	} else if duration.Hours() >= 1 {
		hours := int(duration.Hours())
		return fmt.Sprintf("%dh ago", hours)
	} else if duration.Minutes() >= 1 {
		mins := int(duration.Minutes())
		return fmt.Sprintf("%dm ago", mins)
	}
	return "just now"
}

func maskPassword(url string) string {
	// Mask password in connection URL
	// postgresql://user:password@host:port/db -> postgresql://user:****@host:port/db
	parts := strings.SplitN(url, "://", 2)
	if len(parts) != 2 {
		return url
	}

	prefix := parts[0]
	rest := parts[1]

	// Find the @ separator
	atIndex := strings.LastIndex(rest, "@")
	if atIndex == -1 {
		return url
	}

	credentials := rest[:atIndex]
	hostAndDb := rest[atIndex:]

	// Find : in credentials to separate user:password
	colonIndex := strings.Index(credentials, ":")
	if colonIndex == -1 {
		return url
	}

	user := credentials[:colonIndex]
	return fmt.Sprintf("%s://%s:****%s", prefix, user, hostAndDb)
}

// resolveDatabase finds a database by full UUID, ID prefix, or name.
//
// `database list` shows truncated 8-char IDs; before this resolver
// existed, users had to copy the full UUID from the dashboard for any
// subsequent command. Accepting prefixes here matches the `apps` UX
// and the truncation displayed in `list`.
//
// Resolution order:
//  1. Treat identifier as a full UUID and call GetDatabase directly
//     (fast path — one HTTP request when the user copied the full ID).
//  2. On miss, list all databases and try:
//     a. case-insensitive name match
//     b. case-insensitive ID prefix match — accept only when exactly
//        one database matches the prefix; ambiguous prefixes return an
//        error listing the candidates so the user can disambiguate.
func resolveDatabase(client *api.Client, identifier string) (*api.Database, error) {
	if identifier == "" {
		return nil, fmt.Errorf("database identifier is required")
	}

	// Fast path: identifier might be a full UUID
	if db, err := client.GetDatabase(identifier); err == nil {
		return db, nil
	}

	databases, err := client.ListDatabases("")
	if err != nil {
		return nil, fmt.Errorf("failed to list databases: %w", err)
	}

	needle := strings.ToLower(identifier)
	var nameMatch *api.Database
	var prefixMatches []api.Database
	for i := range databases {
		db := databases[i]
		dbName := strings.ToLower(db.Name)
		dbID := strings.ToLower(db.ID)
		switch {
		case dbName == needle:
			nameMatch = &db
		case strings.HasPrefix(dbID, needle):
			prefixMatches = append(prefixMatches, db)
		}
	}

	if nameMatch != nil {
		return nameMatch, nil
	}

	switch len(prefixMatches) {
	case 0:
		return nil, fmt.Errorf("database '%s' not found — run `deployado database list` to see available IDs", identifier)
	case 1:
		return &prefixMatches[0], nil
	default:
		var ids []string
		for _, db := range prefixMatches {
			ids = append(ids, fmt.Sprintf("%s (%s)", db.ID, db.Name))
		}
		return nil, fmt.Errorf(
			"database prefix '%s' is ambiguous — matches:\n  %s\nUse the full ID or a longer prefix",
			identifier, strings.Join(ids, "\n  "),
		)
	}
}
