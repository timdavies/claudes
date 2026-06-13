package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/timdavies/claudes/internal/config"
	"github.com/timdavies/claudes/internal/session"
	"github.com/timdavies/claudes/internal/tmux"
)

// formField identifies a row in the new-agent form, in tab order.
type formField int

const (
	fieldName formField = iota
	fieldDir
	fieldPinned
	fieldPrompt
	fieldCount
)

// newForm is the modal create-agent form reached by pressing `n` in the TUI.
// It's a plain value driven by tuiModel.updateNewForm — no separate tea.Model,
// so it shares the dashboard's event loop and styling.
type newForm struct {
	name   string
	dir    string
	pinned bool
	prompt string
	focus  formField

	recent  []string // candidate dirs (absolute) for directory autocomplete
	matches []string // recent dirs matching the current input
	sel     int      // highlighted match index
}

func newNewForm(cfg *config.Config, rows []agentRow) *newForm {
	cwd, _ := os.Getwd()
	f := &newForm{
		dir:    cwd,
		focus:  fieldName,
		recent: recentDirs(cfg, rows, cwd),
	}
	f.name = suggestFormName(cfg, rows, cwd)
	f.refilter()
	return f
}

// recentDirs is the autocomplete pool: dirs you're plausibly working in —
// the cwd, every live/paused agent's dir, and the configured project dirs —
// deduped, cwd first.
func recentDirs(cfg *config.Config, rows []agentRow, cwd string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(d string) {
		if d == "" {
			return
		}
		if abs, err := filepath.Abs(d); err == nil {
			d = abs
		}
		if seen[d] {
			return
		}
		seen[d] = true
		out = append(out, d)
	}
	add(cwd)
	for _, r := range rows {
		add(r.Dir)
	}
	for _, p := range cfg.Projects {
		add(p.Dir)
	}
	return out
}

// suggestFormName mirrors `claudes new`'s auto-naming (base = dir basename,
// "-N" suffix) but checks the rows already on screen rather than hitting tmux.
func suggestFormName(cfg *config.Config, rows []agentRow, dir string) string {
	used := map[string]bool{}
	for _, r := range rows {
		used[r.Name] = true
	}
	base := strings.Trim(filepath.Base(dir), "-./ ")
	if base == "" || base == strings.TrimRight(cfg.Prefix, "-") {
		base = "claude"
	}
	for i := 1; i < 10000; i++ {
		c := fmt.Sprintf("%s-%d", base, i)
		if !used[c] {
			return c
		}
	}
	return base
}

// refilter recomputes the directory suggestions for the current input. It
// matches against both the absolute and ~-form of each recent dir so a typed
// fragment narrows the list. When the input is empty or already equals a recent
// dir exactly (e.g. the prefilled cwd or an accepted suggestion), the full list
// is shown so the user can still pick a different one.
func (f *newForm) refilter() {
	in := strings.ToLower(strings.TrimSpace(f.dir))
	showAll := in == ""
	for _, d := range f.recent {
		if strings.EqualFold(d, f.dir) {
			showAll = true
			break
		}
	}
	f.matches = f.matches[:0]
	for _, d := range f.recent {
		if showAll || strings.Contains(strings.ToLower(d), in) || strings.Contains(strings.ToLower(tildify(d)), in) {
			f.matches = append(f.matches, d)
		}
	}
	if f.sel >= len(f.matches) {
		f.sel = 0
	}
}

func (f *newForm) appendText(s string) {
	switch f.focus {
	case fieldName:
		f.name += s
	case fieldDir:
		f.dir += s
		f.sel = 0
		f.refilter()
	case fieldPrompt:
		f.prompt += s
	}
}

func (f *newForm) backspace() {
	trim := func(s string) string {
		r := []rune(s)
		if len(r) == 0 {
			return s
		}
		return string(r[:len(r)-1])
	}
	switch f.focus {
	case fieldName:
		f.name = trim(f.name)
	case fieldDir:
		f.dir = trim(f.dir)
		f.sel = 0
		f.refilter()
	case fieldPrompt:
		f.prompt = trim(f.prompt)
	}
}

func (m tuiModel) updateNewForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	f := m.form
	switch msg.String() {
	case "esc", "ctrl+c":
		m.mode = modeList
		m.form = nil
		m.status = "new cancelled"
		return m, nil
	case "enter":
		if strings.TrimSpace(f.name) == "" || strings.TrimSpace(f.dir) == "" {
			m.status = "name and dir required"
			return m, nil
		}
		name, dir, pinned, prompt := f.name, expandTilde(f.dir), f.pinned, f.prompt
		m.mode = modeList
		m.form = nil
		m.status = "spawning " + name + "…"
		return m, spawnAgentCmd(m.client, m.cfg, name, dir, pinned, prompt)
	case "tab":
		// On the directory field, Tab first completes to the highlighted
		// suggestion; once the input already equals it, Tab advances.
		if f.focus == fieldDir && len(f.matches) > 0 && tildify(f.matches[f.sel]) != tildify(f.dir) {
			f.dir = f.matches[f.sel]
			f.refilter()
			return m, nil
		}
		f.focus = (f.focus + 1) % fieldCount
		if f.focus == fieldDir {
			f.refilter()
		}
	case "shift+tab":
		f.focus = (f.focus - 1 + fieldCount) % fieldCount
	case "down":
		if f.focus == fieldDir && len(f.matches) > 0 {
			if f.sel < len(f.matches)-1 {
				f.sel++
			}
		} else {
			f.focus = (f.focus + 1) % fieldCount
		}
	case "up":
		if f.focus == fieldDir && len(f.matches) > 0 {
			if f.sel > 0 {
				f.sel--
			}
		} else {
			f.focus = (f.focus - 1 + fieldCount) % fieldCount
		}
	case "right":
		if f.focus == fieldDir && len(f.matches) > 0 {
			f.dir = f.matches[f.sel]
			f.refilter()
		}
	case "backspace":
		f.backspace()
	default:
		switch msg.Type {
		case tea.KeySpace:
			if f.focus == fieldPinned {
				f.pinned = !f.pinned
			} else {
				f.appendText(" ")
			}
		case tea.KeyRunes:
			f.appendText(string(msg.Runes))
		}
	}
	return m, nil
}

// spawnAgentCmd creates the session off the event loop (spawnSession blocks up
// to 30s waiting for claude to boot), optionally pins it, and fires the first
// prompt once it's ready.
func spawnAgentCmd(client *tmux.Client, cfg *config.Config, name, dir string, pin bool, prompt string) tea.Cmd {
	return func() tea.Msg {
		resolved, err := cfg.Resolve(dir, "", dir)
		if err != nil {
			return spawnedMsg{name, err}
		}
		full := session.FullName(cfg.Prefix, name)
		if has, _ := client.Has(full); has {
			return spawnedMsg{name, fmt.Errorf("session already exists")}
		}
		if reg, _ := pinnedRegistry(); reg != nil && reg.Has(name) {
			return spawnedMsg{name, fmt.Errorf("pinned and paused — start or unpin it first")}
		}
		if err := spawnSession(client, cfg, resolved, name, nil, true); err != nil {
			return spawnedMsg{name, err}
		}
		if pin {
			_ = pinLiveAgent(client, cfg, name, resolved, nil)
		}
		if strings.TrimSpace(prompt) != "" {
			_ = client.SendKeys(full, prompt)
		}
		return spawnedMsg{name, nil}
	}
}

// expandTilde turns a leading ~ back into $HOME — the inverse of tildify, so a
// suggestion the user accepts (shown as ~/…) spawns in the right place.
func expandTilde(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return home + strings.TrimPrefix(p, "~")
		}
	}
	return p
}

// --- rendering ---

var (
	formBox      = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("14")).Padding(1, 3)
	formTitle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("14"))
	formLabel    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	formFocus    = lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Bold(true)
	formGhost    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	formSuggSel  = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(lipgloss.Color("14"))
	confirmStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)
)

func (f *newForm) view(width, height int) string {
	var b strings.Builder
	b.WriteString(formTitle.Render("New agent") + "\n\n")
	b.WriteString(f.label("Name", fieldName) + f.value(f.name, fieldName, "") + "\n")
	b.WriteString(f.label("Directory", fieldDir) + f.value(tildify(f.dir), fieldDir, "") + "\n")
	if f.focus == fieldDir {
		shown := f.matches
		if len(shown) > 6 {
			shown = shown[:6]
		}
		for i, d := range shown {
			if i == f.sel {
				b.WriteString("    " + formSuggSel.Render(" "+tildify(d)+" ") + "\n")
			} else {
				b.WriteString("    " + formGhost.Render(tildify(d)) + "\n")
			}
		}
	}
	box := "[ ]"
	if f.pinned {
		box = "[x]"
	}
	b.WriteString(f.label("Pinned", fieldPinned) + box + "\n")
	b.WriteString(f.label("Prompt", fieldPrompt) + f.value(f.prompt, fieldPrompt, "(optional)") + "\n")
	b.WriteString("\n" + formGhost.Render("tab next · ↑/↓ suggestions · space toggle · enter create · esc cancel"))

	content := formBox.Render(b.String())
	if width <= 0 || height <= 0 {
		return content + "\n"
	}
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, content)
}

func (f *newForm) label(name string, fld formField) string {
	marker, st := "  ", formLabel
	if f.focus == fld {
		marker, st = formFocus.Render("▸ "), formFocus
	}
	return marker + st.Render(fmt.Sprintf("%-10s", name))
}

// value renders an editable field's text: a blinking-ish caret when focused, a
// dim placeholder when empty and unfocused.
func (f *newForm) value(val string, fld formField, placeholder string) string {
	if f.focus == fld {
		return val + formFocus.Render("▏")
	}
	if val == "" && placeholder != "" {
		return formGhost.Render(placeholder)
	}
	return val
}
