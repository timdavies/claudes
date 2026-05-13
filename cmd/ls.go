package cmd

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"unicode/utf8"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/timdavies/claudes/internal/session"
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
	sessions, err := session.List(client, cfg)
	if err != nil {
		return err
	}
	// Merge in paused pinned agents (those with no live tmux session).
	if reg, err := pinnedRegistry(); err == nil && reg != nil {
		live := map[string]bool{}
		for _, s := range sessions {
			live[s.Name] = true
		}
		for name, e := range reg.All() {
			if live[name] {
				continue
			}
			sessions = append(sessions, session.Session{
				Name: name, Project: e.Project, Model: e.Model, Dir: e.Dir,
				Status: session.StatusPaused, Pinned: true,
			})
		}
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].Name < sessions[j].Name })
	if lsProject != "" {
		filtered := sessions[:0]
		for _, s := range sessions {
			if s.Project == lsProject {
				filtered = append(filtered, s)
			}
		}
		sessions = filtered
	}
	if len(sessions) == 0 {
		// Bare `claudes` with no sessions → show full help instead of an empty list.
		// Explicit `claudes ls` stays silent (scriptable).
		if cmd.Use == "claudes" {
			return cmd.Help()
		}
		return nil
	}
	// Make sure the daemon is up so descriptions stay fresh on the next tick.
	// No-op if it's already running. Best-effort.
	ensureDaemonForCmd(false)

	descBudget := descriptionBudget(sessions)

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tPROJECT\tMODEL\tSTATUS\tDIR\tDESCRIPTION")
	for _, s := range sessions {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			displayName(s),
			dash(s.Project),
			dash(s.Model),
			s.Status,
			dash(tildify(s.Dir)),
			dash(truncate(s.Description, descBudget)))
	}
	return w.Flush()
}

// displayName renders the NAME column, adding a trailing 📌 when pinned.
func displayName(s session.Session) string {
	if s.Pinned {
		return s.Name + " 📌"
	}
	return s.Name
}

func dash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// tildify replaces a leading $HOME with "~" for compactness.
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

// descriptionBudget computes a sensible truncation cap for the DESCRIPTION
// column, given the terminal width and the widest fixed-column data we have.
// Falls back to 60 when stdout isn't a terminal (so scripted output is
// stable).
func descriptionBudget(ss []session.Session) int {
	const fallback = 60
	fd := int(os.Stdout.Fd())
	if !term.IsTerminal(fd) {
		return fallback
	}
	cols, _, err := term.GetSize(fd)
	if err != nil || cols <= 0 {
		return fallback
	}
	headers := []string{"NAME", "PROJECT", "MODEL", "STATUS", "DIR"}
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = utf8.RuneCountInString(h)
	}
	for _, s := range ss {
		w := []int{
			utf8.RuneCountInString(displayName(s)),
			utf8.RuneCountInString(dash(s.Project)),
			utf8.RuneCountInString(dash(s.Model)),
			utf8.RuneCountInString(string(s.Status)),
			utf8.RuneCountInString(dash(tildify(s.Dir))),
		}
		for i, x := range w {
			if x > widths[i] {
				widths[i] = x
			}
		}
	}
	used := 0
	for _, w := range widths {
		used += w + 2 // tabwriter padding
	}
	budget := cols - used - 1 // leave a column for safety
	if budget < 20 {
		return 20 // floor: prefer truncation over ugly wrapping
	}
	return budget
}
