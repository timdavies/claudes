package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/timdavies/claudes/internal/config"
	"github.com/timdavies/claudes/internal/daemon"
	"github.com/timdavies/claudes/internal/session"
	"github.com/timdavies/claudes/internal/tasks"
	"github.com/timdavies/claudes/internal/tmux"
)

var (
	taskTo      string // --to: assign to a specific agent
	taskProject string // --project
	taskAs      string // --as: override the acting session name
	taskResult  string // --result on complete
	taskNoPoke  bool   // --no-poke: don't message a directed assignee
)

var tasksCmd = &cobra.Command{
	Use:   "tasks",
	Short: "Shared task queue across agents",
	Long: `A cross-agent task queue. Agents (or you) add work; idle agents claim it;
completion reports back to whoever created the task.

Run with no subcommand for an interactive dashboard.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runTasksTUI()
	},
}

var tasksAddCmd = &cobra.Command{
	Use:   "add <title...>",
	Short: "Add a task to the queue",
	Args:  cobra.MinimumNArgs(1),
	RunE:  runTaskAdd,
}

var tasksClaimCmd = &cobra.Command{
	Use:   "claim [id]",
	Short: "Claim a task (the next open one if no id is given)",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runTaskClaim,
}

var tasksCompleteCmd = &cobra.Command{
	Use:     "complete [id]",
	Aliases: []string{"done"},
	Short:   "Mark a task done and report back to its creator",
	Args:    cobra.MaximumNArgs(1),
	RunE:    runTaskComplete,
}

var tasksRmCmd = &cobra.Command{
	Use:     "rm <id>",
	Aliases: []string{"cancel"},
	Short:   "Remove a task from the queue",
	Args:    cobra.ExactArgs(1),
	RunE:    runTaskRm,
}

var tasksShowCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Show a task's full detail",
	Args:  cobra.ExactArgs(1),
	RunE:  runTaskShow,
}

var tasksLsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List tasks (non-interactive)",
	Args:  cobra.NoArgs,
	RunE:  runTaskLs,
}

func init() {
	tasksAddCmd.Flags().StringVar(&taskTo, "to", "", "Assign to a specific agent (otherwise open to anyone)")
	tasksAddCmd.Flags().StringVar(&taskProject, "project", "", "Tag the task with a project")
	tasksAddCmd.Flags().StringVar(&taskAs, "as", "", "Record a different creator than the current session")
	tasksAddCmd.Flags().BoolVar(&taskNoPoke, "no-poke", false, "Don't message a directed assignee")

	tasksClaimCmd.Flags().StringVar(&taskAs, "as", "", "Claim as a different session name")
	tasksCompleteCmd.Flags().StringVar(&taskResult, "result", "", "Result note sent back to the creator")
	tasksCompleteCmd.Flags().StringVar(&taskAs, "as", "", "Complete as a different session name")

	tasksCmd.AddCommand(tasksAddCmd, tasksClaimCmd, tasksCompleteCmd, tasksRmCmd, tasksShowCmd, tasksLsCmd)
	rootCmd.AddCommand(tasksCmd)
}

// taskStore returns the queue rooted at ~/.cache/claudes/tasks.json.
func taskStore() (*tasks.Store, error) {
	dir, err := daemon.CacheDir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return tasks.NewStore(filepath.Join(dir, "tasks.json")), nil
}

// taskActor resolves who is acting: the --as override, else the current
// claudes session, else "" (a human at a plain shell).
func taskActor(cfg *config.Config) string {
	if taskAs != "" {
		return taskAs
	}
	return currentSessionName(cfg)
}

func runTaskAdd(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	store, err := taskStore()
	if err != nil {
		return err
	}
	task, err := store.Add(tasks.Task{
		Title:    strings.Join(args, " "),
		Project:  taskProject,
		Creator:  taskActor(cfg),
		Assignee: taskTo,
	})
	if err != nil {
		return err
	}
	fmt.Printf("added #%s\n", task.ID)

	// Directed tasks poke the assignee so they don't have to poll.
	if task.Assignee != "" && !taskNoPoke {
		pokeAssignee(newClient(cfg), cfg, task)
	}
	return nil
}

func runTaskClaim(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	actor := taskActor(cfg)
	if actor == "" {
		return fmt.Errorf("can't tell who's claiming — run inside a claudes session or pass --as <name>")
	}
	store, err := taskStore()
	if err != nil {
		return err
	}

	var task tasks.Task
	if len(args) == 1 {
		task, err = store.Claim(args[0], actor)
	} else {
		task, err = store.ClaimNext(actor)
	}
	if errors.Is(err, tasks.ErrEmptyQueue) {
		fmt.Println("nothing to claim")
		return nil
	}
	if err != nil {
		return err
	}
	fmt.Printf("claimed #%s: %s\n", task.ID, task.Title)
	return nil
}

func runTaskComplete(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	store, err := taskStore()
	if err != nil {
		return err
	}

	id, err := resolveCompleteID(store, args, taskActor(cfg))
	if err != nil {
		return err
	}
	task, err := store.Complete(id, taskResult)
	if err != nil {
		return err
	}
	fmt.Printf("completed #%s\n", task.ID)

	// Report back to the creating agent (no-op for human creators / self).
	reportCompletion(newClient(cfg), cfg, task)
	return nil
}

// resolveCompleteID returns the id to complete: the explicit arg, or — when
// omitted — the single task this actor has claimed. Ambiguity is an error
// rather than a guess.
func resolveCompleteID(store *tasks.Store, args []string, actor string) (string, error) {
	if len(args) == 1 {
		return args[0], nil
	}
	var mine []tasks.Task
	for _, t := range store.All() {
		if t.Status == tasks.StatusClaimed && t.Claimant == actor {
			mine = append(mine, t)
		}
	}
	switch len(mine) {
	case 0:
		return "", fmt.Errorf("no task claimed by you — pass an id")
	case 1:
		return mine[0].ID, nil
	default:
		ids := make([]string, len(mine))
		for i, t := range mine {
			ids[i] = "#" + t.ID
		}
		return "", fmt.Errorf("you have %d claimed tasks (%s) — pass an id", len(mine), strings.Join(ids, " "))
	}
}

func runTaskRm(cmd *cobra.Command, args []string) error {
	store, err := taskStore()
	if err != nil {
		return err
	}
	if err := store.Remove(args[0]); err != nil {
		return err
	}
	fmt.Printf("removed #%s\n", args[0])
	return nil
}

func runTaskShow(cmd *cobra.Command, args []string) error {
	store, err := taskStore()
	if err != nil {
		return err
	}
	task, err := store.Get(args[0])
	if err != nil {
		return err
	}
	fmt.Print(formatTaskDetail(task))
	return nil
}

func runTaskLs(cmd *cobra.Command, args []string) error {
	store, err := taskStore()
	if err != nil {
		return err
	}
	all := store.All()
	if len(all) == 0 {
		return nil
	}
	fmt.Print(renderTasks(all, -1, terminalWidth()))
	return nil
}

// reportCompletion messages the task's creator that it's done. Best-effort:
// skipped when the creator is a human (no session), is the completer, or the
// session is no longer live.
func reportCompletion(client *tmux.Client, cfg *config.Config, task tasks.Task) {
	if task.Creator == "" || task.Creator == task.Claimant {
		return
	}
	if !sessionLive(client, cfg, task.Creator) {
		return
	}
	msg := fmt.Sprintf("[TASK #%s DONE] %s\n\nCompleted by %s.", task.ID, task.Title, dash(task.Claimant))
	if task.Result != "" {
		msg += "\n\n" + task.Result
	}
	_ = client.SendKeys(session.FullName(cfg.Prefix, task.Creator), msg)
}

// pokeAssignee nudges a directed task's assignee so they can claim without polling.
func pokeAssignee(client *tmux.Client, cfg *config.Config, task tasks.Task) {
	if task.Assignee == task.Creator || !sessionLive(client, cfg, task.Assignee) {
		return
	}
	msg := fmt.Sprintf("[NEW TASK #%s] %s\n\nRun `claudes tasks claim %s` to take it.", task.ID, task.Title, task.ID)
	_ = client.SendKeys(session.FullName(cfg.Prefix, task.Assignee), msg)
}

// sessionLive reports whether a claudes session by that display-name is running.
func sessionLive(client *tmux.Client, cfg *config.Config, name string) bool {
	sessions, err := session.List(client, cfg)
	if err != nil {
		return false
	}
	for _, s := range sessions {
		if s.Name == name {
			return true
		}
	}
	return false
}
