package cmd

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/spf13/cobra"

	"github.com/timdavies/claudes/internal/config"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Read and write top-level claudes config",
	RunE:  func(cmd *cobra.Command, args []string) error { return runConfigShow(cmd, args) },
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Print the current config",
	Args:  cobra.NoArgs,
	RunE:  runConfigShow,
}

var configGetCmd = &cobra.Command{
	Use:   "get <key>",
	Short: "Print one top-level scalar key",
	Args:  cobra.ExactArgs(1),
	RunE:  runConfigGet,
}

var configSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Set one top-level scalar key and write the config",
	Args:  cobra.ExactArgs(2),
	RunE:  runConfigSet,
}

func init() {
	configCmd.AddCommand(configShowCmd)
	configCmd.AddCommand(configGetCmd)
	configCmd.AddCommand(configSetCmd)
	rootCmd.AddCommand(configCmd)
}

func runConfigShow(_ *cobra.Command, _ []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "# config: %s\n", cfg.Path)
	enc := toml.NewEncoder(os.Stdout)
	return enc.Encode(cfg)
}

// configScalars is the set of top-level keys editable via `config set`.
// Maps key → getter / setter on a *Config.
var configScalars = map[string]struct {
	get func(*config.Config) string
	set func(*config.Config, string) error
}{
	"model": {
		get: func(c *config.Config) string { return c.Model },
		set: func(c *config.Config, v string) error { c.Model = v; return nil },
	},
	"default_project": {
		get: func(c *config.Config) string { return c.DefaultProject },
		set: func(c *config.Config, v string) error { c.DefaultProject = v; return nil },
	},
	"prefix": {
		get: func(c *config.Config) string { return c.Prefix },
		set: func(c *config.Config, v string) error { c.Prefix = v; return nil },
	},
	"tmux_socket": {
		get: func(c *config.Config) string { return c.TmuxSocket },
		set: func(c *config.Config, v string) error { c.TmuxSocket = v; return nil },
	},
	"tmux_config": {
		get: func(c *config.Config) string { return c.TmuxConfig },
		set: func(c *config.Config, v string) error { c.TmuxConfig = v; return nil },
	},
	"stop_timeout": {
		get: func(c *config.Config) string { return strconv.Itoa(c.StopTimeout) },
		set: func(c *config.Config, v string) error {
			n, err := strconv.Atoi(v)
			if err != nil {
				return fmt.Errorf("stop_timeout must be an integer: %w", err)
			}
			c.StopTimeout = n
			return nil
		},
	},
}

func runConfigGet(_ *cobra.Command, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	key := args[0]
	s, ok := configScalars[key]
	if !ok {
		return fmt.Errorf("unknown key %q (try one of: %s)", key, configKeys())
	}
	fmt.Println(s.get(cfg))
	return nil
}

func runConfigSet(_ *cobra.Command, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	key, val := args[0], args[1]
	s, ok := configScalars[key]
	if !ok {
		return fmt.Errorf("unknown key %q (try one of: %s)", key, configKeys())
	}
	if err := s.set(cfg, val); err != nil {
		return err
	}
	if err := config.Save(*cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	fmt.Println(val)
	return nil
}

func configKeys() string {
	keys := make([]string, 0, len(configScalars))
	for k := range configScalars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}
