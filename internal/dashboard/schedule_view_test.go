// SPEC: _spec/internal/dashboard/schedule-manager.puml
package dashboard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/proveo-ca/proveo/internal/schedule"
	"github.com/proveo-ca/proveo/internal/ui"
)

const fantasySchedule = `# kickoff-relative fantasy football passes
jobs:
  ff-lineup:          # pass 1 of 2
    target: hermes
    model: inkling-2-mini
    prompt_file: PROMPTS/lineup.md
    budget: 36m
    tz: America/New_York
    at:
      - thu 18:50
      - sun 11:35
  ff-odds:
    target: hermes
    prompt_file: PROMPTS/odds.md
    budget: 40m
    tz: America/New_York
    at:
      - thu 18:15
  ff-trades:
    target: hermes
    prompt_file: PROMPTS/trades.md
`

func scheduleModel(t *testing.T) (*dashModel, string) {
	t.Helper()
	home := t.TempDir()
	prompts := filepath.Join(home, "prompts")
	if err := os.MkdirAll(prompts, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"lineup", "odds", "trades"} {
		if err := os.WriteFile(filepath.Join(prompts, p+".md"), []byte("Set the "+p+" for this week.\nReport what changed.\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	body := strings.ReplaceAll(fantasySchedule, "PROMPTS", prompts)
	if err := os.WriteFile(schedule.Path(home), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 12, 3, 9, 0, 0, 0, ny)
	m := newModel(Options{Home: home, CWD: home}, ui.GlyphsSafe)
	m.clock = func() time.Time { return now }
	m.tickStatus = func() schedule.Tick {
		return schedule.Tick{Installed: true, Manager: "launchd io.proveo.schedule, every 60s"}
	}
	m.width, m.height = 140, 44
	m.say("")
	m.view = scheduleView
	m.receiveSchedule(loadSchedule(home, now, m.tickStatus))
	return m, home
}

func screen(m *dashModel) string { return ansi.Strip(m.View().Content) }

func TestTheScheduleViewDrawsTheWeekTheJobsAndTheSelectedJob(t *testing.T) {
	m, _ := scheduleModel(t)
	out := screen(m)
	for _, want := range []string{
		"Schedule", "✓ tick launchd",
		"Mon 30", "Thu 3", "Sun 6", "11:30", "18:00", "18:30", "19:00",
		"Jobs", "ff-odds", "ff-lineup", "ff-trades", "manual",
		"overlap", "times EST",
		"Overlaps ff-lineup Thu 18:50", "Budget   40m",
		"Prompt", "Set the odds for this week.",
		"e edit", "p pause", "r run now", "n new job",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the schedule view lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "n new instance") {
		t.Errorf("view 3 offers a new job, not a new instance:\n%s", out)
	}
	if got := m.schedJob().name; got != "ff-odds" {
		t.Errorf("the soonest job opens selected, got %q", got)
	}
}

func TestTheGridMarksTheSelectedJobAndTheOverlap(t *testing.T) {
	m, _ := scheduleModel(t)
	g := weekGrid{slots: m.sched.slots, week: m.sched.week, now: m.now(), sel: "ff-odds", glyphs: ui.GlyphsSafe}
	rows, cells := g.render(80)
	row := func(label string) string {
		t.Helper()
		for _, r := range rows {
			if strings.HasPrefix(ansi.Strip(r), label) {
				return r
			}
		}
		t.Fatalf("no %s row:\n%s", label, ansi.Strip(strings.Join(rows, "\n")))
		return ""
	}
	thu := gridLabel + 3*(cellWidth(80)+1)
	thursday := func(r string) string { return string([]rune(ansi.Strip(r))[thu : thu+cellWidth(80)]) }
	if cellWidth(80) != 6 {
		t.Fatalf("80 cells give six five-minute slices a day, got %d", cellWidth(80))
	}
	if got := thursday(row("18:00")); got != "   ███" {
		t.Errorf("Thursday 18:00–18:30 = %q: odds from 18:15", got)
	}
	at1830 := row("18:30")
	if got := thursday(at1830); got != "████▒█" {
		t.Errorf("Thursday 18:30–19:00 = %q: odds to 18:55, lineup from 18:50 over its last minutes", got)
	}
	if !strings.Contains(at1830, warnText.Render("▒")) || !strings.Contains(at1830, brand.Render("█")) {
		t.Errorf("the overlap draws warn, the selected job brand: %q", at1830)
	}
	found := false
	for _, c := range cells {
		if c.job == "ff-odds" {
			found = true
		}
	}
	if !found {
		t.Error("a click on the odds run must select ff-odds")
	}
}

func TestScheduleKeysMoveAndPauseAJob(t *testing.T) {
	m, home := scheduleModel(t)
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if got := m.schedJob().name; got != "ff-lineup" {
		t.Fatalf("down selects the next job, got %q", got)
	}
	m.Update(tea.KeyPressMsg{Code: 'p', Text: "p"})
	if m.pauseChoice == nil {
		t.Fatal("p must ask how long to pause")
	}
	m.Update(tea.KeyPressMsg{Code: 'w', Text: "w"})
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("a week's pause must write the file")
	}
	m.Update(cmd())
	c, err := schedule.LoadFile(schedule.Path(home))
	if err != nil || c.Jobs["ff-lineup"].PausedUntil != "2026-12-10" {
		t.Fatalf("one week from Thu Dec 3 = %+v %v", c.Jobs["ff-lineup"], err)
	}
	m.receiveSchedule(loadSchedule(home, m.now(), m.tickStatus))
	if out := screen(m); !strings.Contains(out, "paused until Thu Dec 10") || !strings.Contains(out, "p resume") {
		t.Errorf("a paused job says so and offers resume:\n%s", out)
	}
	m.Update(tea.KeyPressMsg{Code: 'p', Text: "p"})
	if m.confirm == nil {
		t.Fatal("p on a paused job must ask to resume")
	}
	m.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	_, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.Update(cmd())
	if b, _ := os.ReadFile(schedule.Path(home)); strings.Contains(string(b), "paused") {
		t.Errorf("resume must drop the pause:\n%s", b)
	}
}

func TestAnEditIsSavedOnlyWhenTheDraftIsValid(t *testing.T) {
	m, home := scheduleModel(t)
	path := schedule.Path(home)
	before, _ := os.ReadFile(path)
	m.editSchedule("ff-odds", m.schedJob().line)
	e := m.schedEdit
	if e == nil || e.line != 12 {
		t.Fatalf("the draft opens at ff-odds' line, got %+v", e)
	}
	m.receiveEdit(editedEvent{})
	if m.schedEdit != nil || !strings.Contains(m.status, "unchanged") {
		t.Errorf("an untouched draft saves nothing: %q", m.status)
	}
	m.editSchedule("ff-odds", 12)
	draft := strings.Replace(string(before), "budget: 40m", "budget: 40 minutes", 1)
	if err := os.WriteFile(m.schedEdit.draft, []byte(draft), 0o600); err != nil {
		t.Fatal(err)
	}
	m.receiveEdit(editedEvent{})
	if m.confirm == nil {
		t.Fatal("an invalid draft must be refused with its reason")
	}
	if b, _ := os.ReadFile(path); string(b) != string(before) {
		t.Fatal("a refused draft must leave schedule.yml alone")
	}
	if out := screen(m); !strings.Contains(out, "40 minutes") || !strings.Contains(out, "Edit again") {
		t.Errorf("the refusal names the error and offers the editor:\n%s", out)
	}
	m.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if _, err := os.Stat(draftPath(path)); !os.IsNotExist(err) || m.schedEdit != nil {
		t.Errorf("discard removes the draft: %v", err)
	}
	m.editSchedule("ff-odds", 12)
	if err := os.WriteFile(m.schedEdit.draft, []byte(strings.Replace(string(before), "budget: 40m", "budget: 25m", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	m.receiveEdit(editedEvent{})
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), "budget: 25m") || !strings.Contains(string(b), "# pass 1 of 2") {
		t.Errorf("a valid draft replaces the file, comments and all:\n%s", b)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o644 {
		t.Errorf("the file keeps its mode: %v", st.Mode().Perm())
	}
}

func TestANewJobIsAStubTheOperatorFillsIn(t *testing.T) {
	m, home := scheduleModel(t)
	m.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	e := m.schedEdit
	if e == nil {
		t.Fatal("n must open the editor on a draft")
	}
	b, _ := os.ReadFile(e.draft)
	lines := strings.Split(string(b), "\n")
	if e.line < 1 || e.line > len(lines) || strings.TrimSpace(lines[e.line-1]) != "job-1:" {
		t.Fatalf("the editor opens on the stub's first line, got line %d:\n%s", e.line, b)
	}
	if _, err := schedule.LoadFile(e.draft); err != nil {
		t.Errorf("the stub itself must be valid: %v", err)
	}
	m.receiveEdit(editedEvent{})
	if c, _ := schedule.LoadFile(schedule.Path(home)); len(c.Jobs) != 3 {
		t.Errorf("an untouched stub adds nothing: %d jobs", len(c.Jobs))
	}
}

func TestEditorArgvOpensTheFileAtTheLine(t *testing.T) {
	t.Parallel()
	env := func(visual, editor string) func(string) string {
		return func(k string) string {
			return map[string]string{"VISUAL": visual, "EDITOR": editor}[k]
		}
	}
	for _, c := range []struct {
		visual, editor string
		want           string
	}{
		{"", "", "vi +12 f.yml"},
		{"", "nvim", "nvim +12 f.yml"},
		{"code --wait", "vim", "code --wait --goto f.yml:12"},
		{"", "cursor", "cursor --wait --goto f.yml:12"},
		{"", "hx", "hx f.yml:12"},
		{"", "zed", "zed --wait f.yml:12"},
	} {
		if got := strings.Join(editorArgv("f.yml", 12, env(c.visual, c.editor)), " "); got != c.want {
			t.Errorf("VISUAL=%q EDITOR=%q: %q, want %q", c.visual, c.editor, got, c.want)
		}
	}
}

func TestAnInvalidScheduleSaysWhyAndOffersTheEditor(t *testing.T) {
	m, home := scheduleModel(t)
	if err := os.WriteFile(schedule.Path(home), []byte("jobs:\n  x:\n    target: hermes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m.receiveSchedule(loadSchedule(home, m.now(), m.tickStatus))
	out := screen(m)
	for _, want := range []string{"schedule.yml is not valid", "prompt_file is required", "e open it"} {
		if !strings.Contains(out, want) {
			t.Errorf("an invalid file must show %q:\n%s", want, out)
		}
	}
}

func TestRunsSharingASliceWithoutOverlappingAreNotAClash(t *testing.T) {
	t.Parallel()
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	c := schedule.Config{Jobs: map[string]schedule.Job{
		"lineup": {Target: "hermes", PromptFile: "/p", Budget: "36m", At: []string{"sun 11:35"}, Timezone: "America/New_York"},
		"refine": {Target: "hermes", PromptFile: "/p", Budget: "36m", At: []string{"sun 12:17"}, Timezone: "America/New_York"},
	}}
	now := time.Date(2026, 12, 3, 9, 0, 0, 0, ny)
	week := schedule.WeekStart(now, ny)
	g := weekGrid{slots: schedule.Week(c, week), week: week, now: now, sel: "lineup", glyphs: ui.GlyphsSafe}
	rows, _ := g.render(80)
	for _, r := range rows[1 : len(rows)-1] {
		if strings.Contains(r, "▒") {
			t.Errorf("lineup ends 12:11 and refine starts 12:17, both inside the 12:10 slice; no clash:\n%s", ansi.Strip(strings.Join(rows, "\n")))
		}
	}
}
