package cmd

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/timdavies/claudes/internal/config"
	"github.com/timdavies/claudes/internal/tasks"
	"github.com/timdavies/claudes/internal/tmux"
)

func runTasksTUI() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	store, err := taskStore()
	if err != nil {
		return err
	}
	m := tasksModel{
		cfg:    cfg,
		client: newClient(cfg),
		store:  store,
		actor:  taskActor(cfg),
		width:  terminalWidth(),
		rows:   store.All(),
	}
	_, err = tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

type tasksModel struct {
	cfg    *config.Config
	client *tmux.Client
	store  *tasks.Store
	actor  string

	rows   []tasks.Task
	cursor int
	width  int
	status string
}

type tasksTickMsg time.Time
type tasksRowsMsg []tasks.Task

func tasksTick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tasksTickMsg(t) })
}

func (m tasksModel) loadCmd() tea.Cmd {
	store := m.store
	return func() tea.Msg { return tasksRowsMsg(store.All()) }
}

func (m tasksModel) Init() tea.Cmd { return tasksTick() }

func (m tasksModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tasksTickMsg:
		return m, m.loadCmd()
	case tasksRowsMsg:
		m.rows = mergeTaskRows(m.rows, []tasks.Task(msg), &m.cursor)
		return m, tasksTick()
	case tea.WindowSizeMsg:
		m.width = msg.Width
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
		case "c":
			return m.claim()
		case "x", "enter":
			return m.complete()
		case "d":
			return m.remove()
		case "r":
			m.status = "refreshed"
			return m, m.loadCmd()
		}
	}
	return m, nil
}

func (m tasksModel) current() (tasks.Task, bool) {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return tasks.Task{}, false
	}
	return m.rows[m.cursor], true
}

func (m tasksModel) claim() (tea.Model, tea.Cmd) {
	t, ok := m.current()
	if !ok {
		return m, nil
	}
	if m.actor == "" {
		m.status = "can't claim from a plain shell — open the TUI inside a session"
		return m, nil
	}
	if _, err := m.store.Claim(t.ID, m.actor); err != nil {
		m.status = fmt.Sprintf("claim #%s: %v", t.ID, err)
		return m, nil
	}
	m.status = fmt.Sprintf("claimed #%s", t.ID)
	return m, m.loadCmd()
}

func (m tasksModel) complete() (tea.Model, tea.Cmd) {
	t, ok := m.current()
	if !ok {
		return m, nil
	}
	done, err := m.store.Complete(t.ID, "")
	if err != nil {
		m.status = fmt.Sprintf("complete #%s: %v", t.ID, err)
		return m, nil
	}
	reportCompletion(m.client, m.cfg, done)
	m.status = fmt.Sprintf("completed #%s (use `tasks complete %s --result` from the CLI to add a note)", t.ID, t.ID)
	return m, m.loadCmd()
}

func (m tasksModel) remove() (tea.Model, tea.Cmd) {
	t, ok := m.current()
	if !ok {
		return m, nil
	}
	if err := m.store.Remove(t.ID); err != nil {
		m.status = fmt.Sprintf("remove #%s: %v", t.ID, err)
		return m, nil
	}
	m.status = fmt.Sprintf("removed #%s", t.ID)
	return m, m.loadCmd()
}

func (m tasksModel) View() string {
	if len(m.rows) == 0 {
		return "no tasks — `claudes tasks add <title>` to queue one\n\nq quit\n"
	}
	var b strings.Builder
	b.WriteString(renderTasks(m.rows, m.cursor, m.width))
	b.WriteString("\n")
	if m.status != "" {
		b.WriteString(taskMeta.Render(m.status) + "\n")
	}
	b.WriteString(taskMeta.Render("↑/↓ move  c claim  x complete  d remove  r refresh  q quit"))
	b.WriteString("\n")
	return b.String()
}

// mergeTaskRows swaps in fresh rows while keeping the cursor on the same task id.
func mergeTaskRows(old, fresh []tasks.Task, cursor *int) []tasks.Task {
	if len(old) == 0 || *cursor >= len(old) {
		if *cursor >= len(fresh) {
			*cursor = max(0, len(fresh)-1)
		}
		return fresh
	}
	want := old[*cursor].ID
	for i, t := range fresh {
		if t.ID == want {
			*cursor = i
			return fresh
		}
	}
	if *cursor >= len(fresh) {
		*cursor = max(0, len(fresh)-1)
	}
	return fresh
}

// --- rendering ---

var (
	taskID      = lipgloss.NewStyle().Foreground(lipgloss.Color("13")).Bold(true) // magenta
	taskTitle   = lipgloss.NewStyle().Bold(true)
	taskDone    = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Strikethrough(true)
	taskWho     = lipgloss.NewStyle().Foreground(lipgloss.Color("6")) // cyan
	taskMeta    = lipgloss.NewStyle().Foreground(lipgloss.Color("8")) // dim
	taskCursor  = lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Bold(true)
	taskKeyName = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)

func taskStatusColor(s tasks.Status) lipgloss.Color {
	switch s {
	case tasks.StatusQueued:
		return lipgloss.Color("11") // yellow — waiting for someone
	case tasks.StatusClaimed:
		return lipgloss.Color("12") // blue — in progress
	case tasks.StatusDone:
		return lipgloss.Color("10") // green — finished
	default:
		return lipgloss.Color("8")
	}
}

// renderTasks renders the queue as tight two-line cards with a status-colored
// left rail. cursor is the selected index (-1 for none, e.g. `tasks ls`).
func renderTasks(rows []tasks.Task, cursor, width int) string {
	if width <= 0 {
		width = 80
	}
	interactive := cursor >= 0

	blocks := make([]string, len(rows))
	for i, t := range rows {
		titleStyle := taskTitle
		if t.Status == tasks.StatusDone {
			titleStyle = taskDone
		}
		line1 := taskID.Render("#"+t.ID) + " " + titleStyle.Render(t.Title)
		if t.Project != "" {
			line1 += "  " + taskWho.Render(t.Project)
		}

		// Line 2: who it's for / who's on it / where it came from.
		var parts []string
		switch t.Status {
		case tasks.StatusClaimed:
			parts = append(parts, "claimed by "+dash(t.Claimant))
		case tasks.StatusQueued:
			if t.Assignee != "" {
				parts = append(parts, "for "+t.Assignee)
			} else {
				parts = append(parts, "open")
			}
		case tasks.StatusDone:
			parts = append(parts, "done")
			if t.Result != "" {
				parts = append(parts, t.Result)
			}
		}
		if t.Creator != "" {
			parts = append(parts, "from "+t.Creator)
		}
		budget := width - 4
		if interactive {
			budget -= 2
		}
		line2 := taskMeta.Render(truncate(strings.Join(parts, "  ·  "), max(10, budget)))

		if interactive {
			gutter := "  "
			if i == cursor {
				gutter = taskCursor.Render("▸") + " "
			}
			line1 = gutter + line1
			line2 = "  " + line2
		}

		rail := lipgloss.NewStyle().
			Border(lipgloss.ThickBorder(), false, false, false, true).
			BorderForeground(taskStatusColor(t.Status)).
			PaddingLeft(1)
		blocks[i] = rail.Render(line1 + "\n" + line2)
	}
	return strings.Join(blocks, "\n") + "\n"
}

// formatTaskDetail is the `tasks show` long form.
func formatTaskDetail(t tasks.Task) string {
	row := func(k, v string) string {
		if v == "" {
			v = "—"
		}
		return taskKeyName.Render(fmt.Sprintf("%-11s", k)) + v + "\n"
	}
	var b strings.Builder
	b.WriteString(taskID.Render("#"+t.ID) + " " + taskTitle.Render(t.Title) + "\n")
	b.WriteString(row("status", string(t.Status)))
	b.WriteString(row("project", t.Project))
	b.WriteString(row("creator", t.Creator))
	b.WriteString(row("assignee", t.Assignee))
	b.WriteString(row("claimant", t.Claimant))
	b.WriteString(row("created", t.CreatedAt))
	if t.ClaimedAt != "" {
		b.WriteString(row("claimed", t.ClaimedAt))
	}
	if t.CompletedAt != "" {
		b.WriteString(row("completed", t.CompletedAt))
	}
	if t.Result != "" {
		b.WriteString(row("result", t.Result))
	}
	return b.String()
}
