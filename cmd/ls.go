package cmd

import (
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var lsProject string

var lsCmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list"},
	Short:   "List managed sessions",
	Args:    cobra.NoArgs,
	RunE:    runLs,
}

func init() {
	lsCmd.Flags().StringVar(&lsProject, "project", "", "Filter by project name")
	rootCmd.AddCommand(lsCmd)
}

func runLs(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	client := newClient(cfg)
	rows := loadAgentRows(client, cfg)
	if lsProject != "" {
		filtered := rows[:0]
		for _, r := range rows {
			if r.Project == lsProject {
				filtered = append(filtered, r)
			}
		}
		rows = filtered
	}
	if len(rows) == 0 {
		// Bare `claudes` with no sessions → show full help instead of an empty list.
		// Explicit `claudes ls` stays silent.
		if cmd.Use == "claudes" {
			return cmd.Help()
		}
		return nil
	}
	// Make sure the daemon is up so descriptions stay fresh on the next tick.
	// No-op if it's already running. Best-effort.
	ensureDaemonForCmd(false)

	fmt.Fprint(os.Stdout, renderAgents(rows, -1, 0, terminalWidth()))
	return nil
}

func dash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// tildify replaces a leading $HOME with "~" for compact full-path display
// (used by the new-agent form's directory field and suggestions).
func tildify(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if strings.HasPrefix(p, home+string(os.PathSeparator)) {
		return "~" + p[len(home):]
	}
	return p
}

// shortDir compresses a path to its last two segments so deep working dirs stay
// legible: /Users/me/Projects/grow/.claude/worktrees/cact-4075 renders as
// "worktrees/cact-4075". Paths already two segments or shorter are returned
// whole (with their root stripped, e.g. /tmp → "tmp").
func shortDir(p string) string {
	var segs []string
	for _, seg := range strings.Split(p, string(os.PathSeparator)) {
		if seg != "" {
			segs = append(segs, seg)
		}
	}
	if len(segs) > 2 {
		segs = segs[len(segs)-2:]
	}
	return strings.Join(segs, string(os.PathSeparator))
}

// truncate cuts s to at most n display columns (UTF-8 aware on rune count;
// good enough for ASCII descriptions). Adds an ellipsis when truncated.
// n<=0 returns s unchanged.
func truncate(s string, n int) string {
	if n <= 0 || utf8.RuneCountInString(s) <= n {
		return s
	}
	if n <= 1 {
		return "…"
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

// terminalWidth returns stdout's column count, or a stable fallback when
// stdout isn't a terminal (so piped/scripted output stays deterministic).
func terminalWidth() int {
	const fallback = 80
	fd := int(os.Stdout.Fd())
	if !term.IsTerminal(fd) {
		return fallback
	}
	cols, _, err := term.GetSize(fd)
	if err != nil || cols <= 0 {
		return fallback
	}
	return cols
}
