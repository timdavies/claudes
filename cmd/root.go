package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/timdavies/claudes/internal/config"
	"github.com/timdavies/claudes/internal/tmux"
)

var (
	flagConfig        string
	flagNoInteractive bool
)

var rootCmd = &cobra.Command{
	Use:   "claudes",
	Short: "Manage Claude Code sessions like screen.",
	RunE: func(cmd *cobra.Command, args []string) error {
		// No subcommand → ls
		return runLs(cmd, args)
	},
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	rootCmd.PersistentFlags().StringVar(&flagConfig, "config", "", "Path to config file")
	rootCmd.PersistentFlags().BoolVar(&flagNoInteractive, "no-interactive", false, "Disable interactive picker")
	rootCmd.CompletionOptions.HiddenDefaultCmd = true
}

func Execute() error {
	return rootCmd.Execute()
}

func loadConfig() (*config.Config, error) {
	cfg, err := config.Load(flagConfig)
	if err != nil {
		return nil, err
	}
	if flagNoInteractive {
		os.Setenv("CLAUDES_NO_INTERACTIVE", "1")
	}
	return &cfg, nil
}

func newClient(cfg *config.Config) *tmux.Client {
	return tmux.New(cfg.TmuxSocket, cfg.TmuxConfig)
}

// printErr is unused for now but kept to centralize stderr formatting if needed.
var _ = fmt.Errorf
