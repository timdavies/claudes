package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/timdavies/claudes/internal/config"
	"github.com/timdavies/claudes/internal/session"
	"github.com/timdavies/claudes/internal/tmux"
)

// groupCmd assigns one or more agents to a group, which is how `claudes ls`
// clusters them. The group lives in the session env (CLAUDES_GROUP) for live
// agents and in the pin registry for paused ones, so it survives both refreshes
// and resurrection. "default" (or empty) drops an agent back to the implicit
// top group.
var groupCmd = &cobra.Command{
	Use:   "group <group> [name...]",
	Short: "Move agents into a group (use 'default' to ungroup)",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		client := newClient(cfg)
		group := session.NormalizeGroup(args[0])

		names := args[1:]
		if len(names) == 0 {
			target, err := pickSession(client, cfg, nil)
			if err != nil {
				return err
			}
			if target == nil {
				return nil // picker cancelled
			}
			names = []string{target.Name}
		}

		for _, name := range names {
			if err := setAgentGroup(client, cfg, name, group); err != nil {
				fmt.Fprintf(os.Stderr, "claudes: group: %s: %v\n", name, err)
				continue
			}
			fmt.Println(name)
		}
		return nil
	},
}

// setAgentGroup writes group onto whichever backing store(s) hold the agent: the
// live session env, the pin registry, or both. It errors if the agent is
// neither live nor pinned. group is already normalized ("" == default).
func setAgentGroup(client *tmux.Client, cfg *config.Config, name, group string) error {
	full := session.FullName(cfg.Prefix, name)
	live, _ := client.Has(full)

	reg, err := pinnedRegistry()
	if err != nil {
		return err
	}
	entry, pinned := reg.Get(name)

	if !live && !pinned {
		return fmt.Errorf("no agent named %q", name)
	}

	if live {
		if err := client.SetSessionEnv(full, "CLAUDES_GROUP", group); err != nil {
			return err
		}
	}
	if pinned {
		entry.Group = group
		if err := reg.Set(name, entry); err != nil {
			return err
		}
	}
	return nil
}

func init() {
	rootCmd.AddCommand(groupCmd)
}
