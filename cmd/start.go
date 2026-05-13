package cmd

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/timdavies/claudes/internal/config"
	"github.com/timdavies/claudes/internal/picker"
	"github.com/timdavies/claudes/internal/session"
)

var startCmd = &cobra.Command{
	Use:   "start [name]",
	Short: "Resurrect a paused pinned agent",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		client := newClient(cfg)
		reg, err := pinnedRegistry()
		if err != nil {
			return err
		}

		var name string
		if len(args) == 1 {
			name = args[0]
		} else {
			// Picker over paused-only candidates.
			liveInfos, err := client.List()
			if err != nil {
				return err
			}
			live := map[string]bool{}
			for _, i := range liveInfos {
				live[session.DisplayName(cfg.Prefix, i.Name)] = true
			}
			var paused []session.Session
			for n, e := range reg.All() {
				if live[n] {
					continue
				}
				paused = append(paused, session.Session{
					Name: n, Project: e.Project, Model: e.Model, Dir: e.Dir,
					Status: session.StatusPaused, Pinned: true,
				})
			}
			if len(paused) == 0 {
				return fmt.Errorf("no paused agents to start")
			}
			chosen, err := picker.Pick(paused)
			if err != nil {
				if errors.Is(err, picker.ErrCancelled) {
					return nil
				}
				return err
			}
			name = chosen.Name
		}

		entry, ok := reg.Get(name)
		if !ok {
			return fmt.Errorf("no pinned agent named %q", name)
		}
		full := session.FullName(cfg.Prefix, name)
		if has, _ := client.Has(full); has {
			return fmt.Errorf("agent %q is already running", name)
		}

		resolved := config.Resolved{
			Project:     entry.Project,
			Dir:         entry.Dir,
			Model:       entry.Model,
			DefaultArgs: append([]string(nil), entry.DefaultArgs...),
			Hooks:       cfg.Hooks,
			StopTimeout: cfg.StopTimeout,
			Prefix:      cfg.Prefix,
			TmuxSocket:  cfg.TmuxSocket,
			TmuxConfig:  cfg.TmuxConfig,
			Models:      cfg.Models,
		}
		// If the project still exists in config, prefer its hooks (matches
		// the same merge order as cfg.Resolve at session-create time).
		if p, ok := cfg.Projects[entry.Project]; ok {
			if p.Hooks.PostNew != "" || p.Hooks.PostStop != "" {
				resolved.Hooks = p.Hooks
			}
		}

		if err := spawnSession(client, cfg, resolved, name, entry.PassthroughArgs); err != nil {
			return err
		}
		// Re-stamp the marker on the fresh tmux session.
		_ = client.SetSessionEnv(full, "@claudes-pinned", "true")
		fmt.Println(name)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(startCmd)
}
