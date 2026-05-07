package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/timdavies/claudes/internal/config"
	"github.com/timdavies/claudes/internal/session"
	"github.com/timdavies/claudes/internal/tmux"
)

var sendKeysMode bool

var sendCmd = &cobra.Command{
	Use:     "write [name] <message|key...>",
	Aliases: []string{"send"},
	Short:   "Write a message into a session (or raw keys with --keys)",
	Long: `Write a message to a session and press Enter.

With --keys, each remaining argument is passed to tmux as a key name (e.g.
Escape, Up, C-c, BSpace) or literal token, and no Enter is appended.

Aliased as "send" for backwards compatibility.`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		client := newClient(cfg)

		if sendKeysMode {
			return runSendKeys(client, cfg, args)
		}
		return runSendMessage(client, cfg, args)
	},
}

func runSendMessage(client *tmux.Client, cfg *config.Config, args []string) error {
	if len(args) > 2 {
		return fmt.Errorf("expected [name] <message>; got %d args", len(args))
	}
	var name, message string
	if len(args) == 2 {
		name, message = args[0], args[1]
	} else {
		message = args[0]
		s, err := pickSession(client, cfg, nil)
		if err != nil {
			return err
		}
		if s == nil {
			return nil
		}
		name = s.Name
	}
	full := session.FullName(cfg.Prefix, name)
	if err := client.SendKeys(full, message); err != nil {
		return err
	}
	fmt.Println(name)
	return nil
}

func runSendKeys(client *tmux.Client, cfg *config.Config, args []string) error {
	// First arg may be a session name; use the picker if it's not a known session.
	sessions, err := session.List(client, cfg)
	if err != nil {
		return err
	}
	known := map[string]bool{}
	for _, s := range sessions {
		known[s.Name] = true
	}

	var name string
	var keys []string
	if known[args[0]] {
		name = args[0]
		keys = args[1:]
	} else {
		s, err := pickSession(client, cfg, nil)
		if err != nil {
			return err
		}
		if s == nil {
			return nil
		}
		name = s.Name
		keys = args
	}
	if len(keys) == 0 {
		return fmt.Errorf("no keys to send")
	}
	full := session.FullName(cfg.Prefix, name)
	if err := client.SendRawKeys(full, keys); err != nil {
		return err
	}
	fmt.Println(name)
	return nil
}

func init() {
	sendCmd.Flags().BoolVar(&sendKeysMode, "keys", false, "Send raw tmux keys (no auto-Enter)")
	rootCmd.AddCommand(sendCmd)
}
