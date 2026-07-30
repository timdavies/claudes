package cmd

import (
	"fmt"
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
func collectSessions(client *tmux.Client, cfg *config.Config, cache *session.EnvCache) []session.Session {
	var (
		sessions []session.Session
		err      error
	)
	if cache != nil {
		sessions, err = session.ListCached(client, cfg, cache)
	} else {
		sessions, err = session.List(client, cfg)
	}
	if err != nil {
		return nil
	}
	// Pinned order lives in the registry (it must survive the agent dying);
	// non-pinned order rides on each live session's @claudes-order env, already
	// loaded into s.Order by session.List.
	pinOrder := map[string]int{}
	if reg, err := pinnedRegistry(); err == nil && reg != nil {
		live := map[string]bool{}
		for _, s := range sessions {
			live[s.Name] = true
		}
		for name, e := range reg.All() {
			pinOrder[name] = e.Order
			// Archived agents live in their own view, not the main roster.
			if e.Archived {
				continue
			}
			if live[name] {
				continue
			}
			sessions = append(sessions, session.Session{
				Name: name, Project: e.Project, Model: e.Model, Dir: e.Dir,
				Group: e.Group, Status: session.StatusPaused, Pinned: true,
				Order: e.Order,
			})
		}
	}
	for i := range sessions {
		if sessions[i].Pinned {
			sessions[i].Order = pinOrder[sessions[i].Name]
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
		// Within a (group, pinned-ness) block, honor the manual order set via
		// shift+↑/↓ in the TUI. Unset (0) keeps its place at the top by name, so
		// agents nobody has reordered look exactly as before; reordered ones get
		// explicit positions and newly added ones append below them.
		oi, oj := sessions[i].Order, sessions[j].Order
		if oi != oj {
			if oi == 0 || oj == 0 {
				return oi == 0
			}
			return oi < oj
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
func loadAgentRows(client *tmux.Client, cfg *config.Config, cache *session.EnvCache) []agentRow {
	sessions := collectSessions(client, cfg, cache)

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
	cardModel      = lipgloss.NewStyle().Foreground(lipgloss.Color("13"))                                            // magenta
	cardPR         = lipgloss.NewStyle().Foreground(lipgloss.Color("12"))                                            // blue
	cardMeta       = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))                                             // dim
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
	// `ls` never folds — always render every group expanded.
	return strings.Join(renderAgentBlocks(rows, cursor, col, width, nil), "\n") + "\n"
}

// renderAgentBlocks renders one block per row (group header + two-line card),
// without joining them. The TUI needs the blocks separately to measure each
// row's line span for scroll math; renderAgents just joins them for `ls`.
// groupSize counts how many rows belong to the named group.
func groupSize(rows []agentRow, group string) int {
	n := 0
	for _, r := range rows {
		if r.Group == group {
			n++
		}
	}
	return n
}

// renderAgentBlocks builds one render block per row. folded holds collapsed
// section keys (nil while filtering): a folded named group renders as a single
// caret header with a count on its first row, and its remaining rows render as
// empty blocks (skipped by the scroll geometry).
func renderAgentBlocks(rows []agentRow, cursor, col, width int, folded map[string]bool) []string {
	if width <= 0 {
		width = 80
	}

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
		isFirst := r.Group != prevGroup || !started
		prevGroup = r.Group
		started = true

		collapsed := r.Group != "" && folded[foldKeyGroup(r.Group)]
		if collapsed {
			if !isFirst {
				blocks[i] = "" // hidden under the fold header
				continue
			}
			// Fold header stands in for the whole group as a single tight line —
			// no surrounding blank padding, so consecutive folded groups stack
			// one per line with no gap.
			label := fmt.Sprintf("▸ %s (%d)", r.Group, groupSize(rows, r.Group))
			style := cardGroup
			if i == cursor {
				style = cardSelected
			}
			blocks[i] = style.Render(label)
			continue
		}

		var header string
		if isFirst && r.Group != "" {
			// Each card already trails a blank line (see the join below), so
			// the group label just needs its own line — no extra separator. The
			// ▾ caret marks it as an expanded, foldable section.
			header = cardGroup.Render("▾ "+r.Group) + "\n"
			// A couple of blank lines set each group apart from the one above it
			// — but not when this is the very first block.
			if i > 0 {
				header = "\n\n" + header
			}
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
			body := truncate(r.Description, max(10, contentW-lipgloss.Width(chip)))
			line2 = chip + cardMeta.Render(body)
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
	return blocks
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
