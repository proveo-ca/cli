// SPEC: _spec/_paradigms/dashboard-terminal-design.puml
package dashboard

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/proveo-ca/proveo/internal/sbx"
	"github.com/proveo-ca/proveo/internal/schedule"
	"github.com/proveo-ca/proveo/internal/ui"
)

var sampleNow = time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)

var sampleChanges = []Change{
	{Kind: "M", Path: "_spec/_experiments/dashboard/workspace-instances.puml"},
	{Kind: "A", Path: "internal/backend/sandbox/checkpoint.go"},
	{Kind: "A", Path: "internal/backend/sandbox/checkpoint_test.go"},
	{Kind: "D", Path: "internal/backend/sandbox/session_by_pwd.go"},
	{Kind: "M", Path: "internal/backend/sandbox/teardown.go"},
	{Kind: "R100", Path: "internal/run/options.go", OldPath: "internal/run/params.go"},
	{Kind: "M", Path: "internal/run/run.go"},
	{Kind: "M", Path: "go.mod"},
}

func sampleModel(w, h int, glyphs ui.GlyphTier) *dashModel {
	m := newModel(Options{CWD: "/Users/dev/projects/proveo"}, glyphs)
	m.clock = func() time.Time { return sampleNow }
	m.width, m.height = w, h
	m.say("")
	m.data = Data{
		Tasks: []Task{
			{ID: "plan-17", Title: "Cap overnight run budgets", Agent: "claudecode", Phase: "Review", Ticket: "PLAN-17",
				State: schedule.Attention, Outcome: "failed", Updated: sampleNow.Add(-5 * time.Hour)},
			{ID: "prv-284", Title: "Persist instance history", Agent: "codex", Phase: "Review", Ticket: "PRV-284",
				State: schedule.Attention, Outcome: "done", Updated: sampleNow.Add(-3 * time.Hour),
				Base: "6bf4c2a9e1", Tip: "1c2f6e4b07", Prompt: "Persist native session history on the host."},
			{ID: "prv-291", Title: "Verify parallel clone isolation", Agent: "opencode", Phase: "Live verification",
				Ticket: "PRV-291", State: schedule.Running, Live: true},
			{ID: "prv-304", Title: "Bind evidence to the review revision", Agent: "claudecode", Phase: "Upgrade",
				Ticket: "PRV-304", State: schedule.Queued, DependsOn: []string{"prv-284", "prv-291"}},
		},
		Capacity: sbx.Capacity{Cores: 8, BusyCores: 4, CPUUsage: true, RAM: 16 << 30, RAMFree: 3 << 30, RAMUsage: true,
			Disk: 512 << 30, DiskFree: 148 << 30},
	}
	m.selected = 1
	m.changes = sampleChanges
	return m
}

func frame(m *dashModel) []string { return strings.Split(m.View().Content, "\n") }

func plain(lines []string) string { return ansi.Strip(strings.Join(lines, "\n")) }

// breakpointGrid is the researched size set plus each tier edge.
var breakpointGrid = [][2]int{
	{39, 9}, {40, 12}, {59, 20}, {60, 20}, {80, 24}, {80, 25}, {84, 46}, {85, 46},
	{100, 14}, {119, 30}, {120, 30}, {120, 40}, {132, 43}, {159, 48}, {160, 48}, {200, 50},
}

func TestLayoutTiers(t *testing.T) {
	for _, c := range []struct {
		w, h int
		want string
	}{
		{39, 24, "too small"}, {80, 9, "too small"}, {40, 12, "single"}, {59, 20, "single"},
		{60, 20, "split"}, {80, 24, "split"}, {84, 46, "portrait"}, {85, 46, "split"},
		{84, 45, "split"}, {100, 14, "short split"}, {120, 30, "split"}, {200, 50, "split"},
	} {
		if got := newLayout(c.w, c.h, 0).mode(); got != c.want {
			t.Errorf("%dx%d: %s, want %s", c.w, c.h, got, c.want)
		}
	}
}

func TestEveryFrameFillsTheTerminalExactly(t *testing.T) {
	states := map[string]func(*dashModel){
		"queue":   func(*dashModel) {},
		"details": func(m *dashModel) { m.details = true },
		"filter": func(m *dashModel) {
			m.focus = 1
			tree := m.fileTree()
			tree.filter, tree.editing = "chk", true
		},
		"schedule": func(m *dashModel) { m.view = 2 },
		"graph":    func(m *dashModel) { m.view = 1 },
		"error":    func(m *dashModel) { m.fail(errors.New(strings.Repeat("/deep/path", 20) + ": cause at the end")) },
	}
	for _, size := range breakpointGrid {
		for name, apply := range states {
			t.Run(fmt.Sprintf("%dx%d/%s", size[0], size[1], name), func(t *testing.T) {
				m := sampleModel(size[0], size[1], ui.GlyphsSafe)
				apply(m)
				lines := frame(m)
				if len(lines) != size[1] {
					t.Fatalf("%d rows, want %d:\n%s", len(lines), size[1], plain(lines))
				}
				for i, line := range lines {
					if got := ansi.StringWidth(line); got != size[0] {
						t.Errorf("row %d is %d cells, want %d: %q", i, got, size[0], ansi.Strip(line))
					}
				}
				if !strings.Contains(plain(lines), "Ctrl+C") {
					t.Errorf("no way out is visible:\n%s", plain(lines))
				}
			})
		}
	}
}

func TestTooSmallCardNamesTheMinimum(t *testing.T) {
	got := plain(frame(sampleModel(39, 9, ui.GlyphsSafe)))
	for _, want := range []string{"Terminal too small", "39×9 · needs 40×10", "Ctrl+C quit"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
}

func TestFooterKeepsTheErrorTail(t *testing.T) {
	for _, w := range []int{60, 120} {
		m := sampleModel(w, 30, ui.GlyphsSafe)
		m.fail(errors.New("/private/tmp/claude/scratchpad/fx/home/schedule.yml: job \"plan-21\": task requires new"))
		footer := ansi.Strip(m.footer(w))
		if !strings.HasPrefix(footer, "Ctrl+C quit") || !strings.HasSuffix(footer, "task requires new") {
			t.Errorf("%d cols: footer %q lost quit or the cause", w, footer)
		}
	}
}

func TestTaskItemsCarryStatusAndMeta(t *testing.T) {
	got := plain(frame(sampleModel(120, 40, ui.GlyphsSafe)))
	for _, want := range []string{
		"◇ Persist instance history", "Ready for review", "× Cap overnight run budgets", "Failed", "● Verify parallel clone",
		"Running", "○ Bind evidence to the review", "Blocked by PRV-284, PRV-291", "Codex · PRV-284", "Claude Code · PLAN-17", "5h ago",
		"╭", "Review", "Live verification",
		"Attention required ─", "Working ─", "Queued ─", "n new instance",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
	if strings.Index(got, "Persist instance history") > strings.Index(got, "Cap overnight run budgets") {
		t.Errorf("the newer outcome should lead its group:\n%s", got)
	}
}

func TestANarrowCardKeepsItsGlyphAndWidth(t *testing.T) {
	m := sampleModel(120, 40, ui.GlyphsSafe)
	lines := m.taskCard(3, m.data.Tasks[3], 38)
	if len(lines) != m.cardHeight() {
		t.Fatalf("%d rows, want %d", len(lines), m.cardHeight())
	}
	for _, line := range lines {
		if ansi.StringWidth(line) != 38 {
			t.Errorf("row %q is %d cells", ansi.Strip(line), ansi.StringWidth(line))
		}
	}
	if first := ansi.Strip(lines[1]); !strings.Contains(first, "○ Bind evidence") {
		t.Errorf("narrow card title row %q lost its glyph", first)
	}
}

func TestPortraitStacksChangesAboveTheQueue(t *testing.T) {
	lines := frame(sampleModel(84, 46, ui.GlyphsSafe))
	changes, queue := -1, -1
	for i, line := range lines {
		text := ansi.Strip(line)
		if changes < 0 && strings.HasPrefix(text, "╭─ Changes") {
			changes = i
		}
		if strings.HasPrefix(text, "╭─ Task Dashboard") {
			queue = i
		}
	}
	if changes < 0 || queue <= changes {
		t.Fatalf("Changes at row %d, Task Dashboard at row %d:\n%s", changes, queue, plain(lines))
	}
}

func TestSinglePaneSwapsOnFocus(t *testing.T) {
	m := sampleModel(50, 20, ui.GlyphsSafe)
	if got := plain(frame(m)); !strings.Contains(got, "Task Dashboard") || strings.Contains(got, "See changes") {
		t.Errorf("queue focus should fill the width:\n%s", got)
	}
	m.focus = 1
	if got := plain(frame(m)); strings.Contains(got, "Task Dashboard") || !strings.Contains(got, "checkpoint.go") {
		t.Errorf("Changes focus should fill the width:\n%s", got)
	}
}

func TestShortFrameDropsCapacityAndKeepsTheQueueUnderThePopover(t *testing.T) {
	m := sampleModel(100, 14, ui.GlyphsSafe)
	if got := plain(frame(m)); strings.Contains(got, "Host capacity") {
		t.Errorf("a short frame keeps the capacity row:\n%s", got)
	}
	m.details = true
	if got := plain(frame(m)); !strings.Contains(got, "Sandbox") || !strings.Contains(got, "Task Dashboard") {
		t.Errorf("short details should be a card over the queue:\n%s", got)
	}
}

func TestGlyphTiersDecideTheMarks(t *testing.T) {
	nerd := plain(frame(sampleModel(120, 40, ui.GlyphsNerd)))
	if !strings.Contains(nerd, string(rune(0xe627))) {
		t.Error("the nerd tier draws no Go devicon")
	}
	safe := plain(frame(sampleModel(120, 40, ui.GlyphsSafe)))
	for _, r := range safe {
		if (r >= 0xe000 && r <= 0xf8ff) || r >= 0xf0000 {
			t.Fatalf("the safe tier drew private-use U+%04X", r)
		}
	}
	ascii := plain(frame(sampleModel(120, 40, ui.GlyphsASCII)))
	for _, want := range []string{"> Persist instance history", "x Cap overnight", "o Bind evidence"} {
		if !strings.Contains(ascii, want) {
			t.Errorf("ascii tier lacks %q", want)
		}
	}
}

func TestNewInstanceHandsTheTerminalToProveoRunQueue(t *testing.T) {
	m := sampleModel(120, 40, ui.GlyphsSafe)
	m.Binary = "/usr/local/bin/proveo"
	var got []string
	m.runner = func(binary string, args ...string) tea.Cmd { got = append([]string{binary}, args...); return nil }
	m.key(tea.KeyPressMsg(tea.Key{Code: 'n', Text: "n"}))
	if m.taskPicker == nil || got != nil {
		t.Fatalf("n ran %q; it should open the task picker first", got)
	}
	m.key(tea.KeyPressMsg{Code: tea.KeyEnter})
	if strings.Join(got, " ") != "/usr/local/bin/proveo run --queue" || m.taskPicker != nil {
		t.Errorf("the blank session ran %q, want the same card as proveo run, queued", got)
	}
}

func TestTheFocusedPaneWearsTheBrandEdgeAndTabMovesIt(t *testing.T) {
	m := sampleModel(120, 30, ui.GlyphsSafe)
	brandEdge, greyEdge := brand.Render("╭─"), ruleText.Render("╭─")
	top := func() (string, string) {
		lines := frame(m)
		for _, line := range lines {
			if strings.Contains(ansi.Strip(line), "─ Task Dashboard") {
				left, right, _ := strings.Cut(line, " "+brand.Render("╭─"))
				if right == "" {
					left, right, _ = strings.Cut(line, " "+ruleText.Render("╭─"))
					return left, ruleText.Render("╭─") + right
				}
				return left, brand.Render("╭─") + right
			}
		}
		t.Fatalf("no pane titles:\n%s", plain(lines))
		return "", ""
	}
	m.focus = 0
	left, right := top()
	if !strings.HasPrefix(left, greyEdge) || !strings.HasPrefix(right, brandEdge) {
		t.Errorf("tasks focused: left %q, right %q", left, right)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if m.focus != 1 {
		t.Fatalf("tab went to focus %d, want the tree", m.focus)
	}
	left, right = top()
	if !strings.HasPrefix(left, brandEdge) || !strings.HasPrefix(right, greyEdge) {
		t.Errorf("tree focused: left %q, right %q", left, right)
	}
	m.preview = true
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if m.focus != 2 {
		t.Errorf("with a file open, tab from the tree goes to the file, got %d", m.focus)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if m.focus != 1 {
		t.Errorf("tab from the file goes back to the tree, got %d", m.focus)
	}
	lines := frame(m)
	for i, line := range lines {
		if got := ansi.StringWidth(line); got != 120 {
			t.Errorf("row %d is %d cells", i, got)
		}
	}
}

func TestAPaneTitleRidesTheEdgeWithoutATrailingEllipsis(t *testing.T) {
	for _, line := range frame(sampleModel(140, 34, ui.GlyphsSafe)) {
		if text := ansi.Strip(line); strings.Contains(text, "╭─ Task Dashboard") && strings.Contains(text, "Task Dashboard …") {
			t.Errorf("a padded title was cut instead of trimmed: %q", text)
		}
	}
}

func TestTheNewInstanceCardAlwaysEndsTheQueuedSection(t *testing.T) {
	sectionOf := func(m *dashModel, needle string) string {
		section := ""
		for _, line := range strings.Split(ansi.Strip(strings.Join(m.renderTasks(140, 60), "\n")), "\n") {
			for _, label := range m.groupLabels() {
				if strings.HasPrefix(strings.TrimSpace(line), label+" ─") || strings.TrimSpace(line) == label {
					section = label
				}
			}
			if strings.Contains(line, needle) {
				return section
			}
		}
		return "nowhere"
	}
	m := sampleModel(140, 60, ui.GlyphsSafe)
	if got := sectionOf(m, "n new instance"); got != "Queued" {
		t.Errorf("with a queued task the card sits under %q", got)
	}
	if sectionOf(m, "Bind evidence to the review revision") != "Queued" {
		t.Fatal("fixture: the queued task is not under Queued")
	}
	m.data.Tasks = slices.DeleteFunc(m.data.Tasks, func(t Task) bool { return t.State == schedule.Queued })
	m.selectVisible()
	if got := sectionOf(m, "n new instance"); got != "Queued" {
		t.Errorf("with nothing queued the card sits under %q; the Queued heading should still show", got)
	}
	m.data.Tasks = nil
	m.selectVisible()
	if got := sectionOf(m, "n new instance"); got != "Queued" {
		t.Errorf("with no tasks the card sits under %q", got)
	}
}
