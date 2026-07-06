package cmd

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/timdavies/claudes/internal/config"
	"github.com/timdavies/claudes/internal/picker"
	"github.com/timdavies/claudes/internal/pinned"
	"github.com/timdavies/claudes/internal/session"
	"github.com/timdavies/claudes/internal/tmux"
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

		name, err := resolvePausedName(client, cfg, reg, args)
		if err != nil {
			return err
		}
		if name == "" {
			return nil // picker cancelled
		}

		full := session.FullName(cfg.Prefix, name)
		if has, _ := client.Has(full); has {
			return fmt.Errorf("agent %q is already running", name)
		}
		if err := resurrectPin(client, cfg, name, false); err != nil {
			return err
		}
		fmt.Println(name)
		return nil
	},
}

// resolvePausedName picks the paused pinned agent to act on: the explicit name
// argument if given, otherwise a picker over paused-only candidates. Returns an
// empty name (nil error) when the picker is cancelled.
func resolvePausedName(client *tmux.Client, cfg *config.Config, reg *pinned.Registry, args []string) (string, error) {
	if len(args) == 1 {
		return args[0], nil
	}
	liveInfos, err := client.List()
	if err != nil {
		return "", err
	}
	live := map[string]bool{}
	for _, i := range liveInfos {
		live[session.DisplayName(cfg.Prefix, i.Name)] = true
	}
	var paused []session.Session
	for name, entry := range reg.All() {
		if live[name] {
			continue
		}
		paused = append(paused, session.Session{
			Name: name, Project: entry.Project, Model: entry.Model, Dir: entry.Dir,
			Group: entry.Group, Status: session.StatusPaused, Pinned: true,
		})
	}
	if len(paused) == 0 {
		return "", fmt.Errorf("no paused agents to start")
	}
	chosen, err := picker.Pick(paused)
	if err != nil {
		if errors.Is(err, picker.ErrCancelled) {
			return "", nil
		}
		return "", err
	}
	return chosen.Name, nil
}

// resurrectPin spawns a fresh tmux session for a paused pinned agent using
// its saved metadata. Caller is responsible for the not-already-running check.
// Re-stamps the @claudes-pinned marker so 'claudes ls' continues to show 📌.
// openTab is false when the caller will attach in the current terminal (e.g.
// `claudes open`), so resurrecting doesn't spawn a redundant second tab.
func resurrectPin(client *tmux.Client, cfg *config.Config, name string, openTab bool) error {
	reg, err := pinnedRegistry()
	if err != nil {
		return err
	}
	entry, ok := reg.Get(name)
	if !ok {
		return fmt.Errorf("no pinned agent named %q", name)
	}
	resolved := config.Resolved{
		Project:        entry.Project,
		Dir:            entry.Dir,
		Model:          entry.Model,
		PermissionMode: cfg.PermissionMode,
		Group:          entry.Group,
		DefaultArgs:    append([]string(nil), entry.DefaultArgs...),
		Hooks:          cfg.Hooks,
		StopTimeout:    cfg.StopTimeout,
		Prefix:         cfg.Prefix,
		TmuxSocket:     cfg.TmuxSocket,
		TmuxConfig:     cfg.TmuxConfig,
		Models:         cfg.Models,
	}
	// If the project still exists in config, prefer its hooks and permission
	// mode (matches the same merge order as cfg.Resolve at session-create time).
	if p, ok := cfg.Projects[entry.Project]; ok {
		if p.Hooks.PostNew != "" || p.Hooks.PostStop != "" {
			resolved.Hooks = p.Hooks
		}
		if p.PermissionMode != "" {
			resolved.PermissionMode = p.PermissionMode
		}
	}
	if err := spawnSession(client, cfg, resolved, name, entry.PassthroughArgs, openTab); err != nil {
		return err
	}
	full := session.FullName(cfg.Prefix, name)
	_ = client.SetSessionEnv(full, "@claudes-pinned", "true")
	return nil
}

func init() {
	rootCmd.AddCommand(startCmd)
}
