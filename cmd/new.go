package cmd

import (
	"bufio"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/timdavies/claudes/internal/config"
	"github.com/timdavies/claudes/internal/hooks"
	"github.com/timdavies/claudes/internal/session"
	"github.com/timdavies/claudes/internal/tmux"
	"github.com/timdavies/claudes/internal/worktree"
)

var (
	newDir     string
	newProject string
	newAttach  bool
	newModel   string
	newSonnet  bool
	newOpus    bool
	newHaiku   bool
	newPin     bool
	newGroup   string
	newNoWT    bool
	newInPlace bool
)

var newCmd = &cobra.Command{
	Use:                "new [name] [-- claude-flags...]",
	Short:              "Create a new session",
	DisableFlagParsing: false,
	Args:               cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		client := newClient(cfg)

		// Split positional args from passthrough at `--`.
		atDash := cmd.ArgsLenAtDash()
		var positional, passthrough []string
		if atDash < 0 {
			positional = args
		} else {
			positional = args[:atDash]
			passthrough = args[atDash:]
		}
		if len(positional) > 1 {
			return fmt.Errorf("at most one name argument allowed")
		}

		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		resolved, err := cfg.Resolve(newDir, newProject, cwd)
		if err != nil {
			return err
		}
		// Precedence: --model wins, then alias flags (--haiku/--sonnet/--opus),
		// then resolved (project or config default).
		switch {
		case newModel != "":
			resolved.Model = newModel
		case newHaiku:
			resolved.Model = resolved.Models["haiku"]
		case newSonnet:
			resolved.Model = resolved.Models["sonnet"]
		case newOpus:
			resolved.Model = resolved.Models["opus"]
		}
		resolved.Group = session.NormalizeGroup(newGroup)

		// Name
		var displayName string
		if len(positional) == 1 {
			displayName = positional[0]
		} else {
			suggested, err := suggestName(client, cfg.Prefix, resolved.Project, resolved.Dir)
			if err != nil {
				return err
			}
			displayName, err = promptName(suggested)
			if err != nil {
				return err
			}
		}
		// Default the agent into its own git worktree (branch + path named for
		// the session), unless opted out, sandboxed, or not in a git repo.
		resolved.Dir = resolveWorktreeDir(resolved.Dir, displayName, newNoWT || newInPlace, resolved.WorktreeCopy)

		full := session.FullName(cfg.Prefix, displayName)

		has, err := client.Has(full)
		if err != nil {
			return err
		}
		if has {
			return fmt.Errorf("session %q already exists", displayName)
		}
		// Refuse if a paused-pinned agent already owns this name; the user
		// has to explicitly start or unpin it first.
		pinReg, _ := pinnedRegistry()
		if pinReg != nil && pinReg.Has(displayName) {
			return fmt.Errorf("agent %q is pinned and paused; use 'claudes start %s' or 'claudes unpin %s'",
				displayName, displayName, displayName)
		}

		if err := spawnSession(client, cfg, resolved, displayName, passthrough, false); err != nil {
			return err
		}

		if newPin {
			if err := pinLiveAgent(client, cfg, displayName, resolved, passthrough); err != nil {
				fmt.Fprintf(os.Stderr, "claudes: pin: %v\n", err)
			}
		}

		fmt.Println(displayName)

		if newAttach {
			return client.Attach(session.FullName(cfg.Prefix, displayName))
		}
		return nil
	},
}

// spawnSession runs the full session-creation pipeline shared by `claudes new`
// and `claudes start`: build cmdline → tmux new-session → daemon ensure →
// wait for claude to boot → open terminal tab → post_new hook. openTab is false
// when the caller will attach in the current terminal (e.g. `claudes open`
// resurrecting a pin), so we don't also spawn a second tab on the same session.
func spawnSession(client *tmux.Client, cfg *config.Config, resolved config.Resolved,
	displayName string, passthrough []string, openTab bool) error {
	full := session.FullName(cfg.Prefix, displayName)

	// Assign the Claude Code session UUID ourselves so the transcript path is
	// deterministic (~/.claude/projects/<encoded-dir>/<uuid>.jsonl). The daemon
	// reads it back to estimate cost. A passthrough --session-id wins, so don't
	// stomp it.
	sessionID := uuidV4()
	if hasFlag(passthrough, "--session-id") || hasFlag(resolved.DefaultArgs, "--session-id") {
		sessionID = ""
	}

	cmdline := []string{"claude"}
	cmdline = append(cmdline, resolved.DefaultArgs...)
	if resolved.Model != "" {
		cmdline = append(cmdline, "--model", resolved.Model)
	}
	if sessionID != "" {
		cmdline = append(cmdline, "--session-id", sessionID)
	}
	cmdline = append(cmdline, passthrough...)

	// A passthrough `--model` (after `--`) overrides the resolved model that
	// claude actually runs with, so stamp the effective one — otherwise the
	// Model column lies for agents spawned that way.
	stampedModel := lastModelFlag(cmdline)
	if stampedModel == "" {
		stampedModel = resolved.Model
	}

	// Stamp the session-level metadata. tmux passes -e KEY=VALUE on
	// new-session into the session env, where show-environment reads it
	// back. claudes ls uses these to populate Project/Model columns, and
	// `claudes pin` reads DEFAULT_ARGS/PASSTHROUGH back so resurrecting
	// the agent later runs with the same cmdline.
	extraEnv := []string{
		"CLAUDES_NAME=" + displayName,
		"CLAUDES_PROJECT=" + resolved.Project,
		"CLAUDES_MODEL=" + stampedModel,
		"CLAUDES_GROUP=" + resolved.Group,
		"CLAUDES_SESSION_ID=" + sessionID,
		"CLAUDES_DIR=" + resolved.Dir,
		"CLAUDES_DEFAULT_ARGS=" + mustJSON(resolved.DefaultArgs),
		"CLAUDES_PASSTHROUGH=" + mustJSON(passthrough),
	}

	if err := client.NewSession(full, resolved.Dir, extraEnv, cmdline); err != nil {
		return err
	}

	ensureDaemonForCmd(true)
	waitForReady(client, full, 30*time.Second)
	if openTab {
		maybeOpenTab(cfg, displayName, resolved.Dir)
	}

	_ = hooks.Run("post_new", resolved.Hooks.PostNew,
		hookEnv(displayName, resolved.Project, resolved.Dir, resolved.Model))
	return nil
}

// resolveWorktreeDir returns the directory the session should run in. By
// default each new agent gets its own git worktree (branch + path named for the
// session, reused across resume) so parallel agents don't fight over one
// checkout. It falls back to dir in place — silently for non-git dirs, with a
// one-line warning when the add fails — and skips entirely when the user opts
// out. We attempt the add rather than pre-checking the sandbox: a sandboxed
// shell can still create the worktree when the path is write-allowlisted (e.g.
// ~/Projects/grow-worktrees), and a real EPERM just degrades to in-place.
func resolveWorktreeDir(dir, name string, optOut bool, copyPaths []string) string {
	if optOut {
		return dir
	}
	repo, err := worktree.RepoRoot(dir)
	if err != nil {
		return dir // not a git repo — run in place
	}
	path := worktree.StablePath(repo, name)
	created, err := worktree.Ensure(repo, name, path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "claudes: worktree setup failed (%v) — running in place\n", err)
		return dir
	}
	// Carry over per-project personal-context files (CLAUDE.local.md, .env, …)
	// that git worktree add doesn't bring. Only on creation; copy failures are
	// non-fatal — the worktree is still usable.
	if created && len(copyPaths) > 0 {
		if err := worktree.CopyInto(repo, path, copyPaths); err != nil {
			fmt.Fprintf(os.Stderr, "claudes: worktree_copy partial (%v)\n", err)
		}
	}
	return path
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// uuidV4 returns a random RFC-4122 v4 UUID. Used to pin the Claude Code session
// id at spawn so its transcript path is known. Falls back to the empty string
// on the (vanishingly unlikely) rand failure, which just disables cost tracking
// for that session rather than aborting the spawn.
func uuidV4() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// hasFlag reports whether args contains flag, either bare or as flag=value.
func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag || strings.HasPrefix(a, flag+"=") {
			return true
		}
	}
	return false
}

// lastModelFlag returns the model claude will actually use given a cmdline,
// mirroring claude's "last --model wins" semantics across the resolved model
// and any passthrough override. Returns "" if no model flag is present.
func lastModelFlag(cmdline []string) string {
	model := ""
	for i, a := range cmdline {
		switch {
		case a == "--model" && i+1 < len(cmdline):
			model = cmdline[i+1]
		case strings.HasPrefix(a, "--model="):
			model = strings.TrimPrefix(a, "--model=")
		case a == "--haiku":
			model = "haiku"
		case a == "--sonnet":
			model = "sonnet"
		case a == "--opus":
			model = "opus"
		}
	}
	return model
}

func init() {
	newCmd.Flags().StringVarP(&newDir, "dir", "d", "", "Working directory")
	newCmd.Flags().StringVar(&newProject, "project", "", "Project name from config")
	newCmd.Flags().BoolVarP(&newAttach, "attach", "a", false, "Attach immediately")
	newCmd.Flags().StringVar(&newModel, "model", "", "Model name (passes --model to claude)")
	newCmd.Flags().BoolVar(&newHaiku, "haiku", false, "Use haiku model")
	newCmd.Flags().BoolVar(&newSonnet, "sonnet", false, "Use sonnet model")
	newCmd.Flags().BoolVar(&newOpus, "opus", false, "Use opus model")
	newCmd.Flags().BoolVar(&newPin, "pin", false, "Pin the agent — survives claude exit, resurrect with 'claudes start'")
	newCmd.Flags().StringVar(&newGroup, "group", "", "Group the agent belongs to in 'claudes ls' (default group if empty)")
	newCmd.Flags().BoolVar(&newNoWT, "no-worktree", false, "Run in the checkout itself instead of a per-agent git worktree")
	newCmd.Flags().BoolVar(&newInPlace, "in-place", false, "Alias for --no-worktree")
	rootCmd.AddCommand(newCmd)
}

// waitForReady polls the pane until Claude Code's prompt indicator (❯) shows
// up — i.e. the TUI has finished booting and is accepting input. Returns
// silently on timeout: the session still exists; callers proceed best-effort.
func waitForReady(client *tmux.Client, full string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := client.CapturePane(full, 30)
		if err == nil && strings.Contains(out, "❯") {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// suggestName returns the next available "<base>-N" name. base is the project
// name when resolved, else the working directory basename.
func suggestName(client *tmux.Client, prefix, project, dir string) (string, error) {
	infos, err := client.List()
	if err != nil {
		return "", err
	}
	used := map[string]bool{}
	for _, i := range infos {
		used[i.Name] = true
	}
	// Also avoid clashing with paused-pinned agents.
	if pinReg, err := pinnedRegistry(); err == nil && pinReg != nil {
		for name := range pinReg.All() {
			used[prefix+name] = true
		}
	}
	base := strings.TrimSpace(project)
	if base == "" {
		base = filepath.Base(dir)
	}
	base = strings.Trim(base, "-./ ")
	// Avoid colliding with the configured tmux prefix (e.g. base="claudes" + prefix="claudes-").
	if base == "" || base == strings.TrimRight(prefix, "-") {
		base = "claude"
	}
	for i := 1; i < 10000; i++ {
		c := fmt.Sprintf("%s-%d", base, i)
		if !used[prefix+c] {
			return c, nil
		}
	}
	return "", fmt.Errorf("could not find an unused name")
}

// promptName asks the user for a name, defaulting to suggested. Empty input or
// a non-TTY uses the suggestion.
func promptName(suggested string) (string, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) || os.Getenv("CLAUDES_NO_INTERACTIVE") != "" {
		return suggested, nil
	}
	fmt.Fprintf(os.Stderr, "Workspace name [%s]: ", suggested)
	r := bufio.NewReader(os.Stdin)
	line, err := r.ReadString('\n')
	if err != nil && line == "" {
		return suggested, nil
	}
	name := strings.TrimSpace(line)
	if name == "" {
		return suggested, nil
	}
	return name, nil
}

func hookEnv(name, project, dir, model string) map[string]string {
	return map[string]string{
		"CLAUDES_NAME":    name,
		"CLAUDES_PROJECT": project,
		"CLAUDES_DIR":     dir,
		"CLAUDES_MODEL":   model,
	}
}
