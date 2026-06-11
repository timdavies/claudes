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
	cardModel   = lipgloss.NewStyle().Foreground(lipgloss.Color("13")) // magenta
	cardProject = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))  // cyan
	cardMeta    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))  // dim
	cardCursor  = lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Bold(true)
)

// statusColor is the rail color for a status: it's the only place state is
// surfaced now that the standalone dot is gone.
func statusColor(s session.Status) lipgloss.Color {
	switch s {
	case session.StatusRunning:
		return lipgloss.Color("10") // green
	case session.StatusWaiting:
		return lipgloss.Color("11") // yellow
	case session.StatusIdle:
		return lipgloss.Color("12") // blue
	case session.StatusStopped:
		return lipgloss.Color("9") // red
	default: // paused / unknown
		return lipgloss.Color("8") // dim
	}
}

// renderAgents renders the list as tight two-line cards, each fronted by a
// status-colored left rail. cursor is the selected index (-1 for none, e.g.
// non-interactive `ls`); width truncates the second line.
func renderAgents(rows []agentRow, cursor, width int) string {
	if width <= 0 {
		width = 80
	}
	interactive := cursor >= 0

	nameW := 0
	for _, r := range rows {
		if w := lipgloss.Width(nameCell(r)); w > nameW {
			nameW = w
		}
	}

	blocks := make([]string, len(rows))
	for i, r := range rows {
		name := nameCell(r)
		nameStyle := cardName
		if r.Status == session.StatusPaused {
			nameStyle = cardNamePaused
		}
		namePadded := nameStyle.Render(name) + strings.Repeat(" ", nameW-lipgloss.Width(name))

		// Line 1: name · model · project.
		line1 := namePadded + "  " + cardModel.Render(dash(r.Model))
		if r.Project != "" {
			line1 += "  " + cardProject.Render(r.Project)
		}

		// Line 2: dir, plus the daemon's description when present.
		meta := tildify(r.Dir)
		if r.Description != "" {
			meta += "  ·  " + r.Description
		}
		budget := width - 4
		if interactive {
			budget -= 2
		}
		line2 := cardMeta.Render(truncate(meta, max(10, budget)))

		if interactive {
			gutter := "  "
			if i == cursor {
				gutter = cardCursor.Render("▸") + " "
			}
			line1 = gutter + line1
			line2 = "  " + line2
		}

		rail := lipgloss.NewStyle().
			Border(lipgloss.ThickBorder(), false, false, false, true).
			BorderForeground(statusColor(r.Status)).
			PaddingLeft(1)
		blocks[i] = rail.Render(line1 + "\n" + line2)
	}
	return strings.Join(blocks, "\n") + "\n"
}

// nameCell is the name plus a trailing pin marker when pinned.
func nameCell(r agentRow) string {
	if r.Pinned {
		return r.Name + " 📌"
	}
	return r.Name
}
