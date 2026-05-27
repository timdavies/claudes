package cmd

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/timdavies/claudes/internal/config"
	"github.com/timdavies/claudes/internal/macuake"
	"github.com/timdavies/claudes/internal/session"
	"github.com/timdavies/claudes/internal/tmux"
)

var tuiCmd = &cobra.Command{
	Use:   "tui",
	Short: "Interactive agent dashboard",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runTUI()
	},
}

func init() {
	rootCmd.AddCommand(tuiCmd)
}

// rootRunInteractive is called from rootCmd.RunE when bare `claudes` is run
// on a TTY. On a non-TTY (or with --no-interactive), falls through to ls.
func rootRunInteractive(cmd *cobra.Command, args []string) error {
	if !term.IsTerminal(int(os.Stdin.Fd())) || os.Getenv("CLAUDES_NO_INTERACTIVE") != "" {
		return runLs(cmd, args)
	}
	return runTUI()
}

func runTUI() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	client := newClient(cfg)
	ensureDaemonForCmd(false)

	m := tuiModel{cfg: cfg, client: client}
	m.rows = loadTUIRows(client, cfg)

	p := tea.NewProgram(m, tea.WithAltScreen())
	final, err := p.Run()
	if err != nil {
		return err
	}
	fm := final.(tuiModel)
	switch fm.exitAction {
	case exitAttach:
		// Replace this process with tmux attach. Returns only on error.
		return client.Attach(session.FullName(cfg.Prefix, fm.exitName))
	}
	return nil
}

type exitKind int

const (
	exitNone exitKind = iota
	exitAttach
)

type tuiModel struct {
	cfg    *config.Config
	client *tmux.Client

	rows   []tuiRow
	cursor int
	status string // transient one-liner

	exitAction exitKind
	exitName   string
}

type tuiRow struct {
	Name        string
	Project     string
	Model       string
	Status      session.Status
	Dir         string
	Description string
	Pinned      bool
	HasTab      bool
	TabSID      string
}

type tickMsg time.Time

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m tuiModel) Init() tea.Cmd { return tick() }

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tickMsg:
		m.rows = mergeRows(m.rows, loadTUIRows(m.client, m.cfg), &m.cursor)
		return m, tick()
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.rows)-1 {
				m.cursor++
			}
		case "g", "home":
			m.cursor = 0
		case "G", "end":
			if len(m.rows) > 0 {
				m.cursor = len(m.rows) - 1
			}
		case "enter":
			return m.activate()
		case "r":
			m.rows = loadTUIRows(m.client, m.cfg)
			m.status = "refreshed"
		}
	}
	return m, nil
}

// activate handles Enter: focus the macuake tab, or fall back to tmux attach.
func (m tuiModel) activate() (tea.Model, tea.Cmd) {
	if len(m.rows) == 0 {
		return m, nil
	}
	r := m.rows[m.cursor]
	if r.Status == session.StatusPaused {
		m.status = fmt.Sprintf("%s is paused — `claudes start %s` to resurrect", r.Name, r.Name)
		return m, nil
	}
	mc := macuakeClient(m.cfg)
	if mc != nil {
		if sid := resolveMacuakeTab(mc, r); sid != "" {
			if err := mc.Focus(sid); err == nil {
				m.status = "focused " + r.Name
				m.rows = loadTUIRows(m.client, m.cfg)
				return m, nil
			} else if !macuake.IsNotFound(err) {
				m.status = "focus failed: " + err.Error()
				return m, nil
			}
			// Tab vanished between resolve and focus; fall through.
		}
		full := session.FullName(m.cfg.Prefix, r.Name)
		maybeOpenMacuakeTab(m.cfg, full, r.Name, r.Dir, m.client)
		m.rows = loadTUIRows(m.client, m.cfg)
		m.status = "opened tab for " + r.Name
		return m, nil
	}
	// Macuake disabled: quit and attach directly.
	m.exitAction = exitAttach
	m.exitName = r.Name
	return m, tea.Quit
}

var (
	tuiHeaderStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("8"))
	tuiCursorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("12"))
	tuiPausedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	tuiTabOn       = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Render("●")
	tuiTabOff      = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render("—")
)

func (m tuiModel) View() string {
	if len(m.rows) == 0 {
		return "no sessions — `claudes new` to spawn one\n\nq quit\n"
	}
	// Compute column widths.
	headers := []string{"NAME", "PROJECT", "MODEL", "STATE", "TAB", "DIR"}
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}
	cells := make([][]string, len(m.rows))
	for i, r := range m.rows {
		name := r.Name
		if r.Pinned {
			name += " 📌"
		}
		tab := tuiTabOff
		if r.HasTab {
			tab = tuiTabOn
		}
		cells[i] = []string{
			name,
			dash(r.Project),
			dash(r.Model),
			string(r.Status),
			tab,
			dash(tildify(r.Dir)),
		}
		for j, c := range cells[i] {
			// lipgloss styling is invisible to len(); the tab cell is the
			// only styled one and is a single visible glyph.
			n := visibleWidth(c)
			if n > widths[j] {
				widths[j] = n
			}
		}
	}

	var b strings.Builder
	b.WriteString(tuiHeaderStyle.Render("  " + padRow(headers, widths)))
	b.WriteString("\n")
	for i, row := range cells {
		prefix := "  "
		if i == m.cursor {
			prefix = tuiCursorStyle.Render("▸ ")
		}
		line := padRow(row, widths)
		if m.rows[i].Status == session.StatusPaused {
			line = tuiPausedStyle.Render(line)
		}
		b.WriteString(prefix + line + "\n")
	}
	b.WriteString("\n")
	if m.status != "" {
		b.WriteString(tuiPausedStyle.Render(m.status) + "\n")
	}
	b.WriteString(tuiPausedStyle.Render("↑/↓ move  enter focus tab  r refresh  q quit"))
	b.WriteString("\n")
	return b.String()
}

func padRow(cells []string, widths []int) string {
	parts := make([]string, len(cells))
	for i, c := range cells {
		pad := widths[i] - visibleWidth(c)
		if pad < 0 {
			pad = 0
		}
		parts[i] = c + strings.Repeat(" ", pad)
	}
	return strings.Join(parts, "  ")
}

// visibleWidth approximates display width by stripping ANSI escapes. Good
// enough for our cells (no double-width chars beyond the pin emoji, which
// runewidth handles via lipgloss internally; rough fallback here).
func visibleWidth(s string) int {
	// Strip CSI sequences \x1b[...m.
	out := s
	for {
		i := strings.Index(out, "\x1b[")
		if i < 0 {
			break
		}
		end := strings.IndexByte(out[i:], 'm')
		if end < 0 {
			break
		}
		out = out[:i] + out[i+end+1:]
	}
	return len([]rune(out))
}

// resolveMacuakeTab finds the macuake tab for r, falling back through:
//  1. the registry's session_id (if HasTab) — verified live via mc.List;
//  2. a live tab whose Title matches r.Name (set by SetAppearance on open).
//
// If a live match is found, the registry is updated to point at it so the
// next Enter is a single Focus call. Returns "" when nothing matches.
func resolveMacuakeTab(mc *macuake.Client, r tuiRow) string {
	tabs, err := mc.List()
	if err != nil {
		// Fall back to whatever the registry said — Focus will fail
		// cleanly with IsNotFound if it's stale.
		return r.TabSID
	}
	live := map[string]string{} // session_id → title
	for _, t := range tabs {
		live[t.SessionID] = t.Title
	}
	reg, _ := macuakeRegistry()
	if r.HasTab {
		if _, ok := live[r.TabSID]; ok {
			return r.TabSID
		}
		// Registry entry stale — drop it.
		if reg != nil {
			_ = reg.Delete(r.Name)
		}
	}
	// Title match: SetAppearance is called with displayName at open time.
	for _, t := range tabs {
		if t.Title == r.Name && t.SessionID != "" {
			if reg != nil {
				_ = reg.Set(r.Name, t.SessionID)
			}
			return t.SessionID
		}
	}
	return ""
}

// loadTUIRows builds the row list from tmux + pin registry + macuake registry.
func loadTUIRows(client *tmux.Client, cfg *config.Config) []tuiRow {
	sessions, err := session.List(client, cfg)
	if err != nil {
		return nil
	}
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

	tabs := map[string]string{}
	if reg, err := macuakeRegistry(); err == nil && reg != nil {
		for name, t := range reg.All() {
			tabs[name] = t.SessionID
		}
	}

	rows := make([]tuiRow, len(sessions))
	for i, s := range sessions {
		sid, has := tabs[s.Name]
		rows[i] = tuiRow{
			Name:        s.Name,
			Project:     s.Project,
			Model:       s.Model,
			Status:      s.Status,
			Dir:         s.Dir,
			Description: s.Description,
			Pinned:      s.Pinned,
			HasTab:      has,
			TabSID:      sid,
		}
	}
	return rows
}

// mergeRows replaces rows with fresh, keeping the cursor pointed at the same
// session name when possible (so refresh ticks don't yank focus around).
func mergeRows(old, fresh []tuiRow, cursor *int) []tuiRow {
	if len(old) == 0 || *cursor >= len(old) {
		if *cursor >= len(fresh) {
			*cursor = max(0, len(fresh)-1)
		}
		return fresh
	}
	want := old[*cursor].Name
	for i, r := range fresh {
		if r.Name == want {
			*cursor = i
			return fresh
		}
	}
	if *cursor >= len(fresh) {
		*cursor = max(0, len(fresh)-1)
	}
	return fresh
}
