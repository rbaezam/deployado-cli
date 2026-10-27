package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var projectsCmd = &cobra.Command{
	Use:     "projects",
	Aliases: []string{"project"},
	Short:   "Manage projects",
	Long:    "Commands for listing and inspecting projects.",
}

var projectsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all projects",
	RunE:  runProjectsList,
}

var projectsListJSON bool

func init() {
	projectsListCmd.Flags().BoolVar(&projectsListJSON, "json", false, "Output as JSON")

	projectsCmd.AddCommand(projectsListCmd)
	rootCmd.AddCommand(projectsCmd)
}

func runProjectsList(cmd *cobra.Command, args []string) error {
	client, err := getClient()
	if err != nil {
		return err
	}

	projects, err := client.ListProjects()
	if err != nil {
		return fmt.Errorf("failed to list projects: %w", err)
	}

	if projectsListJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]interface{}{
			"projects": projects,
		})
	}

	if len(projects) == 0 {
		fmt.Println("No projects found.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tSLUG\tDESCRIPTION")
	fmt.Fprintln(w, "--\t----\t----\t-----------")

	for _, project := range projects {
		description := project.Description
		if description == "" {
			description = "-"
		}

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", project.ID, project.Name, project.Slug, description)
	}

	return w.Flush()
}
