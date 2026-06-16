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
	State       string
	Group       string
	Cost        string
	PR          string // attached pull-request URL, may be empty
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
				Group: e.Group, Status: session.StatusPaused, Pinned: true,
			})
		}
	}
	// Group-major ordering so `claudes ls` and the TUI render contiguous
	// groups: the default group ("") sorts first, then groups alphabetically.
	// Within a group, pinned agents float to the top (so the long-lived pinned
	// agents — usually ungrouped — sit at the very top of the list), then by
	// name. Keeping the slice grouped is what lets the TUI treat the cursor as a
	// plain row index.
	sort.Slice(sessions, func(i, j int) bool {
		if sessions[i].Group != sessions[j].Group {
			return sessions[i].Group < sessions[j].Group
		}
		if sessions[i].Pinned != sessions[j].Pinned {
			return sessions[i].Pinned // pinned first
		}
		return sessions[i].Name < sessions[j].Name
	})
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

	// TabSID/HasTab come straight from the registry — no per-tick mc.List()
	// (osascript) round-trip. Liveness is verified lazily on Enter (resolveTab),
	// which prunes stale entries then; the registry hint is only a focus
	// shortcut, so a closed-out-from-under-us tab costs nothing until used.
	tracked := map[string]string{} // name -> session_id (from registry)
	reg, _ := tabRegistryFor(cfg)
	if reg != nil {
		for name, t := range reg.All() {
			tracked[name] = t.SessionID
		}
	}

	rows := make([]agentRow, len(sessions))
	for i, s := range sessions {
		sid, has := tracked[s.Name]
		// A self-reported activity (via `claudes status`) takes precedence over
		// any ambient summary set under @claudes-description.
		desc := s.SelfReport
		if desc == "" {
			desc = s.Description
		}
		rows[i] = agentRow{
			Name:        s.Name,
			Project:     s.Project,
			Model:       s.Model,
			Status:      s.Status,
			Dir:         s.Dir,
			Description: desc,
			State:       s.State,
			Group:       s.Group,
			Cost:        s.Cost,
			PR:          s.PR,
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
	cardPR         = lipgloss.NewStyle().Foreground(lipgloss.Color("12")) // blue
	cardMeta       = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))  // dim
	cardCursor     = lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Bold(true)
	cardGroup      = lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Bold(true)                                 // yellow
	cardCost       = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))                                            // green
	cardSelected   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("0")).Background(lipgloss.Color("14")) // black-on-cyan chip
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
// non-interactive `ls`); col is the selected cell within the cursor row (0 =
// the agent, 1 = its attached PR); width truncates the second line.
func renderAgents(rows []agentRow, cursor, col, width int) string {
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
	prevGroup, started := "", false
	for i, r := range rows {
		// Emit a header whenever the group changes. Rows are pre-sorted
		// group-major, so each group's header appears exactly once, above its
		// first agent. The default group ("") is headerless — it just sits at
		// the top, matching how ungrouped agents have always rendered.
		var header string
		if r.Group != prevGroup || !started {
			if r.Group != "" {
				// Each card already trails a blank line (see the join below), so
				// the group label just needs its own line — no extra separator.
				header = cardGroup.Render(r.Group) + "\n"
				if interactive {
					header = indentLines(header, "  ")
				}
				// A couple of blank lines set each group apart from the one
				// above it — but not when this is the very first block (no
				// default group preceding it).
				if i > 0 {
					header = "\n\n" + header
				}
			}
			prevGroup = r.Group
			started = true
		}

		name := nameCell(r)
		selected := i == cursor
		pr := prDisplayID(r.PR)
		// The cursor highlight lives on whichever cell is active: the agent name
		// (col 0) or its PR chip (col 1). With no PR, the name always wins so a
		// stale col can't strand the highlight on an empty cell.
		prActive := selected && col == 1 && pr != ""
		nameActive := selected && !prActive
		nameStyle := cardName
		switch {
		case nameActive:
			nameStyle = cardSelected // bright chip so the current agent reads at a glance
		case r.Status == session.StatusPaused:
			nameStyle = cardNamePaused
		}
		namePadded := nameStyle.Render(name) + strings.Repeat(" ", nameW-lipgloss.Width(name))

		// Line 1 left cluster: name · #pr · model · cost.
		left := namePadded
		if pr != "" {
			prStyle := cardPR
			if prActive {
				prStyle = cardSelected
			}
			left += "  " + prStyle.Render(pr)
		}
		left += "  " + cardModel.Render(dash(r.Model))
		if r.Cost != "" && r.Cost != "0.00" {
			left += "  " + cardCost.Render("$"+r.Cost)
		}
		indent := ""
		if interactive {
			indent = "  "
			if selected {
				indent = cardCursor.Render("▸") + " "
			}
		}
		left = indent + left

		// Content fills the card to the terminal width, minus the rail's
		// border + padding (2 cols). The working dir is right-aligned on line 1
		// via a spacer; it's truncated first so a long path can't crowd it out.
		contentW := max(20, width-2)
		leftW := lipgloss.Width(left)
		dirStr := truncate(shortDir(r.Dir), max(4, contentW-leftW-1))
		dirRendered := cardMeta.Render(dirStr)
		spacer := max(1, contentW-leftW-lipgloss.Width(dirRendered))
		line1 := left + strings.Repeat(" ", spacer) + dirRendered

		// Line 2: the agent's self-reported activity (preferred) or the daemon's
		// ambient summary. A self-reported state shows as a leading chip, tinted
		// the same color as the rail so the two read as one signal.
		line2 := ""
		chip := ""
		if r.State != "" {
			chip = lipgloss.NewStyle().Foreground(statusColor(r.Status)).Bold(true).Render("[" + r.State + "] ")
		}
		if r.Description != "" || chip != "" {
			descIndent := ""
			if interactive {
				descIndent = "  "
			}
			body := truncate(r.Description, max(10, contentW-lipgloss.Width(descIndent)-lipgloss.Width(chip)))
			line2 = descIndent + chip + cardMeta.Render(body)
		}

		railColor := statusColor(r.Status)
		if selected {
			railColor = lipgloss.Color("14") // bright cyan rail for the current agent
		}
		// PaddingBottom(1) gives each card a blank line of breathing room that's
		// *inside* the border, so the left rail stays an unbroken straight line
		// down a group instead of breaking at every gap.
		rail := lipgloss.NewStyle().
			Border(lipgloss.ThickBorder(), false, false, false, true).
			BorderForeground(railColor).
			PaddingLeft(1).
			PaddingBottom(1)
		body := line1
		if line2 != "" {
			body += "\n" + line2
		}
		blocks[i] = header + rail.Render(body)
	}
	return strings.Join(blocks, "\n") + "\n"
}

// indentLines prefixes every non-empty line of s with prefix. Empty lines are
// left bare so blank separators between groups stay blank.
func indentLines(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = prefix + line
		}
	}
	return strings.Join(lines, "\n")
}

// prDisplayID renders an attached PR url as a compact "#123" chip. GitHub PR
// urls end with /pull/<number>; for anything else we fall back to the last path
// segment so a non-standard url still shows something glanceable.
func prDisplayID(url string) string {
	if url == "" {
		return ""
	}
	seg := strings.TrimRight(url, "/")
	if idx := strings.LastIndex(seg, "/"); idx >= 0 {
		seg = seg[idx+1:]
	}
	if seg == "" {
		return ""
	}
	return "#" + seg
}

// nameCell is the name plus a trailing pin marker when pinned.
func nameCell(r agentRow) string {
	if r.Pinned {
		return r.Name + " 📌"
	}
	return r.Name
}
