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
	agentsCursor, schedCursor := -1, -1
	if m.region == regionAgents {
		agentsCursor = m.cursor
	} else {
		schedCursor = m.schedCursor
	}

	var blocks []string
	if len(m.rows) > 0 {
		blocks = renderAgentBlocks(m.rows, agentsCursor, m.col, m.width)
	}
	starts := make([]int, len(blocks))
	for i, b := range blocks {
		starts[i] = len(lines)
		lines = append(lines, strings.Split(b, "\n")...)
	}
	if m.region == regionAgents && m.cursor >= 0 && m.cursor < len(blocks) {
		top = starts[m.cursor]
		bot = top + lipgloss.Height(blocks[m.cursor]) - 1
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
