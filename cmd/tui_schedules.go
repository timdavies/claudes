package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/timdavies/claudes/internal/config"
	"github.com/timdavies/claudes/internal/daemon"
	"github.com/timdavies/claudes/internal/schedule"
	"github.com/timdavies/claudes/internal/worktree"
)

// tuiRegion tracks which list the cursor is in: the agents (top) or the
// scheduled prompts (bottom). Keeping two cursors avoids disturbing the
// agentRow flat-index model that mergeRows relies on.
type tuiRegion int

const (
	regionAgents tuiRegion = iota
	regionSchedules
)

type schedulesMsg struct {
	schedules []schedule.Schedule
	lastRun   map[string]string  // schedule id -> "done 3m ago"
	cost      map[string]float64 // schedule id -> cumulative USD across runs
}
type schedActionMsg struct {
	status string
	err    error
}

// loadSchedulesCmd refreshes the schedule list off the event loop.
func loadSchedulesCmd() tea.Cmd {
	return func() tea.Msg {
		scs, last, cost := loadSchedulesNow()
		return schedulesMsg{schedules: scs, lastRun: last, cost: cost}
	}
}

func loadSchedulesNow() ([]schedule.Schedule, map[string]string, map[string]float64) {
	store, err := scheduleStore()
	if err != nil {
		return nil, nil, nil
	}
	scs := store.All()
	last := map[string]string{}
	cost := map[string]float64{}
	for _, sc := range scs {
		runs := store.RunsFor(sc.ID)
		if len(runs) > 0 {
			last[sc.ID] = lastRunText(runs[0])
		}
		for _, r := range runs {
			cost[sc.ID] += r.Cost
		}
	}
	return scs, last, cost
}

func lastRunText(r schedule.Run) string {
	prefix := ""
	if r.Status == schedule.RunAuthFailed {
		prefix = "⚠ "
	}
	ts := r.FinishedAt
	if ts == "" {
		ts = r.StartedAt
	}
	if t, err := time.Parse(time.RFC3339, ts); err == nil {
		return prefix + string(r.Status) + " " + relTime(time.Since(t))
	}
	return prefix + string(r.Status)
}

func relTime(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// normalizeRegion keeps the region/cursor sane as the two lists change under
// refresh: collapse to agents when there are no schedules, jump to schedules
// when there are no agents, and clamp the schedule cursor.
func (m *tuiModel) normalizeRegion() {
	if len(m.schedules) == 0 {
		m.region = regionAgents
	} else if len(m.rows) == 0 {
		m.region = regionSchedules
	}
	if m.schedCursor >= len(m.schedules) {
		m.schedCursor = max(0, len(m.schedules)-1)
	}
	if m.schedCursor < 0 {
		m.schedCursor = 0
	}
}

func (m tuiModel) currentSchedule() (schedule.Schedule, bool) {
	if m.schedCursor < 0 || m.schedCursor >= len(m.schedules) {
		return schedule.Schedule{}, false
	}
	return m.schedules[m.schedCursor], true
}

// updateScheduleList handles keys while the cursor is in the schedules region.
func (m tuiModel) updateScheduleList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc", "ctrl+c":
		return m, tea.Quit
	case "up", "k":
		if m.schedCursor > 0 {
			m.schedCursor--
		} else if len(m.rows) > 0 {
			m.region = regionAgents
			m.cursor = len(m.rows) - 1
			m.col = 0
		}
	case "down", "j":
		if m.schedCursor < len(m.schedules)-1 {
			m.schedCursor++
		}
	case "g", "home":
		m.schedCursor = 0
	case "G", "end":
		m.schedCursor = max(0, len(m.schedules)-1)
	case "enter":
		return m.openScheduleLogs()
	// Mutating actions require shift (uppercase) so a stray bare keystroke can't
	// add/edit/delete/run/toggle a schedule by accident.
	case "N":
		m.schedForm = newSchedForm(m.cfg, nil)
		m.mode = modeScheduleForm
		m.status = ""
	case "E":
		if sc, ok := m.currentSchedule(); ok {
			m.schedForm = newSchedForm(m.cfg, &sc)
			m.mode = modeScheduleForm
			m.status = ""
		}
	case "X":
		if sc, ok := m.currentSchedule(); ok {
			m.schedToRm = sc
			m.mode = modeScheduleConfirmRm
			m.status = ""
		}
	case "T":
		return m.toggleSchedule()
	case "R":
		if sc, ok := m.currentSchedule(); ok {
			m.status = "firing " + sc.Name + "…"
			return m, fireScheduleCmd(m.cfg, sc.ID)
		}
	case "r":
		m.status = "refreshed"
		return m, loadSchedulesCmd()
	}
	return m, nil
}

func (m tuiModel) toggleSchedule() (tea.Model, tea.Cmd) {
	sc, ok := m.currentSchedule()
	if !ok {
		return m, nil
	}
	return m, setScheduleEnabledCmd(sc.ID, !sc.Enabled)
}

func (m tuiModel) openScheduleLogs() (tea.Model, tea.Cmd) {
	sc, ok := m.currentSchedule()
	if !ok {
		return m, nil
	}
	store, err := scheduleStore()
	if err != nil {
		m.status = err.Error()
		return m, nil
	}
	m.logView = &schedLogView{schedule: sc, runs: store.RunsFor(sc.ID)}
	m.mode = modeScheduleLogs
	return m, nil
}

// updateScheduleConfirmRm handles the y/n delete prompt.
func (m tuiModel) updateScheduleConfirmRm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y", "enter":
		m.mode = modeList
		return m, removeScheduleCmd(m.schedToRm.ID)
	case "n", "N", "esc", "ctrl+c", "q":
		m.mode = modeList
		m.status = "delete cancelled"
	}
	return m, nil
}

// --- schedule action commands (run off the event loop) ---

func setScheduleEnabledCmd(id string, on bool) tea.Cmd {
	return func() tea.Msg {
		store, err := scheduleStore()
		if err != nil {
			return schedActionMsg{err: err}
		}
		if err := store.SetEnabled(id, on); err != nil {
			return schedActionMsg{err: err}
		}
		if on {
			ensureDaemonForSchedule()
			return schedActionMsg{status: "enabled #" + id}
		}
		return schedActionMsg{status: "disabled #" + id}
	}
}

func removeScheduleCmd(id string) tea.Cmd {
	return func() tea.Msg {
		store, err := scheduleStore()
		if err != nil {
			return schedActionMsg{err: err}
		}
		if err := store.Remove(id); err != nil {
			return schedActionMsg{err: err}
		}
		if dir, derr := daemon.CacheDir(); derr == nil {
			_ = os.RemoveAll(filepath.Join(dir, "schedules", id))
		}
		return schedActionMsg{status: "removed #" + id}
	}
}

func fireScheduleCmd(cfg *config.Config, id string) tea.Cmd {
	return func() tea.Msg {
		store, err := scheduleStore()
		if err != nil {
			return schedActionMsg{err: err}
		}
		ensureDaemonForSchedule()
		if err := daemon.FireNow(cfg, store, id); err != nil {
			return schedActionMsg{err: err}
		}
		return schedActionMsg{status: "fired #" + id}
	}
}

func saveScheduleCmd(sc schedule.Schedule, isEdit bool) tea.Cmd {
	return func() tea.Msg {
		store, err := scheduleStore()
		if err != nil {
			return schedActionMsg{err: err}
		}
		if isEdit {
			err = store.Update(sc)
		} else {
			_, err = store.Add(sc)
		}
		if err != nil {
			return schedActionMsg{err: err}
		}
		if sc.Enabled {
			ensureDaemonForSchedule()
		}
		return schedActionMsg{status: "saved " + sc.Name}
	}
}

// --- schedules section rendering ---

var (
	schedHeader  = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Bold(true)
	schedName    = lipgloss.NewStyle().Bold(true)
	schedOn      = lipgloss.NewStyle().Foreground(lipgloss.Color("10")) // green dot
	schedOff     = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))  // dim dot
	schedNextSty = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))  // cyan
)

// renderSchedules renders the bottom schedules section, one line per schedule.
// cursor is the selected index, or -1 when the region is inactive. cost maps a
// schedule id to its cumulative run cost (USD), shown as a green $ matching the
// agent list and `tasks ls`.
func renderSchedules(schedules []schedule.Schedule, lastRun map[string]string, cost map[string]float64, cursor, width int) string {
	if len(schedules) == 0 {
		return ""
	}
	if width <= 0 {
		width = 80
	}
	now := time.Now()
	nameW := 0
	for _, sc := range schedules {
		if w := lipgloss.Width(sc.Name); w > nameW {
			nameW = w
		}
	}

	var b strings.Builder
	b.WriteString(schedHeader.Render("schedules") + "\n")
	for i, sc := range schedules {
		dot := schedOff.Render("○")
		if sc.Enabled {
			dot = schedOn.Render("●")
		}
		// Selection is the bright name-chip, matching the agent list.
		nameStyle := schedName
		if i == cursor {
			nameStyle = cardSelected
		}
		name := nameStyle.Render(sc.Name) + strings.Repeat(" ", max(0, nameW-lipgloss.Width(sc.Name)))
		next := "—"
		if sc.Enabled {
			next = humanizeNext(sc, now)
		}
		line := fmt.Sprintf("  %s  %s  %s  %s",
			name, cardMeta.Render(pad(schedule.Spec(sc), 16)),
			dot, schedNextSty.Render(pad(next, 14)))
		// Always show a $ amount ($0.00 when no spend) so the column aligns.
		line += "  " + cardCost.Render(formatUSD(cost[sc.ID]))
		if last := lastRun[sc.ID]; last != "" {
			line += "  " + cardMeta.Render("last: "+last)
		}
		// ansi.Truncate counts visible columns, not bytes — truncating the raw
		// styled string (as the old rune-based truncate did) sliced through
		// escape sequences and bled the wrong color onto the name.
		b.WriteString(ansi.Truncate(line, width, "…") + "\n")
	}
	return b.String()
}

func pad(s string, w int) string {
	if lipgloss.Width(s) >= w {
		return s
	}
	return s + strings.Repeat(" ", w-lipgloss.Width(s))
}

// --- run-logs detail view ---

type schedLogView struct {
	schedule schedule.Schedule
	runs     []schedule.Run
	sel      int
	body     string // loaded run output; "" => show the run list
	scroll   int
}

func (m tuiModel) updateScheduleLogs(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	v := m.logView
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc", "q":
		if v.body != "" {
			v.body = ""
			v.scroll = 0
			return m, nil
		}
		m.mode = modeList
		m.logView = nil
		return m, nil
	case "up", "k":
		if v.body != "" {
			if v.scroll > 0 {
				v.scroll--
			}
		} else if v.sel > 0 {
			v.sel--
		}
	case "down", "j":
		if v.body != "" {
			v.scroll++
		} else if v.sel < len(v.runs)-1 {
			v.sel++
		}
	case "enter":
		if v.body == "" && len(v.runs) > 0 {
			v.body = loadRunLog(v.runs[v.sel])
			v.scroll = 0
		}
	}
	return m, nil
}

func loadRunLog(r schedule.Run) string {
	dir, err := daemon.CacheDir()
	if err != nil {
		return "(cache dir unavailable)"
	}
	b, err := os.ReadFile(filepath.Join(dir, r.LogFile))
	if err != nil {
		return "(no output captured)"
	}
	clean := sanitizeLog(string(b))
	if strings.TrimSpace(clean) == "" {
		return "(no output captured)"
	}
	return clean
}

// ansiSeq matches terminal control sequences left over from the old pipe-pane
// capture: CSI, OSC, charset designations, and bare escapes.
var ansiSeq = regexp.MustCompile("\x1b\\[[0-?]*[ -/]*[@-~]" + // CSI (params 0x30-0x3F, intermediates 0x20-0x2F, final 0x40-0x7E)
	"|\x1b\\][^\x07]*\x07" + // OSC (BEL-terminated)
	"|\x1b[()#][0-9A-Za-z]" + // charset designation
	"|\x1b[0-9=>NOMDEHc78]") // misc single-char escapes

// sanitizeLog turns a captured run log into clean text: strips ANSI escape
// sequences, the legacy completion sentinel, and stray control bytes (incl. the
// CRs pty capture inserts), keeping newlines and tabs. Run logs written after
// the stdout-redirect change are already clean; this keeps the old pipe-pane
// logs on disk legible too.
func sanitizeLog(s string) string {
	s = ansiSeq.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "__CLAUDES_RUN_DONE__", "")
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	return strings.TrimRight(s, "\n ")
}

// runMetaLine is the one-line status · duration · cost header for a run.
func runMetaLine(r schedule.Run) string {
	parts := []string{string(r.Status)}
	if d := runDuration(r); d != "" {
		parts = append(parts, d)
	}
	if r.Cost > 0 {
		parts = append(parts, formatUSD(r.Cost))
	}
	return cardMeta.Render(strings.Join(parts, " · "))
}

// runDuration renders a run's elapsed time (started→finished), or "" if unknown.
func runDuration(r schedule.Run) string {
	if r.StartedAt == "" || r.FinishedAt == "" {
		return ""
	}
	start, err1 := time.Parse(time.RFC3339, r.StartedAt)
	end, err2 := time.Parse(time.RFC3339, r.FinishedAt)
	if err1 != nil || err2 != nil || !end.After(start) {
		return ""
	}
	d := end.Sub(start)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

func (v *schedLogView) view(width, height int) string {
	var b strings.Builder
	b.WriteString(formTitle.Render("Runs — "+v.schedule.Name) + "\n\n")
	if v.body != "" {
		// Header: status · cost · duration for the selected run (data off the run
		// record — accurate without scraping the log).
		if v.sel >= 0 && v.sel < len(v.runs) {
			b.WriteString(runMetaLine(v.runs[v.sel]) + "\n\n")
		}
		lines := strings.Split(strings.TrimRight(v.body, "\n"), "\n")
		visible := max(5, height-8)
		if v.scroll > max(0, len(lines)-visible) {
			v.scroll = max(0, len(lines)-visible)
		}
		end := min(len(lines), v.scroll+visible)
		b.WriteString(strings.Join(lines[v.scroll:end], "\n"))
		b.WriteString("\n\n" + formGhost.Render("↑/↓ scroll · esc back · q close"))
		return b.String()
	}
	if len(v.runs) == 0 {
		b.WriteString(formGhost.Render("no runs yet"))
		b.WriteString("\n\n" + formGhost.Render("esc back"))
		return b.String()
	}
	for i, r := range v.runs {
		marker := "  "
		if i == v.sel {
			marker = formFocus.Render("▸ ")
		}
		when := r.StartedAt
		if t, err := time.Parse(time.RFC3339, r.StartedAt); err == nil {
			when = t.Local().Format("Jan 2 15:04")
		}
		meta := cardMeta.Render(when)
		if d := runDuration(r); d != "" {
			meta += "  " + cardMeta.Render(pad(d, 5))
		}
		if r.Cost > 0 {
			meta += "  " + cardCost.Render(formatUSD(r.Cost))
		}
		b.WriteString(fmt.Sprintf("%s%-11s %s\n", marker, r.Status, meta))
	}
	b.WriteString("\n" + formGhost.Render("↑/↓ select · enter open · esc back · q close"))
	return b.String()
}

// --- add/edit form ---

type schedField int

const (
	sfName schedField = iota
	sfKind
	sfSpec
	sfWindow
	sfDir
	sfModel
	sfPerm
	sfPrompt
	sfCount
)

type schedForm struct {
	id     string // "" => add
	name   string
	kind   schedule.Kind
	spec   string // every (5m) / daily (09:00) / once (datetime)
	window string // interval only, "9-18"
	dir    string
	model  string
	perm   string
	prompt string
	days   []int // carried through from an edited schedule (no TUI field yet)
	focus  schedField
	err    string

	recent  []string
	matches []string
	sel     int
}

var schedKinds = []schedule.Kind{schedule.KindInterval, schedule.KindDaily, schedule.KindOnce}

func newSchedForm(cfg *config.Config, edit *schedule.Schedule) *schedForm {
	cwd, _ := os.Getwd()
	f := &schedForm{
		kind:   schedule.KindInterval,
		dir:    cwd,
		focus:  sfName,
		recent: recentDirs(cfg, nil, cwd),
	}
	if edit != nil {
		f.id = edit.ID
		f.name = edit.Name
		f.kind = edit.Kind
		f.dir = edit.Dir
		f.model = edit.Model
		f.perm = edit.PermMode
		f.prompt = edit.Prompt
		f.days = edit.Days
		switch edit.Kind {
		case schedule.KindInterval:
			f.spec = shortEvery(edit.EverySec)
			f.window = fmt.Sprintf("%d-%d", edit.StartHour, edit.EndHour)
		case schedule.KindDaily:
			f.spec = edit.AtClock
		case schedule.KindOnce:
			f.spec = edit.AtTime
		}
	}
	f.refilter()
	return f
}

func shortEvery(sec int) string {
	d := time.Duration(sec) * time.Second
	switch {
	case d%time.Hour == 0:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d%time.Minute == 0:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
}

func (f *schedForm) refilter() {
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

func (f *schedForm) appendText(s string) {
	switch f.focus {
	case sfName:
		f.name += s
	case sfSpec:
		f.spec += s
	case sfWindow:
		f.window += s
	case sfDir:
		f.dir += s
		f.sel = 0
		f.refilter()
	case sfModel:
		f.model += s
	case sfPerm:
		f.perm += s
	case sfPrompt:
		f.prompt += s
	}
}

func (f *schedForm) backspace() {
	trim := func(s string) string {
		r := []rune(s)
		if len(r) == 0 {
			return s
		}
		return string(r[:len(r)-1])
	}
	switch f.focus {
	case sfName:
		f.name = trim(f.name)
	case sfSpec:
		f.spec = trim(f.spec)
	case sfWindow:
		f.window = trim(f.window)
	case sfDir:
		f.dir = trim(f.dir)
		f.sel = 0
		f.refilter()
	case sfModel:
		f.model = trim(f.model)
	case sfPerm:
		f.perm = trim(f.perm)
	case sfPrompt:
		f.prompt = trim(f.prompt)
	}
}

func (f *schedForm) cycleKind(dir int) {
	idx := 0
	for i, k := range schedKinds {
		if k == f.kind {
			idx = i
			break
		}
	}
	idx = (idx + dir + len(schedKinds)) % len(schedKinds)
	f.kind = schedKinds[idx]
}

func (m tuiModel) updateScheduleForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	f := m.schedForm
	switch msg.String() {
	case "esc", "ctrl+c":
		m.mode = modeList
		m.schedForm = nil
		m.status = "edit cancelled"
		return m, nil
	case "enter":
		sc, err := f.build()
		if err != nil {
			f.err = err.Error()
			return m, nil
		}
		isEdit := f.id != ""
		m.mode = modeList
		m.schedForm = nil
		m.status = "saving " + sc.Name + "…"
		return m, saveScheduleCmd(sc, isEdit)
	case "tab":
		if f.focus == sfDir && len(f.matches) > 0 && tildify(f.matches[f.sel]) != tildify(f.dir) {
			f.dir = f.matches[f.sel]
			f.refilter()
			return m, nil
		}
		f.focus = (f.focus + 1) % sfCount
		f.skipWindowIfNotInterval(1)
	case "shift+tab":
		f.focus = (f.focus - 1 + sfCount) % sfCount
		f.skipWindowIfNotInterval(-1)
	case "down":
		if f.focus == sfDir && len(f.matches) > 0 {
			if f.sel < len(f.matches)-1 {
				f.sel++
			}
		} else {
			f.focus = (f.focus + 1) % sfCount
			f.skipWindowIfNotInterval(1)
		}
	case "up":
		if f.focus == sfDir && len(f.matches) > 0 {
			if f.sel > 0 {
				f.sel--
			}
		} else {
			f.focus = (f.focus - 1 + sfCount) % sfCount
			f.skipWindowIfNotInterval(-1)
		}
	case "left":
		if f.focus == sfKind {
			f.cycleKind(-1)
		}
	case "right":
		if f.focus == sfKind {
			f.cycleKind(1)
		} else if f.focus == sfDir && len(f.matches) > 0 {
			f.dir = f.matches[f.sel]
			f.refilter()
		}
	case "backspace":
		f.backspace()
	default:
		switch msg.Type {
		case tea.KeySpace:
			if f.focus == sfKind {
				f.cycleKind(1)
			} else {
				f.appendText(" ")
			}
		case tea.KeyRunes:
			f.appendText(string(msg.Runes))
		}
	}
	return m, nil
}

// skipWindowIfNotInterval hops over the window field for non-interval kinds, in
// the direction of travel, so it doesn't sit on an irrelevant field.
func (f *schedForm) skipWindowIfNotInterval(dir int) {
	if f.focus == sfWindow && f.kind != schedule.KindInterval {
		f.focus = schedField((int(f.focus) + dir + int(sfCount)) % int(sfCount))
	}
}

// build validates the form and produces a Schedule.
func (f *schedForm) build() (schedule.Schedule, error) {
	sc := schedule.Schedule{
		ID:       f.id,
		Name:     strings.TrimSpace(f.name),
		Kind:     f.kind,
		Prompt:   f.prompt,
		Model:    strings.TrimSpace(f.model),
		PermMode: strings.TrimSpace(f.perm),
		Enabled:  true,
	}
	if sc.Name == "" {
		return sc, fmt.Errorf("name required")
	}
	if strings.TrimSpace(sc.Prompt) == "" {
		return sc, fmt.Errorf("prompt required")
	}
	repo, err := worktree.RepoRoot(expandTilde(strings.TrimSpace(f.dir)))
	if err != nil {
		return sc, fmt.Errorf("dir must be a git repo")
	}
	sc.Dir = repo

	switch f.kind {
	case schedule.KindInterval:
		sec, err := schedule.ParseEvery(f.spec)
		if err != nil {
			return sc, err
		}
		sc.EverySec = sec
		if strings.TrimSpace(f.window) != "" {
			start, end, err := parseWindow(f.window)
			if err != nil {
				return sc, err
			}
			sc.StartHour, sc.EndHour = start, end
		}
	case schedule.KindDaily:
		clock, err := schedule.ParseClock(f.spec)
		if err != nil {
			return sc, err
		}
		sc.AtClock = clock
		sc.Days = f.days // preserved from the edited schedule (no TUI field yet)
	case schedule.KindOnce:
		if strings.TrimSpace(f.spec) == "" {
			return sc, fmt.Errorf("datetime required")
		}
		sc.AtTime = strings.TrimSpace(f.spec)
	}
	return sc, nil
}

func (f *schedForm) view(width, height int) string {
	var b strings.Builder
	title := "New scheduled prompt"
	if f.id != "" {
		title = "Edit scheduled prompt"
	}
	b.WriteString(formTitle.Render(title) + "\n\n")
	b.WriteString(f.label("Name", sfName) + f.value(f.name, sfName, "") + "\n")
	b.WriteString(f.label("Kind", sfKind) + f.kindValue() + "\n")
	b.WriteString(f.label(f.specLabel(), sfSpec) + f.value(f.spec, sfSpec, f.specPlaceholder()) + "\n")
	if f.kind == schedule.KindInterval {
		b.WriteString(f.label("Window", sfWindow) + f.value(f.window, sfWindow, "9-18 (optional)") + "\n")
	}
	b.WriteString(f.label("Directory", sfDir) + f.value(tildify(f.dir), sfDir, "") + "\n")
	if f.focus == sfDir {
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
	b.WriteString(f.label("Model", sfModel) + f.value(f.model, sfModel, "(default)") + "\n")
	b.WriteString(f.label("Perm", sfPerm) + f.value(f.perm, sfPerm, "auto") + "\n")
	b.WriteString(f.label("Prompt", sfPrompt) + f.value(f.prompt, sfPrompt, "") + "\n")
	if f.err != "" {
		b.WriteString("\n" + confirmStyle.Render(f.err) + "\n")
	}
	b.WriteString("\n" + formGhost.Render("tab next · ←/→ kind · enter save · esc cancel"))

	content := formBox.Render(b.String())
	if width <= 0 || height <= 0 {
		return content + "\n"
	}
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, content)
}

func (f *schedForm) specLabel() string {
	switch f.kind {
	case schedule.KindDaily:
		return "At"
	case schedule.KindOnce:
		return "At"
	default:
		return "Every"
	}
}

func (f *schedForm) specPlaceholder() string {
	switch f.kind {
	case schedule.KindDaily:
		return "09:00"
	case schedule.KindOnce:
		return "2026-06-20 14:00"
	default:
		return "5m"
	}
}

func (f *schedForm) kindValue() string {
	s := string(f.kind)
	if f.focus == sfKind {
		return formFocus.Render("‹ "+s+" ›") + formGhost.Render("  (←/→ to change)")
	}
	return s
}

func (f *schedForm) label(name string, fld schedField) string {
	marker, st := "  ", formLabel
	if f.focus == fld {
		marker, st = formFocus.Render("▸ "), formFocus
	}
	return marker + st.Render(fmt.Sprintf("%-10s", name))
}

func (f *schedForm) value(val string, fld schedField, placeholder string) string {
	if f.focus == fld {
		return val + formFocus.Render("▏")
	}
	if val == "" && placeholder != "" {
		return formGhost.Render(placeholder)
	}
	return val
}
