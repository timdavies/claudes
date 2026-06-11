package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/timdavies/claudes/internal/session"
)

var resumeAttach bool

// resume is the background twin of `start`/`open`: it brings a paused pinned
// agent back to a live, writable tmux session so `claudes write`/`read` work
// again, but never opens a tab. By default it doesn't attach either — use it to
// wake an agent you want to keep poking from afar without stealing focus. With
// --attach it attaches in the current terminal (still no tab) once it's live.
var resumeCmd = &cobra.Command{
	Use:   "resume [name]",
	Short: "Bring a paused agent live in the background (no tab)",
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
		// openTab is always false: resume never opens an iTerm tab, including
		// under --attach where we attach in the current terminal instead.
		if err := resurrectPin(client, cfg, name, false); err != nil {
			return err
		}
		if resumeAttach {
			ensureDaemonForCmd(false)
			return client.Attach(full)
		}
		fmt.Println(name)
		return nil
	},
}

func init() {
	resumeCmd.Flags().BoolVar(&resumeAttach, "attach", false, "attach in the current terminal once live (no tab)")
	rootCmd.AddCommand(resumeCmd)
}
