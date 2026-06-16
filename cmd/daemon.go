package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/timdavies/claudes/internal/daemon"
	"github.com/timdavies/claudes/internal/session"
)

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Manage the background summarizer daemon",
	RunE:  func(cmd *cobra.Command, args []string) error { return runDaemonStatus(cmd, args) },
}

var daemonStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Spawn the daemon if not already running",
	Args:  cobra.NoArgs,
	RunE:  runDaemonStart,
}

var daemonStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the running daemon",
	Args:  cobra.NoArgs,
	RunE:  runDaemonStop,
}

var daemonStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Print whether the daemon is running",
	Args:  cobra.NoArgs,
	RunE:  runDaemonStatus,
}

var daemonRunCmd = &cobra.Command{
	Use:    "run",
	Short:  "Run the daemon loop in the foreground (internal)",
	Args:   cobra.NoArgs,
	Hidden: true,
	RunE:   runDaemonRun,
}

var daemonLogsCmd = &cobra.Command{
	Use:   "logs",
	Short: "Print recent daemon log output",
	Args:  cobra.NoArgs,
	RunE:  runDaemonLogs,
}

func init() {
	daemonCmd.AddCommand(daemonStartCmd)
	daemonCmd.AddCommand(daemonStopCmd)
	daemonCmd.AddCommand(daemonStatusCmd)
	daemonCmd.AddCommand(daemonRunCmd)
	daemonCmd.AddCommand(daemonLogsCmd)
	rootCmd.AddCommand(daemonCmd)
}

func runDaemonStart(_ *cobra.Command, _ []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	client := newClient(cfg)
	sessions, err := session.List(client, cfg)
	if err != nil {
		return err
	}
	// `daemon start` is explicit — spawn even if no sessions exist.
	if err := daemon.Ensure(cfg, sessions, true); err != nil {
		return err
	}
	s, err := daemon.Status()
	if err != nil {
		return err
	}
	fmt.Println(s)
	return nil
}

func runDaemonStop(_ *cobra.Command, _ []string) error {
	if err := daemon.Stop(); err != nil {
		return err
	}
	fmt.Println("stopped")
	return nil
}

func runDaemonStatus(_ *cobra.Command, _ []string) error {
	s, err := daemon.Status()
	if err != nil {
		return err
	}
	fmt.Println(s)
	return nil
}

func runDaemonRun(_ *cobra.Command, _ []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	return daemon.Run(cfg)
}

func runDaemonLogs(_ *cobra.Command, _ []string) error {
	return daemon.TailLog(os.Stdout, 64*1024)
}

// ensureDaemonForCmd is the hook callable from session-mutating commands.
//
// Auto-spawn is intentionally disabled: the daemon's jobs (ambient pane
// summaries, ccusage cost stamping, iTerm2 tab reconcile) are no longer worth
// the per-tick osascript/CPU churn now that agents self-report via
// `claudes status`. The daemon code is kept intact — run `claudes daemon start`
// to bring it back, or restore the body below to re-enable auto-spawn.
func ensureDaemonForCmd(spawnAlways bool) {
	// Disabled — see doc comment. Body preserved for easy re-enable:
	//
	//	cfg, err := loadConfig()
	//	if err != nil {
	//		return
	//	}
	//	client := newClient(cfg)
	//	sessions, err := session.List(client, cfg)
	//	if err != nil {
	//		return
	//	}
	//	_ = daemon.Ensure(cfg, sessions, spawnAlways)
	_ = spawnAlways
}
