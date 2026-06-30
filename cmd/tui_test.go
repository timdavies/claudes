package cmd

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/timdavies/claudes/internal/config"
	"github.com/timdavies/claudes/internal/session"
)

func key(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

func special(t tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: t} }

func send(m tuiModel, msg tea.Msg) (tuiModel, tea.Cmd) {
	updated, cmd := m.Update(msg)
	return updated.(tuiModel), cmd
}

func baseModel() tuiModel {
	return tuiModel{
		cfg: &config.Config{Prefix: "claudes-", Projects: map[string]config.Project{}},
		rows: []agentRow{
			{Name: "alpha", Status: session.StatusIdle, Dir: "/tmp/alpha"},
		},
	}
}

func TestConfirmKillRequiresConfirmation(t *testing.T) {
	m := baseModel()

	m, _ = send(m, key("X"))
	if m.mode != modeConfirmKill {
		t.Fatalf("X should enter confirm-kill mode, got %v", m.mode)
	}
	if m.confirmRow.Name != "alpha" {
		t.Fatalf("confirm row should be the cursor row, got %q", m.confirmRow.Name)
	}

	// n cancels without killing.
	m, cmd := send(m, key("n"))
	if m.mode != modeList {
		t.Fatalf("n should return to the list, got %v", m.mode)
	}
	if cmd != nil {
		t.Fatalf("cancel should not dispatch a kill command")
	}

	// X then y confirms and dispatches a kill.
	m, _ = send(m, key("X"))
	m, cmd = send(m, key("y"))
	if m.mode != modeList {
		t.Fatalf("y should return to the list, got %v", m.mode)
	}
	if cmd == nil {
		t.Fatalf("y should dispatch a kill command")
	}
}

func TestPausedPinnedAgentRefusesKill(t *testing.T) {
	m := baseModel()
	m.rows = []agentRow{{Name: "alpha", Status: session.StatusPaused, Pinned: true, Dir: "/tmp/alpha"}}

	m, cmd := send(m, key("X"))
	if m.mode != modeList {
		t.Fatalf("X on a paused pinned agent should stay on the list, got %v", m.mode)
	}
	if cmd != nil {
		t.Fatalf("X on a paused pinned agent should not dispatch a command")
	}
	if m.status != "can't kill a pinned agent" {
		t.Fatalf("expected refusal message, got %q", m.status)
	}
}

func TestConfirmPromptSaysPauseForPinnedRunning(t *testing.T) {
	m := baseModel()
	m.rows = []agentRow{{Name: "alpha", Status: session.StatusRunning, Pinned: true, Dir: "/tmp/alpha"}}
	m.width, m.height = 80, 24

	m, _ = send(m, key("X"))
	if m.mode != modeConfirmKill {
		t.Fatalf("X on a running pinned agent should confirm, got %v", m.mode)
	}
	if v := m.View(); !strings.Contains(v, "pause alpha?") {
		t.Errorf("confirm prompt for a pinned agent should say pause, got:\n%s", v)
	}

	// y dispatches a pause (not a kill) and returns to the list.
	m, cmd := send(m, key("y"))
	if m.mode != modeList {
		t.Fatalf("y should return to the list, got %v", m.mode)
	}
	if cmd == nil {
		t.Fatalf("y should dispatch a pause command")
	}
}

func TestNewKeyOpensFormAndEscCancels(t *testing.T) {
	m := baseModel()

	m, _ = send(m, key("N"))
	if m.mode != modeNew || m.form == nil {
		t.Fatalf("N should open the new-agent form")
	}

	m, _ = send(m, special(tea.KeyEsc))
	if m.mode != modeList || m.form != nil {
		t.Fatalf("esc should close the form")
	}
}

func TestNewFormEditingAndSubmitValidation(t *testing.T) {
	m := baseModel()
	m, _ = send(m, key("N"))

	// Clear the suggested name, then type a new one.
	for range m.form.name {
		m, _ = send(m, special(tea.KeyBackspace))
	}
	for _, ch := range "beta" {
		m, _ = send(m, key(string(ch)))
	}
	if m.form.name != "beta" {
		t.Fatalf("name field should read 'beta', got %q", m.form.name)
	}

	// Tab to the pinned checkbox and toggle it.
	m, _ = send(m, special(tea.KeyTab)) // name -> dir
	m, _ = send(m, special(tea.KeyTab)) // dir -> pinned
	if m.form.focus != fieldPinned {
		t.Fatalf("expected focus on pinned, got %v", m.form.focus)
	}
	m, _ = send(m, special(tea.KeySpace))
	if !m.form.pinned {
		t.Fatalf("space should toggle the pinned checkbox on")
	}

	// Submitting with a blank dir is rejected (stays on the form).
	m.form.dir = ""
	m, cmd := send(m, special(tea.KeyEnter))
	if m.mode != modeNew {
		t.Fatalf("submit with blank dir should stay on the form")
	}
	if cmd != nil {
		t.Fatalf("invalid submit should not spawn")
	}
}

func TestViewRendersEachMode(t *testing.T) {
	m := baseModel()
	m.width, m.height = 80, 24

	if !strings.Contains(m.View(), "N new") {
		t.Errorf("list footer should advertise the N/X keys")
	}

	m, _ = send(m, key("X"))
	if !strings.Contains(m.View(), "kill alpha?") {
		t.Errorf("confirm-kill view should prompt for confirmation")
	}

	m, _ = send(m, key("n")) // n is treated as 'no' in confirm mode -> back to list
	m, _ = send(m, key("N")) // now open the form
	if v := m.View(); !strings.Contains(v, "New agent") || !strings.Contains(v, "Directory") {
		t.Errorf("new-agent form should render its fields, got:\n%s", v)
	}
}

func TestNewFormDirAutocomplete(t *testing.T) {
	f := &newForm{
		recent: []string{"/tmp/alpha", "/tmp/beta", "/home/x/Projects/grow"},
	}
	f.dir = "beta"
	f.refilter()
	if len(f.matches) != 1 || f.matches[0] != "/tmp/beta" {
		t.Fatalf("expected the single beta match, got %v", f.matches)
	}

	f.dir = "tmp"
	f.refilter()
	if len(f.matches) != 2 {
		t.Fatalf("expected both /tmp matches, got %v", f.matches)
	}

	f.dir = ""
	f.refilter()
	if len(f.matches) != 3 {
		t.Fatalf("empty input should suggest all recent dirs, got %v", f.matches)
	}
}

func TestScrollKeepsCursorVisible(t *testing.T) {
	m := baseModel()
	m.width = 80
	m.height = 12 // small viewport: cards (~3 lines) won't all fit
	rows := make([]agentRow, 20)
	for i := range rows {
		rows[i] = agentRow{Name: "agent" + string(rune('a'+i)), Status: session.StatusIdle, Dir: "/tmp"}
	}
	m.rows = rows

	// Jump to the last row; the viewport must scroll so it's on screen.
	m, _ = send(m, key("G"))
	lines, top, bot := m.bodyGeometry()
	viewH := m.viewportHeight()
	if m.scroll > top || bot >= m.scroll+viewH {
		t.Fatalf("cursor row [%d,%d] not within window [%d,%d)", top, bot, m.scroll, m.scroll+viewH)
	}
	if m.scroll == 0 {
		t.Fatalf("expected a non-zero scroll offset for the last of %d rows", len(lines))
	}

	// Back to the top resets the scroll.
	m, _ = send(m, key("g"))
	if m.scroll != 0 {
		t.Fatalf("g should scroll back to top, got scroll=%d", m.scroll)
	}

	// The body window never exceeds the viewport height.
	body, _ := m.renderBody(m.footer())
	if got := strings.Count(body, "\n") + 1; got > viewH {
		t.Fatalf("rendered body has %d lines, viewport is %d", got, viewH)
	}
}

func TestReorderStaysWithinBlock(t *testing.T) {
	m := baseModel()
	m.client = newClient(m.cfg) // persistUnpinnedOrder needs a non-nil client
	m.rows = []agentRow{
		{Name: "pin1", Pinned: true, Status: session.StatusIdle},
		{Name: "free1", Status: session.StatusIdle},
		{Name: "free2", Status: session.StatusIdle},
	}
	// Cursor on the lone pinned row; shift+up/down can't move it (no pinned
	// neighbor) and must never swap it past the non-pinned block.
	m.cursor = 0
	(&m).moveAgent(1)
	if m.rows[0].Name != "pin1" {
		t.Fatalf("pinned row crossed into the non-pinned block: %q at 0", m.rows[0].Name)
	}
	// Non-pinned rows reorder among themselves.
	m.cursor = 1
	(&m).moveAgent(1)
	if m.rows[1].Name != "free2" || m.rows[2].Name != "free1" {
		t.Fatalf("non-pinned reorder failed: %q,%q", m.rows[1].Name, m.rows[2].Name)
	}
}
