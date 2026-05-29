package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/timdavies/claudes/internal/session"
)

var tabsCmd = &cobra.Command{
	Use:   "tabs",
	Short: "Manage the terminal-tab integration (iterm2 / macuake)",
}

var tabsSyncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Open a tab for every running session that doesn't have one",
	Args:  cobra.NoArgs,
	RunE:  runTabsSync,
}

// macuakeCmd is the legacy `claudes macuake …` namespace, kept as a hidden alias
// for `claudes tabs …` so existing muscle memory / scripts keep working.
var macuakeCmd = &cobra.Command{
	Use:    "macuake",
	Short:  "Deprecated alias for `tabs`",
	Hidden: true,
}

func init() {
	tabsCmd.AddCommand(tabsSyncCmd)
	rootCmd.AddCommand(tabsCmd)

	macuakeSync := *tabsSyncCmd
	macuakeCmd.AddCommand(&macuakeSync)
	rootCmd.AddCommand(macuakeCmd)
}

func runTabsSync(_ *cobra.Command, _ []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if cfg.TabBackend() == "" {
		return fmt.Errorf("tab integration is disabled (set [tabs] backend = \"iterm2\" or \"macuake\")")
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
		full := session.FullName(cfg.Prefix, s.Name)
		maybeOpenTab(cfg, full, s.Name, s.Dir, client)
		fmt.Println(s.Name)
		opened++
	}
	if opened == 0 {
		fmt.Println("nothing to do — all sessions already have a tab")
	}
	return nil
}
