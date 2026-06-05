package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/timdavies/claudes/internal/config"
	"github.com/timdavies/claudes/internal/session"
)

var whoamiCmd = &cobra.Command{
	Use:   "whoami",
	Short: "Print the name of the claudes session this is running inside",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		if name := currentSessionName(cfg); name != "" {
			fmt.Println(name)
			return nil
		}
		return fmt.Errorf("not inside a claudes session")
	},
}

// currentSessionName resolves the claudes display-name of the session the caller
// is running in. The live tmux session (via $TMUX_PANE) is authoritative and
// survives renames; $CLAUDES_NAME is the fallback for nested-tmux or odd envs
// where the pane id can't be resolved on our socket. We only trust a tmux name
// that carries our prefix, so a non-claudes tmux doesn't masquerade as one.
func currentSessionName(cfg *config.Config) string {
	if pane := os.Getenv("TMUX_PANE"); pane != "" {
		if full, err := newClient(cfg).PaneSessionName(pane); err == nil {
			if cfg.Prefix == "" || strings.HasPrefix(full, cfg.Prefix) {
				return session.DisplayName(cfg.Prefix, full)
			}
		}
	}
	return os.Getenv("CLAUDES_NAME")
}

func init() {
	rootCmd.AddCommand(whoamiCmd)
}
