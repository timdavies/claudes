package cmd

import (
	"encoding/json"
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
	taskDays    string
	taskJSON    bool
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

var tasksShowCmd = &cobra.Command{
	Use:   "show <id|name>",
	Short: "Print a task's full config, including its prompt",
	Args:  cobra.ExactArgs(1),
	RunE:  runTaskShow,
}

func init() {
	tasksAddCmd.Flags().StringVar(&taskKind, "kind", "", "interval | daily | once")
	tasksAddCmd.Flags().StringVar(&taskEvery, "every", "", "interval cadence, e.g. 5m, 2h")
	tasksAddCmd.Flags().StringVar(&taskAt, "at", "", "daily HH:MM, or once datetime (RFC3339 / 'YYYY-MM-DD HH:MM')")
	tasksAddCmd.Flags().StringVar(&taskDays, "days", "", "restrict daily to weekdays, e.g. mon or mon,thu")
	tasksAddCmd.Flags().StringVar(&taskName, "name", "", "task name")
	tasksAddCmd.Flags().StringVar(&taskDir, "dir", "", "working directory (must be a git repo); default cwd")
	tasksAddCmd.Flags().StringVar(&taskPrompt, "prompt", "", "prompt to run")
	tasksAddCmd.Flags().StringVar(&taskModel, "model", "", "model override")
	tasksAddCmd.Flags().StringVar(&taskPerm, "perm", "", "permission mode (default auto)")
	tasksAddCmd.Flags().StringVar(&taskWindow, "window", "", "active hours for interval, e.g. 9-18")
	tasksAddCmd.Flags().StringVar(&taskProject, "project", "", "project name from config")
	tasksAddCmd.Flags().BoolVar(&taskDisable, "disabled", false, "create disabled")

	tasksLogsCmd.Flags().StringVar(&taskRunID, "run", "", "dump this run's captured output")

	tasksShowCmd.Flags().BoolVar(&taskJSON, "json", false, "emit the task config as JSON")

	// edit mirrors add's flag surface; only the flags actually passed are
	// applied, so unspecified fields keep their current values. Enable/disable
	// have their own subcommands, so there's no --disabled here.
	tasksEditCmd.Flags().StringVar(&taskKind, "kind", "", "interval | daily | once")
	tasksEditCmd.Flags().StringVar(&taskEvery, "every", "", "interval cadence, e.g. 5m, 2h")
	tasksEditCmd.Flags().StringVar(&taskAt, "at", "", "daily HH:MM, or once datetime (RFC3339 / 'YYYY-MM-DD HH:MM')")
	tasksEditCmd.Flags().StringVar(&taskDays, "days", "", "restrict daily to weekdays, e.g. mon or mon,thu (empty clears)")
	tasksEditCmd.Flags().StringVar(&taskName, "name", "", "task name")
	tasksEditCmd.Flags().StringVar(&taskDir, "dir", "", "working directory (must be a git repo)")
	tasksEditCmd.Flags().StringVar(&taskPrompt, "prompt", "", "prompt to run")
	tasksEditCmd.Flags().StringVar(&taskModel, "model", "", "model override")
	tasksEditCmd.Flags().StringVar(&taskPerm, "perm", "", "permission mode (default auto)")
	tasksEditCmd.Flags().StringVar(&taskWindow, "window", "", "active hours for interval, e.g. 9-18 (empty clears)")
	tasksEditCmd.Flags().StringVar(&taskProject, "project", "", "project name from config")

	tasksCmd.AddCommand(tasksAddCmd, tasksLsCmd, tasksRmCmd, tasksEnableCmd, tasksDisableCmd, tasksRunCmd, tasksLogsCmd, tasksEditCmd, tasksShowCmd)
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
		days, err := schedule.ParseDays(taskDays)
		if err != nil {
			return err
		}
		sc.Days = days
	case schedule.KindDaily:
		clock, err := schedule.ParseClock(taskAt)
		if err != nil {
			return err
		}
		sc.AtClock = clock
		days, err := schedule.ParseDays(taskDays)
		if err != nil {
			return err
		}
		sc.Days = days
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
		sc.EverySec, sc.AtClock, sc.AtTime, sc.Days = 0, "", "", nil
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
		if flags.Changed("days") {
			days, err := schedule.ParseDays(taskDays)
			if err != nil {
				return err
			}
			sc.Days = days // empty clears back to every day
		}
	case schedule.KindDaily:
		if flags.Changed("at") || flags.Changed("kind") {
			clock, err := schedule.ParseClock(taskAt)
			if err != nil {
				return err
			}
			sc.AtClock = clock
		}
		if flags.Changed("days") {
			days, err := schedule.ParseDays(taskDays)
			if err != nil {
				return err
			}
			sc.Days = days // empty clears back to every day
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
		total := 0.0
		runs := store.RunsFor(sc.ID)
		if len(runs) > 0 {
			last = "  last: " + string(runs[0].Status)
			if runs[0].Status == schedule.RunAuthFailed {
				last += "  ⚠ /login needed"
			}
		}
		for _, r := range runs {
			total += r.Cost
		}
		// Always show a $ amount ($0.00 when no spend) so the column aligns.
		costStr := "  " + formatUSD(total)
		fmt.Printf("#%s  %-16s %-18s %-8s %s%s%s\n", sc.ID, sc.Name, schedule.Spec(sc), state, next, costStr, last)
	}
	return nil
}

// resolveTask looks a task up by id first, then by exact (case-insensitive)
// name. Name is a convenience for the CLI; ids stay canonical.
func resolveTask(store *schedule.Store, arg string) (schedule.Schedule, error) {
	if sc, err := store.Get(arg); err == nil {
		return sc, nil
	}
	for _, sc := range store.All() {
		if strings.EqualFold(sc.Name, arg) {
			return sc, nil
		}
	}
	return schedule.Schedule{}, fmt.Errorf("no task with id or name %q", arg)
}

func runTaskShow(cmd *cobra.Command, args []string) error {
	store, err := scheduleStore()
	if err != nil {
		return err
	}
	sc, err := resolveTask(store, args[0])
	if err != nil {
		return err
	}
	now := time.Now()
	next := "—"
	if sc.Enabled {
		next = humanizeNext(sc, now)
	}
	var lastRun *schedule.Run
	if runs := store.RunsFor(sc.ID); len(runs) > 0 {
		lastRun = &runs[0]
	}

	if taskJSON {
		payload := map[string]any{
			"schedule":  sc,
			"spec":      schedule.Spec(sc),
			"next_fire": next,
		}
		if lastRun != nil {
			payload["last_run"] = lastRun
		}
		b, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(b))
		return nil
	}

	enabled := "true"
	if !sc.Enabled {
		enabled = "false"
	}
	fmt.Printf("#%s  %s\n", sc.ID, sc.Name)
	fmt.Printf("  kind:      %s\n", sc.Kind)
	fmt.Printf("  cadence:   %s\n", schedule.Spec(sc))
	if sc.Kind == schedule.KindInterval {
		fmt.Printf("  window:    %d–%d\n", sc.StartHour, sc.EndHour)
	}
	if len(sc.Days) > 0 {
		fmt.Printf("  days:      %s\n", schedule.FormatDays(sc.Days))
	}
	fmt.Printf("  dir:       %s\n", orDash(sc.Dir))
	if sc.Project != "" {
		fmt.Printf("  project:   %s\n", sc.Project)
	}
	fmt.Printf("  model:     %s\n", orDefault(sc.Model))
	fmt.Printf("  perm:      %s\n", orDefaultVal(sc.PermMode, "auto"))
	fmt.Printf("  enabled:   %s\n", enabled)
	fmt.Printf("  next fire: %s\n", next)
	if lastRun != nil {
		when := lastRun.StartedAt
		if lastRun.FinishedAt != "" {
			when = lastRun.FinishedAt
		}
		fmt.Printf("  last run:  %s  (%s)\n", lastRun.Status, when)
	} else {
		fmt.Printf("  last run:  —\n")
	}
	fmt.Printf("\nprompt:\n%s\n", sc.Prompt)
	return nil
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func orDefault(s string) string { return orDefaultVal(s, "(default)") }

func orDefaultVal(s, def string) string {
	if s == "" {
		return def
	}
	return s
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
		fmt.Println(sanitizeLog(string(b)))
		return nil
	}
	runs := store.RunsFor(args[0])
	if len(runs) == 0 {
		fmt.Println("no runs yet")
		return nil
	}
	for _, r := range runs {
		meta := ""
		if d := runDuration(r); d != "" {
			meta += "  " + d
		}
		if r.Cost > 0 {
			meta += "  " + formatUSD(r.Cost)
		}
		fmt.Printf("%s  %-11s started %s%s%s\n", r.ID, r.Status, r.StartedAt, finishedSuffix(r), meta)
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

// formatUSD renders a cost like live-agent cost: a bare "$1.23".
func formatUSD(usd float64) string {
	return "$" + strconv.FormatFloat(usd, 'f', 2, 64)
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
