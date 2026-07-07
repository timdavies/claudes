package cmd

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/timdavies/claudes/internal/config"
	"github.com/timdavies/claudes/internal/session"
)

func filterModel() tuiModel {
	return tuiModel{
		cfg: &config.Config{Prefix: "claudes-", Projects: map[string]config.Project{}},
		rows: []agentRow{
			{Name: "grow-4419", Status: session.StatusIdle},
			{Name: "bifrost", Group: "review", Status: session.StatusIdle},
			{Name: "grow-4420", Status: session.StatusIdle},
		},
	}
}

func TestFilterJumpsAndFilters(t *testing.T) {
	m := filterModel()

	m, _ = send(m, key("/"))
	if !m.filtering {
		t.Fatal("/ should enter filtering")
	}

	// Typing a ticket fragment snaps the cursor to the first match and narrows.
	for _, r := range "4420" {
		m, _ = send(m, key(string(r)))
	}
	if m.filter != "4420" {
		t.Fatalf("filter = %q", m.filter)
	}
	fr, ci := m.filteredRows()
	if len(fr) != 1 || fr[0].Name != "grow-4420" {
		t.Fatalf("expected only grow-4420, got %+v", fr)
	}
	if ci < 0 || fr[ci].Name != "grow-4420" {
		t.Fatalf("cursor should sit on the match, ci=%d", ci)
	}

	// Group is matchable too.
	m, _ = send(m, special(tea.KeyEsc))
	if m.filtering || m.filter != "" {
		t.Fatal("esc should clear the filter")
	}
	m, _ = send(m, key("/"))
	for _, r := range "review" {
		m, _ = send(m, key(string(r)))
	}
	if fr, _ := m.filteredRows(); len(fr) != 1 || fr[0].Name != "bifrost" {
		t.Fatalf("group match failed, got %+v", fr)
	}
}

func TestFilterBackspaceWidensAgain(t *testing.T) {
	m := filterModel()
	m, _ = send(m, key("/"))
	for _, r := range "grow" {
		m, _ = send(m, key(string(r)))
	}
	if fr, _ := m.filteredRows(); len(fr) != 2 {
		t.Fatalf("expected 2 grow rows, got %d", len(fr))
	}
	m, _ = send(m, special(tea.KeyBackspace))
	if m.filter != "gro" {
		t.Fatalf("backspace should trim to 'gro', got %q", m.filter)
	}
}
