package cmd

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// The agent list + schedules section can grow taller than the terminal. Rather
// than letting the alt-screen clip the top (stranding agents below the fold),
// the body is rendered into a line window that scrolls to keep the active row
// visible. This mirrors the line-window scrolling already used by the run-logs
// view (schedLogView).

// bodyGeometry builds the full body as a flat slice of lines and reports the
// line span [top, bot] occupied by the active row (agent or schedule), so the
// viewport can keep it on screen. It composes exactly what View renders.
func (m tuiModel) bodyGeometry() (lines []string, top, bot int) {
	agentsCursor, schedCursor, archCursor := -1, -1, -1
	switch m.region {
	case regionAgents:
		agentsCursor = m.cursor
	case regionSchedules:
		schedCursor = m.schedCursor
	case regionArchived:
		archCursor = m.archCursor
	}

	// While filtering, render only matching rows; the active row's index within
	// that filtered slice drives the scroll span.
	viewRows, blockCursor := m.rows, agentsCursor
	if m.filtering {
		fr, ci := m.filteredRows()
		viewRows = fr
		if m.region == regionAgents {
			blockCursor = ci
		} else {
			blockCursor = -1
		}
	}

	var blocks []string
	if len(viewRows) > 0 {
		blocks = renderAgentBlocks(viewRows, blockCursor, m.col, m.width)
	}
	starts := make([]int, len(blocks))
	for i, b := range blocks {
		starts[i] = len(lines)
		lines = append(lines, strings.Split(b, "\n")...)
	}
	if m.region == regionAgents && blockCursor >= 0 && blockCursor < len(blocks) {
		top = starts[blockCursor]
		bot = top + lipgloss.Height(blocks[blockCursor]) - 1
	}

	if sched := renderSchedules(m.schedules, m.schedLastRun, m.schedCost, schedCursor, m.width); sched != "" {
		if len(lines) > 0 {
			lines = append(lines, "") // blank separator between the two sections
		}
		schedStart := len(lines)
		lines = append(lines, strings.Split(strings.TrimRight(sched, "\n"), "\n")...)
		if m.region == regionSchedules {
			// Header sits on schedStart; schedule i on schedStart+1+i.
			top = schedStart + 1 + m.schedCursor
			bot = top
		}
	}

	if arch := renderArchived(m.archived, archCursor, m.width); arch != "" {
		if len(lines) > 0 {
			lines = append(lines, "") // blank separator
		}
		archStart := len(lines)
		lines = append(lines, strings.Split(strings.TrimRight(arch, "\n"), "\n")...)
		if m.region == regionArchived {
			// Header sits on archStart; entry i on archStart+1+i.
			top = archStart + 1 + m.archCursor
			bot = top
		}
	}
	return lines, top, bot
}

// viewportHeight is how many body lines fit above the footer. When the body
// overflows, one line is reserved for the scroll indicator. Returns a large
// value (no constraint) before the first WindowSizeMsg sets the height.
func (m tuiModel) viewportHeight() int {
	if m.height <= 0 {
		return 1 << 20
	}
	avail := m.height - lipgloss.Height(m.footer())
	if avail < 1 {
		avail = 1
	}
	if lines, _, _ := m.bodyGeometry(); len(lines) > avail {
		if avail > 1 {
			avail-- // reserve the indicator line
		}
	}
	return avail
}

// ensureVisible nudges the scroll offset so the active row stays within the
// viewport. Called after any cursor/region/size/rows change.
func (m *tuiModel) ensureVisible() {
	lines, top, bot := m.bodyGeometry()
	viewH := m.viewportHeight()
	if len(lines) <= viewH {
		m.scroll = 0
		return
	}
	if top < m.scroll {
		m.scroll = top
	}
	if bot >= m.scroll+viewH {
		m.scroll = bot - viewH + 1
	}
	if maxScroll := len(lines) - viewH; m.scroll > maxScroll {
		m.scroll = maxScroll
	}
	if m.scroll < 0 {
		m.scroll = 0
	}
}

// renderBody returns the windowed body and a footer prefixed with a scroll
// indicator when content is hidden above or below the viewport.
func (m tuiModel) renderBody(footer string) (body, footerOut string) {
	lines, _, _ := m.bodyGeometry()
	viewH := m.viewportHeight()
	scroll := m.scroll
	if maxScroll := max(0, len(lines)-viewH); scroll > maxScroll {
		scroll = maxScroll
	}
	if scroll < 0 {
		scroll = 0
	}
	end := min(len(lines), scroll+viewH)
	body = strings.Join(lines[scroll:end], "\n")

	if len(lines) > viewH {
		footer = m.scrollIndicator(scroll, len(lines)-end) + "\n" + footer
	}
	return body, footer
}

// pageStep is how many agent rows a PgUp/PgDn jumps — roughly a viewport's
// worth, given each card is about three lines tall.
func (m tuiModel) pageStep() int {
	return max(1, m.viewportHeight()/3)
}

func (m tuiModel) scrollIndicator(above, below int) string {
	var parts []string
	if above > 0 {
		parts = append(parts, "▲ more above")
	}
	if below > 0 {
		parts = append(parts, "▼ more below")
	}
	return cardMeta.Render(strings.Join(parts, "    "))
}
