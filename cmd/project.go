package cmd

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/BurntSushi/toml"
	"github.com/spf13/cobra"

	"github.com/timdavies/claudes/internal/config"
)

var projectCmd = &cobra.Command{
	Use:   "project",
	Short: "Manage projects in the config file",
	// Bare `claudes project` prints the list.
	RunE: func(cmd *cobra.Command, args []string) error { return runProjectList(cmd, args) },
}

var projectListCmd = &cobra.Command{
	Use:   "list",
	Short: "List projects",
	Args:  cobra.NoArgs,
	RunE:  runProjectList,
}

var projectShowCmd = &cobra.Command{
	Use:   "show <name>",
	Short: "Show one project's full settings",
	Args:  cobra.ExactArgs(1),
	RunE:  runProjectShow,
}

var (
	projectAddDir         string
	projectAddModel       string
	projectAddDefaultArgs []string
)

var projectAddCmd = &cobra.Command{
	Use:   "add <name>",
	Short: "Add a project to the config file",
	Args:  cobra.ExactArgs(1),
	RunE:  runProjectAdd,
}

var projectRmCmd = &cobra.Command{
	Use:     "rm <name>",
	Aliases: []string{"remove", "delete"},
	Short:   "Remove a project from the config file",
	Args:    cobra.ExactArgs(1),
	RunE:    runProjectRm,
}

func init() {
	projectAddCmd.Flags().StringVarP(&projectAddDir, "dir", "d", "", "Working directory (required)")
	projectAddCmd.Flags().StringVar(&projectAddModel, "model", "", "Override default model for this project")
	projectAddCmd.Flags().StringArrayVar(&projectAddDefaultArgs, "default-arg", nil, "Default arg passed to claude (repeatable)")
	_ = projectAddCmd.MarkFlagRequired("dir")

	projectCmd.AddCommand(projectListCmd)
	projectCmd.AddCommand(projectShowCmd)
	projectCmd.AddCommand(projectAddCmd)
	projectCmd.AddCommand(projectRmCmd)
	rootCmd.AddCommand(projectCmd)
}

func runProjectList(cmd *cobra.Command, _ []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if len(cfg.Projects) == 0 {
		fmt.Fprintln(os.Stderr, "no projects defined; try: claudes project add <name> --dir <path>")
		return nil
	}
	names := make([]string, 0, len(cfg.Projects))
	for n := range cfg.Projects {
		names = append(names, n)
	}
	sort.Strings(names)
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tDIR\tMODEL\tDEFAULT_ARGS")
	for _, n := range names {
		p := cfg.Projects[n]
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", n, dash(p.Dir), dash(p.Model), strings.Join(p.DefaultArgs, " "))
	}
	return w.Flush()
}

func runProjectShow(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	name := args[0]
	p, ok := cfg.Projects[name]
	if !ok {
		return fmt.Errorf("no such project %q", name)
	}
	// Print as a TOML stanza so the user can copy-paste into config.toml.
	enc := toml.NewEncoder(os.Stdout)
	fmt.Printf("[projects.%s]\n", name)
	return enc.Encode(p)
}

func runProjectAdd(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	name := args[0]
	if _, exists := cfg.Projects[name]; exists {
		return fmt.Errorf("project %q already exists; remove it first or edit %s", name, cfg.Path)
	}
	p := config.Project{
		Dir:         projectAddDir,
		Model:       projectAddModel,
		DefaultArgs: projectAddDefaultArgs,
	}
	if cfg.Projects == nil {
		cfg.Projects = map[string]config.Project{}
	}
	cfg.Projects[name] = p
	if err := config.Save(*cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	fmt.Println(name)
	return nil
}

func runProjectRm(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	name := args[0]
	if _, exists := cfg.Projects[name]; !exists {
		return fmt.Errorf("no such project %q", name)
	}
	delete(cfg.Projects, name)
	if err := config.Save(*cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	fmt.Println(name)
	return nil
}
