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

	m, _ = send(m, key("x"))
	if m.mode != modeConfirmKill {
		t.Fatalf("x should enter confirm-kill mode, got %v", m.mode)
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

	// x then y confirms and dispatches a kill.
	m, _ = send(m, key("x"))
	m, cmd = send(m, key("y"))
	if m.mode != modeList {
		t.Fatalf("y should return to the list, got %v", m.mode)
	}
	if cmd == nil {
		t.Fatalf("y should dispatch a kill command")
	}
}

func TestNewKeyOpensFormAndEscCancels(t *testing.T) {
	m := baseModel()

	m, _ = send(m, key("n"))
	if m.mode != modeNew || m.form == nil {
		t.Fatalf("n should open the new-agent form")
	}

	m, _ = send(m, special(tea.KeyEsc))
	if m.mode != modeList || m.form != nil {
		t.Fatalf("esc should close the form")
	}
}

func TestNewFormEditingAndSubmitValidation(t *testing.T) {
	m := baseModel()
	m, _ = send(m, key("n"))

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

	if !strings.Contains(m.View(), "n new") {
		t.Errorf("list footer should advertise the n/x keys")
	}

	m, _ = send(m, key("x"))
	if !strings.Contains(m.View(), "kill alpha?") {
		t.Errorf("confirm-kill view should prompt for confirmation")
	}

	m, _ = send(m, key("n")) // n is treated as 'no' in confirm mode -> back to list
	m, _ = send(m, key("n")) // now open the form
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
