package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/timdavies/claudes/internal/session"
)

var (
	statusState string
	statusClear bool
)

var statusCmd = &cobra.Command{
	Use:   "status [activity]",
	Short: "Report what this agent is doing (and its state) to claudes",
	Long: `Self-report the current session's activity and state so they show up in
'claudes ls' and the dashboard, instead of relying on the daemon's guess.

  claudes status "refactoring the daemon loop"          # set the activity line
  claudes status --state blocked "waiting on a review"  # set activity + state
  claudes status --state working                        # set just the state
  claudes status --clear                                # drop the self-report

A self-reported activity overrides the daemon's ambient summary; the daemon
keeps refreshing underneath and reappears once you --clear it.

The state is a free word, but these drive the rail color in 'claudes ls':
  working          → green
  waiting, blocked → yellow
  done, idle       → blue

Targets the session you're running inside (same resolution as 'claudes whoami').`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		name := currentSessionName(cfg)
		if name == "" {
			return fmt.Errorf("not inside a claudes session")
		}
		client := newClient(cfg)
		full := session.FullName(cfg.Prefix, name)

		if statusClear {
			_ = client.UnsetSessionEnv(full, "@claudes-self-description")
			_ = client.UnsetSessionEnv(full, "@claudes-state")
			fmt.Printf("%s: cleared\n", name)
			return nil
		}

		if len(args) == 0 && statusState == "" {
			return fmt.Errorf("nothing to set: pass an activity, --state, or --clear")
		}

		if len(args) == 1 {
			if err := client.SetSessionEnv(full, "@claudes-self-description", args[0]); err != nil {
				return err
			}
		}
		if statusState != "" {
			if err := client.SetSessionEnv(full, "@claudes-state", strings.TrimSpace(statusState)); err != nil {
				return err
			}
		}
		fmt.Println(name)
		return nil
	},
}

func init() {
	statusCmd.Flags().StringVar(&statusState, "state", "", "Short state keyword (working, waiting, blocked, done)")
	statusCmd.Flags().BoolVar(&statusClear, "clear", false, "Clear the self-reported activity and state")
	rootCmd.AddCommand(statusCmd)
}
