package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/timdavies/claudes/internal/daemon"
	"github.com/timdavies/claudes/internal/schedule"
	"github.com/timdavies/claudes/internal/worktree"
)

var (
	taskKind    string
	taskEvery   string
	taskAt      string
	taskName    string
	taskDir     string
	taskPrompt  string
	taskModel   string
	taskPerm    string
	taskWindow  string
	taskProject string
	taskDisable bool
	taskRunID   string
)

var tasksCmd = &cobra.Command{
	Use:   "tasks",
	Short: "Recurring scheduled prompts",
	Long: `Recurring scheduled prompts. A task pairs a prompt with a cadence
(interval/daily/once); the daemon fires due tasks, each spawning an ephemeral
session running 'claude -p' inside its own throwaway git worktree.

Run with no subcommand to list tasks; manage them from the main TUI.`,
	Args: cobra.NoArgs,
	RunE: runTaskLs,
}

var tasksAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add a scheduled prompt",
	Args:  cobra.NoArgs,
	RunE:  runTaskAdd,
}

var tasksLsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List scheduled prompts",
	Args:  cobra.NoArgs,
	RunE:  runTaskLs,
}

var tasksRmCmd = &cobra.Command{
	Use:   "rm <id>",
	Short: "Remove a scheduled prompt and its run history",
	Args:  cobra.ExactArgs(1),
	RunE:  runTaskRm,
}

var tasksEnableCmd = &cobra.Command{
	Use:   "enable <id>",
	Short: "Enable a scheduled prompt",
	Args:  cobra.ExactArgs(1),
	RunE:  func(cmd *cobra.Command, args []string) error { return setTaskEnabled(args[0], true) },
}

var tasksDisableCmd = &cobra.Command{
	Use:   "disable <id>",
	Short: "Disable a scheduled prompt",
	Args:  cobra.ExactArgs(1),
	RunE:  func(cmd *cobra.Command, args []string) error { return setTaskEnabled(args[0], false) },
}

var tasksRunCmd = &cobra.Command{
	Use:   "run <id>",
	Short: "Fire a scheduled prompt now (ignores window/enabled)",
	Args:  cobra.ExactArgs(1),
	RunE:  runTaskRunNow,
}

var tasksLogsCmd = &cobra.Command{
	Use:   "logs <id>",
	Short: "Show run history; --run dumps one run's output",
	Args:  cobra.ExactArgs(1),
	RunE:  runTaskLogs,
}

var tasksEditCmd = &cobra.Command{
	Use:   "edit <id>",
	Short: "Edit a scheduled prompt in place (preserves id + run history)",
	Args:  cobra.ExactArgs(1),
	RunE:  runTaskEdit,
}

func init() {
	tasksAddCmd.Flags().StringVar(&taskKind, "kind", "", "interval | daily | once")
	tasksAddCmd.Flags().StringVar(&taskEvery, "every", "", "interval cadence, e.g. 5m, 2h")
	tasksAddCmd.Flags().StringVar(&taskAt, "at", "", "daily HH:MM, or once datetime (RFC3339 / 'YYYY-MM-DD HH:MM')")
	tasksAddCmd.Flags().StringVar(&taskName, "name", "", "task name")
	tasksAddCmd.Flags().StringVar(&taskDir, "dir", "", "working directory (must be a git repo); default cwd")
	tasksAddCmd.Flags().StringVar(&taskPrompt, "prompt", "", "prompt to run")
	tasksAddCmd.Flags().StringVar(&taskModel, "model", "", "model override")
	tasksAddCmd.Flags().StringVar(&taskPerm, "perm", "", "permission mode (default auto)")
	tasksAddCmd.Flags().StringVar(&taskWindow, "window", "", "active hours for interval, e.g. 9-18")
	tasksAddCmd.Flags().StringVar(&taskProject, "project", "", "project name from config")
	tasksAddCmd.Flags().BoolVar(&taskDisable, "disabled", false, "create disabled")

	tasksLogsCmd.Flags().StringVar(&taskRunID, "run", "", "dump this run's captured output")

	// edit mirrors add's flag surface; only the flags actually passed are
	// applied, so unspecified fields keep their current values. Enable/disable
	// have their own subcommands, so there's no --disabled here.
	tasksEditCmd.Flags().StringVar(&taskKind, "kind", "", "interval | daily | once")
	tasksEditCmd.Flags().StringVar(&taskEvery, "every", "", "interval cadence, e.g. 5m, 2h")
	tasksEditCmd.Flags().StringVar(&taskAt, "at", "", "daily HH:MM, or once datetime (RFC3339 / 'YYYY-MM-DD HH:MM')")
	tasksEditCmd.Flags().StringVar(&taskName, "name", "", "task name")
	tasksEditCmd.Flags().StringVar(&taskDir, "dir", "", "working directory (must be a git repo)")
	tasksEditCmd.Flags().StringVar(&taskPrompt, "prompt", "", "prompt to run")
	tasksEditCmd.Flags().StringVar(&taskModel, "model", "", "model override")
	tasksEditCmd.Flags().StringVar(&taskPerm, "perm", "", "permission mode (default auto)")
	tasksEditCmd.Flags().StringVar(&taskWindow, "window", "", "active hours for interval, e.g. 9-18 (empty clears)")
	tasksEditCmd.Flags().StringVar(&taskProject, "project", "", "project name from config")

	tasksCmd.AddCommand(tasksAddCmd, tasksLsCmd, tasksRmCmd, tasksEnableCmd, tasksDisableCmd, tasksRunCmd, tasksLogsCmd, tasksEditCmd)
	rootCmd.AddCommand(tasksCmd)
}

func scheduleStore() (*schedule.Store, error) { return daemon.ScheduleStore() }

func runTaskAdd(cmd *cobra.Command, args []string) error {
	sc := schedule.Schedule{
		Kind:     schedule.Kind(taskKind),
		Name:     taskName,
		Prompt:   taskPrompt,
		Model:    taskModel,
		PermMode: taskPerm,
		Project:  taskProject,
	}
	if strings.TrimSpace(sc.Name) == "" {
		return fmt.Errorf("--name is required")
	}
	if strings.TrimSpace(sc.Prompt) == "" {
		return fmt.Errorf("--prompt is required")
	}

	dir := taskDir
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		dir = cwd
	}
	repo, err := worktree.RepoRoot(dir)
	if err != nil {
		return fmt.Errorf("--dir must be a git repo: %w", err)
	}
	sc.Dir = repo

	switch sc.Kind {
	case schedule.KindInterval:
		sec, err := schedule.ParseEvery(taskEvery)
		if err != nil {
			return err
		}
		sc.EverySec = sec
		if taskWindow != "" {
			start, end, err := parseWindow(taskWindow)
			if err != nil {
				return err
			}
			sc.StartHour, sc.EndHour = start, end
		}
	case schedule.KindDaily:
		clock, err := schedule.ParseClock(taskAt)
		if err != nil {
			return err
		}
		sc.AtClock = clock
	case schedule.KindOnce:
		if strings.TrimSpace(taskAt) == "" {
			return fmt.Errorf("--at is required for kind once")
		}
		sc.AtTime = taskAt
	default:
		return fmt.Errorf("--kind must be interval, daily, or once")
	}

	store, err := scheduleStore()
	if err != nil {
		return err
	}
	added, err := store.Add(sc)
	if err != nil {
		return err
	}
	if taskDisable {
		_ = store.SetEnabled(added.ID, false)
	} else {
		ensureDaemonForSchedule()
	}
	fmt.Printf("added #%s %s — %s\n", added.ID, added.Name, schedule.Spec(added))
	return nil
}

func runTaskEdit(cmd *cobra.Command, args []string) error {
	store, err := scheduleStore()
	if err != nil {
		return err
	}
	sc, err := store.Get(args[0])
	if err != nil {
		return err
	}

	flags := cmd.Flags()
	if flags.Changed("name") {
		sc.Name = taskName
	}
	if flags.Changed("prompt") {
		sc.Prompt = taskPrompt
	}
	if flags.Changed("model") {
		sc.Model = taskModel
	}
	if flags.Changed("perm") {
		sc.PermMode = taskPerm
	}
	if flags.Changed("project") {
		sc.Project = taskProject
	}
	if flags.Changed("dir") {
		repo, err := worktree.RepoRoot(taskDir)
		if err != nil {
			return fmt.Errorf("--dir must be a git repo: %w", err)
		}
		sc.Dir = repo
	}

	// Switching kind resets the old cadence fields so a daily->interval edit
	// doesn't leave a stale AtClock lying around.
	if flags.Changed("kind") {
		sc.Kind = schedule.Kind(taskKind)
		sc.EverySec, sc.AtClock, sc.AtTime = 0, "", ""
	}
	switch sc.Kind {
	case schedule.KindInterval:
		if flags.Changed("every") {
			sec, err := schedule.ParseEvery(taskEvery)
			if err != nil {
				return err
			}
			sc.EverySec = sec
		} else if flags.Changed("kind") {
			return fmt.Errorf("--every is required when switching to interval")
		}
		if flags.Changed("window") {
			if strings.TrimSpace(taskWindow) == "" {
				sc.StartHour, sc.EndHour = 0, 0
			} else {
				start, end, err := parseWindow(taskWindow)
				if err != nil {
					return err
				}
				sc.StartHour, sc.EndHour = start, end
			}
		}
	case schedule.KindDaily:
		if flags.Changed("at") || flags.Changed("kind") {
			clock, err := schedule.ParseClock(taskAt)
			if err != nil {
				return err
			}
			sc.AtClock = clock
		}
	case schedule.KindOnce:
		if flags.Changed("at") || flags.Changed("kind") {
			if strings.TrimSpace(taskAt) == "" {
				return fmt.Errorf("--at is required for kind once")
			}
			sc.AtTime = taskAt
		}
	default:
		return fmt.Errorf("--kind must be interval, daily, or once")
	}

	if err := store.Update(sc); err != nil {
		return err
	}
	if sc.Enabled {
		ensureDaemonForSchedule()
	}
	fmt.Printf("updated #%s %s — %s\n", sc.ID, sc.Name, schedule.Spec(sc))
	return nil
}

func runTaskLs(cmd *cobra.Command, args []string) error {
	store, err := scheduleStore()
	if err != nil {
		return err
	}
	all := store.All()
	if len(all) == 0 {
		fmt.Println("no scheduled prompts — `claudes tasks add` to create one")
		return nil
	}
	now := time.Now()
	for _, sc := range all {
		state := "enabled"
		if !sc.Enabled {
			state = "disabled"
		}
		next := "—"
		if sc.Enabled {
			next = humanizeNext(sc, now)
		}
		last := ""
		if runs := store.RunsFor(sc.ID); len(runs) > 0 {
			last = "  last: " + string(runs[0].Status)
		}
		fmt.Printf("#%s  %-16s %-18s %-8s %s%s\n", sc.ID, sc.Name, schedule.Spec(sc), state, next, last)
	}
	return nil
}

func runTaskRm(cmd *cobra.Command, args []string) error {
	store, err := scheduleStore()
	if err != nil {
		return err
	}
	if err := store.Remove(args[0]); err != nil {
		return err
	}
	if dir, err := daemon.CacheDir(); err == nil {
		_ = os.RemoveAll(filepath.Join(dir, "schedules", args[0]))
	}
	fmt.Printf("removed #%s\n", args[0])
	return nil
}

func setTaskEnabled(id string, on bool) error {
	store, err := scheduleStore()
	if err != nil {
		return err
	}
	if err := store.SetEnabled(id, on); err != nil {
		return err
	}
	if on {
		ensureDaemonForSchedule()
		fmt.Printf("enabled #%s\n", id)
	} else {
		fmt.Printf("disabled #%s\n", id)
	}
	return nil
}

func runTaskRunNow(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	store, err := scheduleStore()
	if err != nil {
		return err
	}
	ensureDaemonForSchedule()
	if err := daemon.FireNow(cfg, store, args[0]); err != nil {
		return err
	}
	fmt.Printf("fired #%s\n", args[0])
	return nil
}

func runTaskLogs(cmd *cobra.Command, args []string) error {
	store, err := scheduleStore()
	if err != nil {
		return err
	}
	dir, err := daemon.CacheDir()
	if err != nil {
		return err
	}
	if taskRunID != "" {
		run, err := store.GetRun(taskRunID)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(filepath.Join(dir, run.LogFile))
		if err != nil {
			return fmt.Errorf("no log for run %s: %w", taskRunID, err)
		}
		fmt.Print(string(b))
		return nil
	}
	runs := store.RunsFor(args[0])
	if len(runs) == 0 {
		fmt.Println("no runs yet")
		return nil
	}
	for _, r := range runs {
		fmt.Printf("%s  %-11s started %s%s\n", r.ID, r.Status, r.StartedAt, finishedSuffix(r))
	}
	fmt.Printf("\n`claudes tasks logs %s --run <id>` to dump output\n", args[0])
	return nil
}

func finishedSuffix(r schedule.Run) string {
	if r.FinishedAt == "" {
		return ""
	}
	return "  finished " + r.FinishedAt
}

// parseWindow parses "9-18" into start/end hours.
func parseWindow(s string) (int, int, error) {
	parts := strings.SplitN(s, "-", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid window %q (want START-END, e.g. 9-18)", s)
	}
	start, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	end, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil || start < 0 || start > 24 || end < 0 || end > 24 {
		return 0, 0, fmt.Errorf("invalid window %q (hours 0-24)", s)
	}
	return start, end, nil
}

// humanizeNext renders the time until a schedule's next fire ("runs in 3m").
func humanizeNext(sc schedule.Schedule, now time.Time) string {
	next, ok := schedule.NextFire(sc, now)
	if !ok {
		return "—"
	}
	d := next.Sub(now)
	switch {
	case d < time.Minute:
		return "runs in <1m"
	case d < time.Hour:
		return fmt.Sprintf("runs in %dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("runs in %dh%dm", int(d.Hours()), int(d.Minutes())%60)
	}
}
