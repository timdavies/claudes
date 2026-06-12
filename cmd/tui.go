package cmd

import (
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/timdavies/claudes/internal/config"
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

	m := tuiModel{cfg: cfg, client: client, width: terminalWidth()}
	m.rows = loadAgentRows(client, cfg)

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

	rows   []agentRow
	cursor int
	width  int
	height int
	status string // transient one-liner

	exitAction exitKind
	exitName   string
}

type tickMsg time.Time
type rowsMsg []agentRow

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// loadRowsCmd refreshes rows off the event loop. Row-loading now includes an
// AppleScript round-trip to verify tab liveness, so it must not block Update.
func loadRowsCmd(client *tmux.Client, cfg *config.Config) tea.Cmd {
	return func() tea.Msg { return rowsMsg(loadAgentRows(client, cfg)) }
}

func (m tuiModel) Init() tea.Cmd { return tick() }

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tickMsg:
		// Kick off an async refresh; the next tick is scheduled when it lands.
		return m, loadRowsCmd(m.client, m.cfg)
	case rowsMsg:
		m.rows = mergeRows(m.rows, []agentRow(msg), &m.cursor)
		return m, tick()
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
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
			m.status = "refreshed"
			return m, loadRowsCmd(m.client, m.cfg)
		}
	}
	return m, nil
}

// activate handles Enter: focus the session's tab, or fall back to tmux attach.
func (m tuiModel) activate() (tea.Model, tea.Cmd) {
	if len(m.rows) == 0 {
		return m, nil
	}
	r := m.rows[m.cursor]
	if r.Status == session.StatusPaused {
		m.status = fmt.Sprintf("%s is paused — `claudes start %s` to resurrect", r.Name, r.Name)
		return m, nil
	}
	mc, ok := tabClientFor(m.cfg)
	if ok {
		reg, _ := tabRegistryFor(m.cfg)
		if sid := resolveTab(mc, reg, r); sid != "" {
			if err := mc.Focus(sid); err == nil {
				m.status = "focused " + r.Name
				return m, loadRowsCmd(m.client, m.cfg)
			} else if !mc.IsNotFound(err) {
				m.status = "focus failed: " + err.Error()
				return m, nil
			}
			// Tab vanished between resolve and focus; fall through.
		}
		full := session.FullName(m.cfg.Prefix, r.Name)
		maybeOpenTab(m.cfg, full, r.Name, r.Dir, m.client)
		m.status = "opened tab for " + r.Name
		return m, loadRowsCmd(m.client, m.cfg)
	}
	// Tab integration disabled: quit and attach directly.
	m.exitAction = exitAttach
	m.exitName = r.Name
	return m, tea.Quit
}

func (m tuiModel) View() string {
	if len(m.rows) == 0 {
		return "no sessions — `claudes new` to spawn one\n\nq quit\n"
	}
	list := renderAgents(m.rows, m.cursor, m.width)
	footer := cardMeta.Render("↑/↓ move  enter focus tab  r refresh  q quit")
	if m.status != "" {
		footer = cardMeta.Render(m.status) + "\n" + footer
	}
	// Pin the footer to the bottom of the screen: pad the gap between the list
	// and the controls so the controls sit on the last row. Falls back to a
	// simple stacked layout until we've learned the window height.
	if m.height <= 0 {
		return list + "\n" + footer + "\n"
	}
	pad := m.height - lipgloss.Height(list) - lipgloss.Height(footer)
	if pad < 1 {
		pad = 1
	}
	return list + strings.Repeat("\n", pad) + footer
}

// resolveTab finds the backend tab for r, falling back through:
//  1. the registry's session_id (if HasTab) — verified live via mc.List;
//  2. a live tab whose Title matches r.Name (set by SetAppearance + tmux's
//     set-titles on open).
//
// If a live match is found, the registry is updated to point at it so the next
// Enter is a single Focus call. Returns "" when nothing matches. The title match
// is tolerant (substring) because iTerm2 may wrap the name in profile job/host
// templating.
func resolveTab(mc tabClient, reg tabRegistry, r agentRow) string {
	tabs, err := mc.List()
	if err != nil {
		// Fall back to whatever the registry said — Focus will fail
		// cleanly with IsNotFound if it's stale.
		return r.TabSID
	}
	have := map[string]bool{} // live session_ids
	for _, t := range tabs {
		have[t.SessionID] = true
	}
	if r.HasTab {
		if have[r.TabSID] {
			return r.TabSID
		}
		// Registry entry stale — drop it.
		if reg != nil {
			_ = reg.Delete(r.Name)
		}
	}
	for _, t := range tabs {
		if t.SessionID != "" && titleMatches(t.Title, r.Name) {
			if reg != nil {
				_ = reg.Set(r.Name, t.SessionID)
			}
			return t.SessionID
		}
	}
	return ""
}

// titleMatches reports whether a tab title corresponds to the session name.
// Exact match preferred; substring covers iTerm2 names like "name (job)".
func titleMatches(title, name string) bool {
	return title == name || (name != "" && strings.Contains(title, name))
}

// mergeRows replaces rows with fresh, keeping the cursor pointed at the same
// session name when possible (so refresh ticks don't yank focus around).
func mergeRows(old, fresh []agentRow, cursor *int) []agentRow {
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
