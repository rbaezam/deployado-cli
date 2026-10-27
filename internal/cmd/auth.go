package cmd

import (
	"errors"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/deployado/cli/internal/api"
	"github.com/deployado/cli/internal/config"
	"github.com/fatih/color"
	"github.com/pkg/browser"
	"github.com/spf13/cobra"
)

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Manage authentication",
	Long:  "Commands for logging in, logging out, switching between accounts, and checking authentication status.",
}

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Log in to Deployado",
	Long: `Log in to Deployado using your browser.

This will open a browser window where you can authenticate.
On success, your credentials are stored as a named profile and become active.
Use --profile to choose the profile name; otherwise the account slug is used.`,
	RunE: runLogin,
}

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Log out of Deployado",
	Long: `Remove a stored profile from this machine.

By default, removes the active profile. Use --profile to target a specific one,
or --all to remove every profile.`,
	RunE: runLogout,
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show authentication status",
	Long:  "Show authentication status and account information for the active or selected profile.",
	RunE:  runStatus,
}

var listCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List configured profiles",
	Long:    "List every saved profile. The active profile is marked with *.",
	RunE:    runList,
}

var switchCmd = &cobra.Command{
	Use:   "switch <profile>",
	Short: "Switch the active profile",
	Long:  "Make a saved profile active so subsequent commands use its credentials.",
	Args:  cobra.ExactArgs(1),
	RunE:  runSwitch,
}

var renameCmd = &cobra.Command{
	Use:   "rename <old> <new>",
	Short: "Rename a profile",
	Long: `Rename a saved profile.

If the renamed profile was active, it stays active under its new name.
Fails if a profile with the new name already exists.`,
	Args: cobra.ExactArgs(2),
	RunE: runRename,
}

func init() {
	authCmd.AddCommand(loginCmd)
	authCmd.AddCommand(logoutCmd)
	authCmd.AddCommand(statusCmd)
	authCmd.AddCommand(listCmd)
	authCmd.AddCommand(switchCmd)
	authCmd.AddCommand(renameCmd)

	loginCmd.Flags().StringP("host", "H", "", "Deployado host (default: https://deployado.com)")
	loginCmd.Flags().String("name", "", "Profile name (default: account slug)")

	logoutCmd.Flags().Bool("all", false, "Remove every saved profile")
}

func runLogin(cmd *cobra.Command, args []string) error {
	host, _ := cmd.Flags().GetString("host")
	if host == "" {
		host = config.DefaultHost
	}
	// --profile is a persistent root flag; --name is a login-local override.
	profileName, _ := cmd.Flags().GetString("name")
	if profileName == "" {
		profileName = profileFlag
	}

	client := api.NewClient(host, "")

	fmt.Println("Requesting device authorization...")
	deviceResp, err := client.RequestDeviceCode()
	if err != nil {
		return fmt.Errorf("failed to request device code: %w", err)
	}

	fmt.Println()
	color.New(color.Bold).Println("To authorize this device, open:")
	color.New(color.FgCyan, color.Bold).Printf("  %s\n\n", deviceResp.VerificationURI)

	color.New(color.Bold).Println("And enter the code:")
	color.New(color.FgYellow, color.Bold).Printf("  %s\n\n", deviceResp.UserCode)

	if deviceResp.VerificationURIComplete != "" {
		fmt.Println("Opening browser...")
		if err := browser.OpenURL(deviceResp.VerificationURIComplete); err != nil {
			fmt.Println("Could not open browser automatically. Please open the URL manually.")
		}
	}

	fmt.Println()
	fmt.Println("Waiting for authorization...")

	token, err := pollForToken(client, deviceResp.DeviceCode, deviceResp.Interval, deviceResp.ExpiresIn)
	if err != nil {
		return err
	}

	// Verify the token works and learn account metadata for the profile.
	client = api.NewClient(host, token)
	me, err := client.GetMe()
	if err != nil {
		return fmt.Errorf("failed to verify token: %w", err)
	}

	if profileName == "" {
		profileName = me.Account.Slug
		if profileName == "" {
			profileName = config.DefaultProfileName
		}
	}

	cfg := config.LoadOrEmpty()
	_, replacing := cfg.Profiles[profileName]

	profile := &config.Profile{
		Host:        host,
		AccessToken: token,
		Email:       me.User.Email,
		AccountSlug: me.Account.Slug,
		AccountName: me.Account.Name,
	}
	if err := config.PutProfile(profileName, profile, true); err != nil {
		return fmt.Errorf("failed to save profile: %w", err)
	}

	fmt.Println()
	if replacing {
		color.New(color.FgGreen, color.Bold).Printf("Updated profile %q (now active).\n", profileName)
	} else {
		color.New(color.FgGreen, color.Bold).Printf("Logged in as profile %q (now active).\n", profileName)
	}
	fmt.Printf("  Account: %s\n", me.Account.Name)
	fmt.Printf("  User:    %s\n", me.User.Email)
	return nil
}

func pollForToken(client *api.Client, deviceCode string, interval, expiresIn int) (string, error) {
	if interval < 1 {
		interval = 5
	}

	deadline := time.Now().Add(time.Duration(expiresIn) * time.Second)
	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if time.Now().After(deadline) {
				return "", fmt.Errorf("device code expired")
			}

			tokenResp, err := client.PollDeviceToken(deviceCode)
			if err != nil {
				if apiErr, ok := err.(*api.Error); ok {
					switch apiErr.Code {
					case "authorization_pending":
						continue
					case "slow_down":
						time.Sleep(5 * time.Second)
						continue
					case "expired_token":
						return "", fmt.Errorf("device code expired")
					case "access_denied":
						return "", fmt.Errorf("authorization denied")
					}
				}
				return "", fmt.Errorf("failed to poll for token: %w", err)
			}

			return tokenResp.AccessToken, nil
		}
	}
}

func runLogout(cmd *cobra.Command, args []string) error {
	allFlag, _ := cmd.Flags().GetBool("all")

	if allFlag {
		if err := config.Delete(); err != nil {
			if os.IsNotExist(err) {
				fmt.Println("Not logged in.")
				return nil
			}
			return fmt.Errorf("failed to remove config: %w", err)
		}
		color.New(color.FgGreen).Println("Removed all profiles.")
		return nil
	}

	cfg, err := config.Load()
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("Not logged in.")
			return nil
		}
		return fmt.Errorf("failed to load config: %w", err)
	}

	name := config.ResolveProfileName(cfg, profileFlag)
	if name == "" {
		fmt.Println("Not logged in.")
		return nil
	}

	if err := config.RemoveProfile(name); err != nil {
		return err
	}

	color.New(color.FgGreen).Printf("Removed profile %q.\n", name)

	// Tell the user what's active now (if anything).
	cfgAfter, err := config.Load()
	if err == nil && cfgAfter.Active != "" {
		fmt.Printf("Active profile is now %q.\n", cfgAfter.Active)
	}
	return nil
}

func runStatus(cmd *cobra.Command, args []string) error {
	p, name, err := config.Active(profileFlag)
	if err != nil {
		if errors.Is(err, config.ErrNoProfile) {
			fmt.Println("Not logged in. Run 'deployado auth login' to authenticate.")
			return nil
		}
		return err
	}

	client := api.NewClient(p.Host, p.AccessToken)
	me, err := client.GetMe()
	if err != nil {
		fmt.Printf("Profile %q: stored but token cannot be verified. You may need to log in again.\n", name)
		return nil
	}

	// Opportunistic backfill so 'auth list' has metadata for profiles migrated
	// from the legacy single-account config.
	if p.Email == "" || p.AccountSlug == "" {
		p.Email = me.User.Email
		p.AccountSlug = me.Account.Slug
		p.AccountName = me.Account.Name
		_ = config.PutProfile(name, p, false)
	}

	fmt.Println("Logged in to Deployado")
	fmt.Printf("  Profile: %s\n", name)
	fmt.Printf("  Host:    %s\n", p.Host)
	fmt.Printf("  Account: %s\n", me.Account.Name)
	fmt.Printf("  User:    %s\n", me.User.Email)
	if me.Token.ExpiresAt != "" {
		fmt.Printf("  Token:   expires %s\n", me.Token.ExpiresAt)
	}
	return nil
}

func runList(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("No profiles configured. Run 'deployado auth login' to add one.")
			return nil
		}
		return err
	}
	if len(cfg.Profiles) == 0 {
		fmt.Println("No profiles configured. Run 'deployado auth login' to add one.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "  PROFILE\tACCOUNT\tEMAIL\tHOST")
	for _, n := range config.SortedProfileNames(cfg) {
		marker := "  "
		if n == cfg.Active {
			marker = "* "
		}
		p := cfg.Profiles[n]
		acct := p.AccountName
		if acct == "" {
			acct = "-"
		}
		email := p.Email
		if email == "" {
			email = "-"
		}
		fmt.Fprintf(w, "%s%s\t%s\t%s\t%s\n", marker, n, acct, email, p.Host)
	}
	return w.Flush()
}

func runSwitch(cmd *cobra.Command, args []string) error {
	name := args[0]
	if err := config.SetActive(name); err != nil {
		return err
	}
	color.New(color.FgGreen).Printf("Switched to profile %q.\n", name)
	return nil
}

func runRename(cmd *cobra.Command, args []string) error {
	oldName, newName := args[0], args[1]
	if err := config.RenameProfile(oldName, newName); err != nil {
		return err
	}
	color.New(color.FgGreen).Printf("Renamed profile %q → %q.\n", oldName, newName)
	return nil
}
