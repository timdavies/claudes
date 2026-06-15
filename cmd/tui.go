package cmd

import (
	"fmt"
	"os"
	"os/exec"
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

// tuiMode is the dashboard's current interaction mode: the list, the
// kill-confirmation prompt, or the new-agent form overlay.
type tuiMode int

const (
	modeList tuiMode = iota
	modeConfirmKill
	modeNew
)

type tuiModel struct {
	cfg    *config.Config
	client *tmux.Client

	rows   []agentRow
	cursor int
	col    int // selected cell in the cursor row: 0 = agent, 1 = its PR
	width  int
	height int
	status string // transient one-liner

	mode       tuiMode
	confirmRow agentRow // row pending kill confirmation (mode == modeConfirmKill)
	form       *newForm // active new-agent form (mode == modeNew)

	exitAction exitKind
	exitName   string
}

type tickMsg time.Time
type rowsMsg []agentRow
type killedMsg struct {
	name string
	err  error
}
type spawnedMsg struct {
	name string
	err  error
}
type openedURLMsg struct {
	url string
	err error
}

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
	case killedMsg:
		if msg.err != nil {
			m.status = fmt.Sprintf("kill %s: %v", msg.name, msg.err)
		} else {
			m.status = "killed " + msg.name
		}
		return m, loadRowsCmd(m.client, m.cfg)
	case spawnedMsg:
		if msg.err != nil {
			m.status = fmt.Sprintf("new %s: %v", msg.name, msg.err)
		} else {
			m.status = "spawned " + msg.name
		}
		return m, loadRowsCmd(m.client, m.cfg)
	case openedURLMsg:
		if msg.err != nil {
			m.status = fmt.Sprintf("open failed: %v", msg.err)
		} else {
			m.status = "opened " + msg.url
		}
		return m, nil
	case tea.KeyMsg:
		switch m.mode {
		case modeConfirmKill:
			return m.updateConfirmKill(msg)
		case modeNew:
			return m.updateNewForm(msg)
		default:
			return m.updateList(msg)
		}
	}
	return m, nil
}

func (m tuiModel) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc", "ctrl+c":
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
			m.col = 0
		}
	case "down", "j":
		if m.cursor < len(m.rows)-1 {
			m.cursor++
			m.col = 0
		}
	case "right", "l":
		// Step onto the PR cell, but only when this row actually has one.
		if m.cursor >= 0 && m.cursor < len(m.rows) && m.rows[m.cursor].PR != "" {
			m.col = 1
		}
	case "left", "h":
		m.col = 0
	case "g", "home":
		m.cursor = 0
		m.col = 0
	case "G", "end":
		if len(m.rows) > 0 {
			m.cursor = len(m.rows) - 1
			m.col = 0
		}
	case "enter":
		return m.activate()
	case "x":
		if m.cursor >= 0 && m.cursor < len(m.rows) {
			m.confirmRow = m.rows[m.cursor]
			m.mode = modeConfirmKill
			m.status = ""
		}
	case "n":
		m.form = newNewForm(m.cfg, m.rows)
		m.mode = modeNew
		m.status = ""
	case "r":
		m.status = "refreshed"
		return m, loadRowsCmd(m.client, m.cfg)
	}
	return m, nil
}

// updateConfirmKill handles the y/n prompt shown after pressing x on a row.
func (m tuiModel) updateConfirmKill(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y", "enter":
		row := m.confirmRow
		m.mode = modeList
		// A paused (pinned, no live tmux) agent is already dead — "kill" just
		// drops it from the pin registry. A live agent gets a graceful stop,
		// run async so the up-to-stop_timeout wait doesn't freeze the UI.
		if row.Status == session.StatusPaused {
			if reg, err := pinnedRegistry(); err == nil && reg != nil {
				_ = reg.Delete(row.Name)
			}
			m.status = "unpinned " + row.Name
			return m, loadRowsCmd(m.client, m.cfg)
		}
		m.status = "killing " + row.Name + "…"
		return m, killSessionCmd(m.client, m.cfg, row)
	case "n", "N", "esc", "ctrl+c", "q":
		m.mode = modeList
		m.status = "kill cancelled"
	}
	return m, nil
}

// killSessionCmd gracefully stops a live session off the event loop.
func killSessionCmd(client *tmux.Client, cfg *config.Config, row agentRow) tea.Cmd {
	return func() tea.Msg {
		target := &session.Session{Name: row.Name, Dir: row.Dir, Project: row.Project, Model: row.Model}
		err := stopResolved(client, cfg, target, false)
		// Drop a pin so a killed pinned agent doesn't linger as paused.
		if reg, rerr := pinnedRegistry(); rerr == nil && reg != nil {
			_ = reg.Delete(row.Name)
		}
		return killedMsg{name: row.Name, err: err}
	}
}

// openURLCmd opens url in the default browser off the event loop.
func openURLCmd(url string) tea.Cmd {
	return func() tea.Msg {
		err := exec.Command("open", url).Start()
		return openedURLMsg{url: url, err: err}
	}
}

// startSessionCmd resurrects a paused pinned agent off the event loop.
func startSessionCmd(cfg *config.Config, row agentRow) tea.Cmd {
	return func() tea.Msg {
		client := newClient(cfg)
		err := resurrectPin(client, cfg, row.Name, true)
		return spawnedMsg{name: row.Name, err: err}
	}
}

// activate handles Enter: focus the session's tab, or fall back to tmux attach.
func (m tuiModel) activate() (tea.Model, tea.Cmd) {
	if len(m.rows) == 0 {
		return m, nil
	}
	r := m.rows[m.cursor]
	// PR cell selected → open it in the browser instead of focusing the tab.
	if m.col == 1 && r.PR != "" {
		m.status = "opening " + prDisplayID(r.PR)
		return m, openURLCmd(r.PR)
	}
	if r.Status == session.StatusPaused {
		m.status = "starting " + r.Name + "…"
		return m, startSessionCmd(m.cfg, r)
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
		maybeOpenTab(m.cfg, r.Name, r.Dir)
		m.status = "opened tab for " + r.Name
		return m, loadRowsCmd(m.client, m.cfg)
	}
	// Tab integration disabled: quit and attach directly.
	m.exitAction = exitAttach
	m.exitName = r.Name
	return m, tea.Quit
}

func (m tuiModel) View() string {
	if m.mode == modeNew && m.form != nil {
		return m.form.view(m.width, m.height)
	}
	if len(m.rows) == 0 {
		return "no sessions — press n to spawn one\n\nn new  q quit\n"
	}
	list := renderAgents(m.rows, m.cursor, m.col, m.width)
	var footer string
	if m.mode == modeConfirmKill {
		footer = confirmStyle.Render(fmt.Sprintf("kill %s? ", m.confirmRow.Name)) +
			cardMeta.Render("y/n")
	} else {
		hint := "↑/↓ move  enter focus  n new  x kill  r refresh  q quit"
		if m.cursor >= 0 && m.cursor < len(m.rows) && m.rows[m.cursor].PR != "" {
			if m.col == 1 {
				hint = "←/→ select  enter open PR  ↑/↓ move  n new  x kill  q quit"
			} else {
				hint = "↑/↓ move  →PR  enter focus  n new  x kill  r refresh  q quit"
			}
		}
		footer = cardMeta.Render(hint)
		if m.status != "" {
			footer = cardMeta.Render(m.status) + "\n" + footer
		}
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
