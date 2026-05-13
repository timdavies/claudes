package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/timdavies/claudes/internal/session"
)

var macuakeCmd = &cobra.Command{
	Use:   "macuake",
	Short: "Manage the macuake terminal integration",
}

var macuakeSyncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Open a macuake tab for every running session that doesn't have one",
	Args:  cobra.NoArgs,
	RunE:  runMacuakeSync,
}

func init() {
	macuakeCmd.AddCommand(macuakeSyncCmd)
	rootCmd.AddCommand(macuakeCmd)
}

func runMacuakeSync(_ *cobra.Command, _ []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if !cfg.Macuake.Enabled {
		return fmt.Errorf("macuake integration is disabled (set [macuake] enabled = true)")
	}
	client := newClient(cfg)
	sessions, err := session.List(client, cfg)
	if err != nil {
		return err
	}
	reg, err := macuakeRegistry()
	if err != nil {
		return err
	}
	existing := reg.All()
	opened := 0
	for _, s := range sessions {
		if _, ok := existing[s.Name]; ok {
			continue
		}
		full := session.FullName(cfg.Prefix, s.Name)
		maybeOpenMacuakeTab(cfg, full, s.Name, s.Dir, client)
		fmt.Println(s.Name)
		opened++
	}
	if opened == 0 {
		fmt.Println("nothing to do — all sessions already have a tab")
	}
	return nil
}
