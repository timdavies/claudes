package cmd

import (
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/timdavies/claudes/internal/config"
	"github.com/timdavies/claudes/internal/session"
	"github.com/timdavies/claudes/internal/tmux"
)

// agentRow is the unified shape that both `claudes ls` and the TUI render. It's
// a session enriched with tab-backend state. Building and rendering it lives
// here so the two front-ends can't drift apart.
type agentRow struct {
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

// collectSessions returns the merged session list both front-ends start from:
// live tmux sessions plus paused pinned agents (those whose tmux session is
// gone), sorted by name.
func collectSessions(client *tmux.Client, cfg *config.Config) []session.Session {
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
	return sessions
}

// loadAgentRows builds the row list from tmux + pin registry + tab registry.
//
// Tab state is reconciled against the backend's *live* tab list, not just the
// registry: a tab closed directly in the terminal leaves a stale registry
// entry, so we verify each tracked session_id is still live and prune the ones
// that aren't. When the backend is unreachable (List errors) we trust the
// registry rather than blanking every indicator.
func loadAgentRows(client *tmux.Client, cfg *config.Config) []agentRow {
	sessions := collectSessions(client, cfg)

	tracked := map[string]string{} // name -> session_id (from registry)
	reg, _ := tabRegistryFor(cfg)
	if reg != nil {
		for name, t := range reg.All() {
			tracked[name] = t.SessionID
		}
	}

	live := map[string]bool{}
	liveKnown := false
	if mc, ok := tabClientFor(cfg); ok {
		if tabs, err := mc.List(); err == nil {
			liveKnown = true
			for _, t := range tabs {
				if t.SessionID != "" {
					live[t.SessionID] = true
				}
			}
		}
	}

	rows := make([]agentRow, len(sessions))
	for i, s := range sessions {
		sid, has := tracked[s.Name]
		if has && liveKnown && !live[sid] {
			// Tab was closed out from under us — drop the stale entry so the
			// indicator goes dark and a later focus re-resolves by title.
			has = false
			sid = ""
			if reg != nil {
				_ = reg.Delete(s.Name)
			}
		}
		rows[i] = agentRow{
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

// --- rendering ---

var (
	cardName       = lipgloss.NewStyle().Bold(true)
	cardNamePaused = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("8"))
	cardModel      = lipgloss.NewStyle().Foreground(lipgloss.Color("13")) // magenta
	cardProject    = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))  // cyan
	cardMeta       = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))  // dim
	cardDesc       = lipgloss.NewStyle().Foreground(lipgloss.Color("7")).Italic(true)
	cardCursor     = lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true)
	cardTabOn      = lipgloss.NewStyle().Foreground(lipgloss.Color("10")) // green
)

// statusDot returns the leading glyph, colored by status. Hollow for the
// dead/parked states (stopped, paused), filled for the live ones.
func statusDot(s session.Status) string {
	switch s {
	case session.StatusRunning:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Render("●") // green
	case session.StatusWaiting:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render("●") // yellow
	case session.StatusIdle:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Render("●") // blue
	case session.StatusStopped:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render("○") // red
	case session.StatusPaused:
		return cardMeta.Render("○")
	default:
		return cardMeta.Render("○")
	}
}

func statusWord(s session.Status) string {
	style := cardMeta
	switch s {
	case session.StatusRunning:
		style = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	case session.StatusWaiting:
		style = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	case session.StatusIdle:
		style = lipgloss.NewStyle().Foreground(lipgloss.Color("12"))
	case session.StatusStopped:
		style = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	}
	return style.Render(string(s))
}

// renderAgents renders the whole list as multi-line cards. cursor is the index
// of the selected row (-1 for none, e.g. non-interactive `ls`). width is the
// terminal column count used to truncate the description/dir lines.
func renderAgents(rows []agentRow, cursor, width int) string {
	if width <= 0 {
		width = 80
	}
	nameW := 0
	for _, r := range rows {
		if w := visibleWidth(nameCell(r)); w > nameW {
			nameW = w
		}
	}

	var b strings.Builder
	for i, r := range rows {
		if i > 0 {
			b.WriteString("\n")
		}

		// Line 1: cursor + dot + name + model + project.
		prefix := "  "
		if i == cursor {
			prefix = cardCursor.Render("▸") + " "
		}
		name := nameCell(r)
		nameStyle := cardName
		if r.Status == session.StatusPaused {
			nameStyle = cardNamePaused
		}
		namePadded := nameStyle.Render(name) + strings.Repeat(" ", nameW-visibleWidth(name))
		line1 := prefix + statusDot(r.Status) + " " + namePadded + "   " + cardModel.Render(dash(r.Model))
		if r.Project != "" {
			line1 += "   " + cardProject.Render(r.Project)
		}
		b.WriteString(line1 + "\n")

		// Line 2: status · tab · dir (indented under the name).
		meta := []string{statusWord(r.Status)}
		if r.HasTab {
			meta = append(meta, cardTabOn.Render("tab"))
		} else {
			meta = append(meta, cardMeta.Render("no tab"))
		}
		meta = append(meta, cardMeta.Render(truncate(tildify(r.Dir), max(10, width-20))))
		b.WriteString("    " + strings.Join(meta, cardMeta.Render("  ·  ")) + "\n")

		// Line 3: description, only when the daemon has written one.
		if r.Description != "" {
			b.WriteString("    " + cardDesc.Render(truncate(r.Description, max(10, width-5))) + "\n")
		}
	}
	return b.String()
}

// nameCell is the name plus a trailing pin marker when pinned.
func nameCell(r agentRow) string {
	if r.Pinned {
		return r.Name + " 📌"
	}
	return r.Name
}
