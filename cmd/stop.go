package cmd

import (
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/timdavies/claudes/internal/config"
	"github.com/timdavies/claudes/internal/hooks"
	"github.com/timdavies/claudes/internal/picker"
	"github.com/timdavies/claudes/internal/session"
	"github.com/timdavies/claudes/internal/tmux"
)

var stopForce bool

var stopCmd = &cobra.Command{
	Use:   "stop [name]",
	Short: "Stop a session",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runStop(args, stopForce)
	},
}

var killCmd = &cobra.Command{
	Use:   "kill [name]",
	Short: "Stop a session immediately (alias for stop --force)",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runStop(args, true)
	},
}

func init() {
	stopCmd.Flags().BoolVar(&stopForce, "force", false, "Skip graceful shutdown")
	rootCmd.AddCommand(stopCmd)
	rootCmd.AddCommand(killCmd)
}

func runStop(args []string, force bool) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	client := newClient(cfg)

	target, err := pickSession(client, cfg, args)
	if err != nil {
		return err
	}
	if target == nil {
		return nil // user cancelled
	}

	if err := stopResolved(client, cfg, target, force); err != nil {
		return err
	}
	fmt.Println(target.Name)
	return nil
}

// stopResolved stops an already-resolved target — graceful (/exit, wait up to
// stop_timeout) unless force — then closes its tab and runs the post_stop hook.
// Shared by `claudes stop`/`kill` and the TUI's kill action.
func stopResolved(client *tmux.Client, cfg *config.Config, target *session.Session, force bool) error {
	full := session.FullName(cfg.Prefix, target.Name)

	if !force {
		// Graceful: send /exit + Enter, then wait up to stop_timeout
		_ = client.SendKeys(full, "/exit")
		deadline := time.Now().Add(time.Duration(cfg.StopTimeout) * time.Second)
		for time.Now().Before(deadline) {
			has, err := client.Has(full)
			if err != nil {
				return err
			}
			if !has {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	if has, _ := client.Has(full); has {
		if err := client.Kill(full); err != nil {
			return err
		}
	}

	maybeCloseTab(cfg, target.Name)

	resolved, _ := cfg.Resolve("", "", target.Dir)
	// Override resolved.Project with what we observed (may differ from cwd-based resolve)
	if target.Project != "" {
		resolved.Project = target.Project
	}
	_ = hooks.Run("post_stop", resolved.Hooks.PostStop,
		hookEnv(target.Name, target.Project, target.Dir, target.Model))
	return nil
}

// pickSession resolves a session by name (from args) or interactive picker.
func pickSession(client *tmux.Client, cfg *config.Config, args []string) (*session.Session, error) {
	sessions, err := session.List(client, cfg)
	if err != nil {
		return nil, err
	}
	if len(args) == 1 {
		for _, s := range sessions {
			if s.Name == args[0] {
				return &s, nil
			}
		}
		// Accept the name even if not in the listing (e.g. stale state).
		return &session.Session{Name: args[0]}, nil
	}
	if len(sessions) == 0 {
		return nil, fmt.Errorf("no sessions")
	}
	chosen, err := picker.Pick(sessions)
	if err != nil {
		if errors.Is(err, picker.ErrCancelled) {
			return nil, nil
		}
		return nil, err
	}
	return &chosen, nil
}
