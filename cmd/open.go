package cmd

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/timdavies/claudes/internal/picker"
	"github.com/timdavies/claudes/internal/session"
)

var openCmd = &cobra.Command{
	Use:   "open [name]",
	Short: "Attach to a session",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		client := newClient(cfg)

		var displayName string
		if len(args) == 1 {
			displayName = args[0]
		} else {
			sessions, err := session.List(client, cfg)
			if err != nil {
				return err
			}
			// Merge in paused pinned agents so the picker can resurrect them.
			if reg, err := pinnedRegistry(); err == nil && reg != nil {
				live := map[string]bool{}
				for _, s := range sessions {
					live[s.Name] = true
				}
				for name, e := range reg.All() {
					if live[name] {
						continue
					}
					sessions = append(sessions, session.Session{
						Name: name, Project: e.Project, Model: e.Model, Dir: e.Dir,
						Status: session.StatusPaused, Pinned: true,
					})
				}
			}
			if len(sessions) == 0 {
				return fmt.Errorf("no sessions; try: claudes new")
			}
			s, err := picker.Pick(sessions)
			if err != nil {
				if errors.Is(err, picker.ErrCancelled) {
					return nil
				}
				return err
			}
			displayName = s.Name
		}
		full := session.FullName(cfg.Prefix, displayName)
		// If the agent is paused-pinned, resurrect it transparently before attach.
		if has, _ := client.Has(full); !has {
			if reg, err := pinnedRegistry(); err == nil && reg.Has(displayName) {
				if err := resurrectPin(client, cfg, displayName, false); err != nil {
					return err
				}
			}
		}
		ensureDaemonForCmd(false)
		return client.Attach(full)
	},
}

func init() {
	rootCmd.AddCommand(openCmd)
}
