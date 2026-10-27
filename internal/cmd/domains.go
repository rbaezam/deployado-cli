package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/deployado/cli/internal/api"
	"github.com/spf13/cobra"
)

var domainsCmd = &cobra.Command{
	Use:     "domains",
	Aliases: []string{"domain"},
	Short:   "Manage app domains",
	Long:    "Commands for listing and managing custom domains on an app.",
}

var domainsListCmd = &cobra.Command{
	Use:   "list [app-id]",
	Short: "List domains for an app",
	Args:  cobra.RangeArgs(0, 1),
	RunE:  runDomainsList,
}

var domainsListAppID string
var domainsListJSON bool

func init() {
	domainsListCmd.Flags().StringVarP(&domainsListAppID, "app", "a", "", "App ID")
	domainsListCmd.Flags().BoolVar(&domainsListJSON, "json", false, "Output as JSON")

	domainsCmd.AddCommand(domainsListCmd)
	rootCmd.AddCommand(domainsCmd)
}

func runDomainsList(cmd *cobra.Command, args []string) error {
	client, err := getClient()
	if err != nil {
		return err
	}

	appID, err := resolveDomainsAppID(args, domainsListAppID)
	if err != nil {
		return err
	}

	domains, err := client.ListDomains(appID)
	if err != nil {
		return fmt.Errorf("failed to list domains: %w", err)
	}

	app, err := client.GetApp(appID)
	if err != nil {
		return fmt.Errorf("failed to get app: %w", err)
	}

	autoDomain := buildAutoDomain(app.Slug, domains)

	if domainsListJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		payload := map[string]interface{}{
			"domains": domains,
		}
		if autoDomain != "" {
			payload["auto_domain"] = map[string]interface{}{
				"hostname": autoDomain,
			}
		}
		return enc.Encode(payload)
	}

	if len(domains) == 0 && autoDomain == "" {
		fmt.Println("No domains found.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tHOSTNAME\tSTATUS\tPRIMARY\tSOURCE")
	fmt.Fprintln(w, "--\t--------\t------\t-------\t------")

	for _, domain := range domains {
		primary := "no"
		if domain.IsPrimary {
			primary = "yes"
		}

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\tcustom\n", domain.ID, domain.Hostname, domain.Status, primary)
	}

	if autoDomain != "" {
		fmt.Fprintf(w, "-\t%s\tactive\t%s\tauto\n", autoDomain, autoPrimary(domains))
	}

	return w.Flush()
}

func resolveDomainsAppID(args []string, appIDFlag string) (string, error) {
	flagApp := strings.TrimSpace(appIDFlag)
	if flagApp != "" {
		return flagApp, nil
	}

	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		return "", fmt.Errorf("no app id provided. Use [app-id] or --app")
	}

	return strings.TrimSpace(args[0]), nil
}

func buildAutoDomain(slug string, domains []api.Domain) string {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return ""
	}

	hostname := slug + ".deployado.com"
	for _, domain := range domains {
		if strings.EqualFold(strings.TrimSpace(domain.Hostname), hostname) {
			return ""
		}
	}

	return hostname
}

func autoPrimary(domains []api.Domain) string {
	for _, domain := range domains {
		if domain.IsPrimary {
			return "no"
		}
	}
	return "yes"
}
