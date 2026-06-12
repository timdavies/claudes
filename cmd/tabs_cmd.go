package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/timdavies/claudes/internal/session"
)

var tabsCmd = &cobra.Command{
	Use:   "tabs",
	Short: "Manage the terminal-tab integration (iterm2)",
}

var tabsSyncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Open a tab for every running session that doesn't have one",
	Args:  cobra.NoArgs,
	RunE:  runTabsSync,
}

func init() {
	tabsCmd.AddCommand(tabsSyncCmd)
	rootCmd.AddCommand(tabsCmd)
}

func runTabsSync(_ *cobra.Command, _ []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if cfg.TabBackend() == "" {
		return fmt.Errorf("tab integration is disabled (set [tabs] backend = \"iterm2\")")
	}
	client := newClient(cfg)
	sessions, err := session.List(client, cfg)
	if err != nil {
		return err
	}
	reg, err := tabRegistryFor(cfg)
	if err != nil {
		return err
	}
	existing := map[string]bool{}
	if reg != nil {
		for name := range reg.All() {
			existing[name] = true
		}
	}
	opened := 0
	for _, s := range sessions {
		if existing[s.Name] {
			continue
		}
		maybeOpenTab(cfg, s.Name, s.Dir)
		fmt.Println(s.Name)
		opened++
	}
	if opened == 0 {
		fmt.Println("nothing to do — all sessions already have a tab")
	}
	return nil
}
