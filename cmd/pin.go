package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/timdavies/claudes/internal/config"
	"github.com/timdavies/claudes/internal/daemon"
	"github.com/timdavies/claudes/internal/pinned"
	"github.com/timdavies/claudes/internal/session"
	"github.com/timdavies/claudes/internal/tmux"
)

var pinCmd = &cobra.Command{
	Use:   "pin [name]",
	Short: "Pin an agent so it survives claude exit (resurrect with 'claudes start')",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
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
			return nil
		}
		full := session.FullName(cfg.Prefix, target.Name)
		has, _ := client.Has(full)
		if !has {
			return fmt.Errorf("agent %q is not running; pin live agents only", target.Name)
		}
		// Re-read env to capture DefaultArgs/Passthrough that pickSession's
		// synthetic Session may have dropped.
		env, _ := client.SessionEnv(full)
		entry := pinned.Entry{
			Project:         env["CLAUDES_PROJECT"],
			Model:           env["CLAUDES_MODEL"],
			Dir:             env["CLAUDES_DIR"],
			DefaultArgs:     decodeJSONStrings(env["CLAUDES_DEFAULT_ARGS"]),
			PassthroughArgs: decodeJSONStrings(env["CLAUDES_PASSTHROUGH"]),
		}
		reg, err := pinnedRegistry()
		if err != nil {
			return err
		}
		if err := reg.Set(target.Name, entry); err != nil {
			return err
		}
		if err := client.SetSessionEnv(full, "@claudes-pinned", "true"); err != nil {
			fmt.Fprintf(os.Stderr, "claudes: pin: set env: %v\n", err)
		}
		fmt.Println(target.Name)
		return nil
	},
}

var unpinCmd = &cobra.Command{
	Use:   "unpin [name]",
	Short: "Remove the pin (paused entries disappear from 'claudes ls')",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
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
			return nil
		}
		reg, err := pinnedRegistry()
		if err != nil {
			return err
		}
		if !reg.Has(target.Name) {
			return fmt.Errorf("agent %q is not pinned", target.Name)
		}
		if err := reg.Delete(target.Name); err != nil {
			return err
		}
		full := session.FullName(cfg.Prefix, target.Name)
		if has, _ := client.Has(full); has {
			// Best-effort: drop the marker. Failure is non-fatal.
			if err := client.UnsetSessionEnv(full, "@claudes-pinned"); err != nil {
				fmt.Fprintf(os.Stderr, "claudes: unpin: %v\n", err)
			}
		}
		fmt.Println(target.Name)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(pinCmd)
	rootCmd.AddCommand(unpinCmd)
}

// pinnedRegistry returns a Registry rooted at ~/.cache/claudes/pinned.json.
// Cached per process via package-level var to avoid stat thrash.
func pinnedRegistry() (*pinned.Registry, error) {
	dir, err := daemon.CacheDir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return pinned.NewRegistry(filepath.Join(dir, "pinned.json")), nil
}

// pinLiveAgent is called by `claudes new --pin` right after spawnSession
// returns. It uses the in-memory resolved/passthrough values rather than
// re-reading tmux env — both are equivalent at this point.
func pinLiveAgent(client *tmux.Client, cfg *config.Config, displayName string,
	resolved config.Resolved, passthrough []string) error {
	reg, err := pinnedRegistry()
	if err != nil {
		return err
	}
	entry := pinned.Entry{
		Project:         resolved.Project,
		Model:           resolved.Model,
		Dir:             resolved.Dir,
		DefaultArgs:     append([]string(nil), resolved.DefaultArgs...),
		PassthroughArgs: append([]string(nil), passthrough...),
	}
	if err := reg.Set(displayName, entry); err != nil {
		return err
	}
	full := session.FullName(cfg.Prefix, displayName)
	return client.SetSessionEnv(full, "@claudes-pinned", "true")
}

func decodeJSONStrings(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil
	}
	return out
}
