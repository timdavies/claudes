package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/timdavies/claudes/internal/session"
)

var lsProject string

var lsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List managed sessions",
	Args:  cobra.NoArgs,
	RunE:  runLs,
}

func init() {
	lsCmd.Flags().StringVar(&lsProject, "project", "", "Filter by project name")
	rootCmd.AddCommand(lsCmd)
}

func runLs(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	client := newClient(cfg)
	sessions, err := session.List(client, cfg)
	if err != nil {
		return err
	}
	if lsProject != "" {
		filtered := sessions[:0]
		for _, s := range sessions {
			if s.Project == lsProject {
				filtered = append(filtered, s)
			}
		}
		sessions = filtered
	}
	if len(sessions) == 0 {
		// Bare `claudes` with no sessions → show full help instead of an empty list.
		// Explicit `claudes ls` stays silent (scriptable).
		if cmd.Use == "claudes" {
			return cmd.Help()
		}
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tPROJECT\tMODEL\tDIR\tSTATUS")
	for _, s := range sessions {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			s.Name, dash(s.Project), dash(s.Model), s.Dir, s.Status)
	}
	return w.Flush()
}

func dash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
