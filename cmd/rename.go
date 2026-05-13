package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/timdavies/claudes/internal/session"
)

var renameCmd = &cobra.Command{
	Use:   "rename [old] <new>",
	Short: "Rename a session",
	Args:  cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		client := newClient(cfg)

		var pickArgs []string
		var newName string
		if len(args) == 2 {
			pickArgs = args[:1]
			newName = args[1]
		} else {
			newName = args[0]
		}

		target, err := pickSession(client, cfg, pickArgs)
		if err != nil {
			return err
		}
		if target == nil {
			return nil // cancelled
		}
		if newName == target.Name {
			return nil
		}

		oldFull := session.FullName(cfg.Prefix, target.Name)
		newFull := session.FullName(cfg.Prefix, newName)

		exists, err := client.Has(newFull)
		if err != nil {
			return err
		}
		if exists {
			return fmt.Errorf("session %q already exists", newName)
		}
		// Refuse if a paused-pinned agent already owns the new name.
		if reg, err := pinnedRegistry(); err == nil && reg.Has(newName) {
			return fmt.Errorf("pinned agent %q already exists", newName)
		}

		// If the target has no live tmux session, it must be a paused pin —
		// rename only the pinned entry, skip tmux and macuake.
		hasLive, _ := client.Has(oldFull)
		if hasLive {
			if err := client.Rename(oldFull, newFull); err != nil {
				return err
			}
			maybeRenameMacuakeTab(cfg, target.Name, newName)
		}
		if reg, err := pinnedRegistry(); err == nil {
			_ = reg.Rename(target.Name, newName)
		}
		fmt.Println(newName)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(renameCmd)
}
