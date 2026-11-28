// SPEC: _spec/_paradigms/dashboard-terminal-design.puml
package dashboard

import (
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/proveo-ca/proveo/internal/ui"
)

func detailsModel(w, h int) *dashModel {
	m := sampleModel(w, h, ui.GlyphsSafe)
	t := &m.data.Tasks[1]
	t.Sandbox, t.VMID, t.Guest, t.Branch = "codex-prv-284-history-83f91a", "vm-1", true, "proveo/codex-prv-284-history-83f91a"
	t.Prompt = strings.Repeat("Persist native session history on the host and prove it survives a rebuild. ", 6)
	m.guest = guestSample{id: t.ID, sandbox: t.Sandbox, vmid: t.VMID, busy: 1.2, cores: 14, ram: 9 << 30, ramTotal: 16 << 30,
		disk: 21 << 30, uptime: 26 * time.Hour, at: sampleNow, available: true}
	m.samples = map[string]guestSample{t.ID: m.guest}
	m.metadata[metadataKey(*t)] = taskMetadata{id: t.ID, key: metadataKey(*t), pr: prInfo{Number: 104, State: "OPEN", Draft: true, URL: "https://github.com/proveo-ca/cli/pull/104"}}
	m.details = true
	return m
}

func TestDetailsPopoverIsACardThatNeverScrolls(t *testing.T) {
	for _, size := range breakpointGrid {
		if size[0] < minCols || size[1] < minRows {
			continue
		}
		m := detailsModel(size[0], size[1])
		lines := frame(m)
		if len(lines) != size[1] {
			t.Fatalf("%dx%d: %d rows", size[0], size[1], len(lines))
		}
		for i, line := range lines {
			if got := ansi.StringWidth(line); got != size[0] {
				t.Errorf("%dx%d row %d is %d cells: %q", size[0], size[1], i, got, ansi.Strip(line))
			}
		}
		got := plain(lines)
		for _, want := range []string{"╭", "╰", "Sandbox", "Usage", "t close"} {
			if size[1] >= 16 && !strings.Contains(got, want) {
				t.Errorf("%dx%d: card lacks %q:\n%s", size[0], size[1], want, got)
			}
		}
	}
}

func TestDetailsCardShowsUsagePRAndAShortPrompt(t *testing.T) {
	m := detailsModel(120, 40)
	got := plain(frame(m))
	for _, want := range []string{"CPU 1.2/14 cores", "RAM 9 / 16 GiB", "Disk 21 GiB", "up 1d", "PR #104 · draft", "Prompt"} {
		if !strings.Contains(got, want) {
			t.Errorf("card lacks %q", want)
		}
	}
	lines := m.detailLines(m.task(), 70, 30)
	at := slices.IndexFunc(lines, func(l string) bool { return strings.HasPrefix(ansi.Strip(l), "Prompt") })
	if at < 0 || len(lines)-at != 3 || !strings.HasSuffix(ansi.Strip(lines[len(lines)-1]), "…") {
		t.Errorf("the prompt takes %d lines from %d of %d; want three, cut with …", len(lines)-at, at, len(lines))
	}
}

func TestFocusMovesToTheTreeAndEscReturnsToTasks(t *testing.T) {
	m := sampleModel(120, 40, ui.GlyphsSafe)
	m.key(tea.KeyPressMsg(tea.Key{Code: 'g', Text: "g"}))
	if m.focus != 1 || !strings.Contains(ansi.Strip(m.footer(120)), "Esc back to tasks") {
		t.Fatalf("g: focus %d, footer %q", m.focus, ansi.Strip(m.footer(120)))
	}
	m.key(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	if m.focus != 0 {
		t.Errorf("Esc left focus on %d, want the tasks", m.focus)
	}
	m.details = true
	m.key(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	if m.details {
		t.Error("Esc should close the details card first")
	}
}

func TestTheWheelScrollsTheTaskList(t *testing.T) {
	m := sampleModel(120, 40, ui.GlyphsSafe)
	first := m.taskOrder()[0]
	m.selected = first
	m.Update(tea.MouseWheelMsg(tea.Mouse{Button: tea.MouseWheelDown}))
	if m.selected == first {
		t.Error("the wheel did not move through the tasks")
	}
}

func TestASandboxNameIsNotATicket(t *testing.T) {
	t.Parallel()
	if got := ticketLabel(Task{Title: "proveo-claudecode-36915573", Sandbox: "proveo-claudecode-36915573"}); got != "" {
		t.Errorf("a discovered sandbox's name read as ticket %q", got)
	}
	if got := ticketLabel(Task{Ticket: "PRV-284"}); got != "PRV-284" {
		t.Errorf("a real ticket = %q", got)
	}
}

func TestTasksAreCardsInAGrid(t *testing.T) {
	m := sampleModel(120, 40, ui.GlyphsSafe)
	frame(m)
	first, second := m.cardPos[1], m.cardPos[0]
	if first.line != second.line || first.col != 0 || second.col != 1 {
		t.Fatalf("the two attention cards should share a row: %+v %+v", first, second)
	}
	got := plain(frame(m))
	for _, want := range []string{"╭", "Review", "╰", "+ n new instance", "↵ attach"} {
		if !strings.Contains(got, want) {
			t.Errorf("queue lacks %q:\n%s", want, got)
		}
	}
	m.selected = 1
	m.move("right")
	if m.selected != 0 {
		t.Errorf("right moved to task %d, want the card beside", m.selected)
	}
	m.move("down")
	if p := m.cardPos[m.selected]; p.line <= first.line {
		t.Errorf("down stayed on row %d", p.line)
	}
}

func TestACardNamesThePRTitleTicketAndPlan(t *testing.T) {
	m := sampleModel(120, 40, ui.GlyphsSafe)
	task := &m.data.Tasks[2]
	task.Title, task.Sandbox, task.Ticket = "proveo-claudecode-8abaedd2", "proveo-claudecode-8abaedd2", ""
	m.metadata[metadataKey(*task)] = taskMetadata{pr: prInfo{Number: 3949, State: "OPEN", Review: "APPROVED",
		Title: "TopHat: Add Ashby and Tipalti connectors", Tickets: []string{"CONN-116", "CONN-114"}, Plan: "_spec/_plans/connectors.md"}}
	m.selected, m.details = 2, true
	got := plain(frame(m))
	for _, want := range []string{"TopHat: Add Ashby", "CONN-116 +1", "PR #3949 · approved", "named in PR", "_spec/_plans/connectors.md"} {
		if !strings.Contains(got, want) {
			t.Errorf("lacks %q:\n%s", want, got)
		}
	}
}

func TestMetadataFillsEveryCardOneAtATime(t *testing.T) {
	m := sampleModel(120, 40, ui.GlyphsSafe)
	first := m.task()
	m.metadata[metadataKey(first)] = taskMetadata{id: first.ID, key: metadataKey(first), at: time.Now()}
	if cmd := m.nextMetadata(); cmd == nil || !m.metadataPending {
		t.Fatal("with the selected task fresh, the next stale card should be read")
	}
	if cmd := m.nextMetadata(); cmd != nil {
		t.Error("a second lookup started while one was in flight")
	}
	if got := agentLabel("proveo-claudecode"); got != "Claude Code" {
		t.Errorf("proveo's own kit reads %q", got)
	}
}

func TestACardStacksEveryPRItsAgesAndAResourcesBand(t *testing.T) {
	m := sampleModel(120, 40, ui.GlyphsSafe)
	task := &m.data.Tasks[2]
	task.Sandbox, task.VMID, task.Guest = "proveo-claudecode-1b3fcb7f", "vm-2", true
	m.metadata[metadataKey(*task)] = taskMetadata{
		linear: linearIssue{CreatedAt: sampleNow.Add(-72 * time.Hour).Format(time.RFC3339)},
		pr:     prInfo{Number: 3975, Tickets: []string{"CONN-120"}},
		prs: []prInfo{
			{Number: 3975, State: "OPEN", Head: "rs-cloud-accounts-ui", Created: sampleNow.Add(-5 * time.Hour)},
			{Number: 3974, State: "OPEN", Draft: true, Head: "rs-cloud-revoke-reach", Created: sampleNow.Add(-7 * time.Hour)},
		}}
	m.samples = map[string]guestSample{task.ID: {id: task.ID, sandbox: task.Sandbox, vmid: task.VMID, busy: 0.4, cores: 4,
		ram: 2 << 30, ramTotal: 8 << 30, disk: 6 << 30, uptime: 3 * time.Hour, at: sampleNow, available: true}}
	card := plain(m.taskCard(2, *task, 40))
	for _, want := range []string{"#3975 #3974", "ticket 3d - PR 7h", "Running · …", "-1b3fcb7f", "0.4/4c · 2/8G · 6G · 3h"} {
		if !strings.Contains(card, want) {
			t.Errorf("an unselected card lacks %q:\n%s", want, card)
		}
	}
	lines := m.taskCard(2, *task, 40)
	if band := lines[6]; !strings.Contains(band, "48;") {
		t.Errorf("the resources row has no surface band: %q", band)
	}
	m.selected, m.details = 2, true
	got := plain(frame(m))
	for _, want := range []string{"rs-cloud-accounts-ui · PR #3975", "rs-cloud-revoke-reach · PR #3974 · draft"} {
		if !strings.Contains(got, want) {
			t.Errorf("the details lack %q:\n%s", want, got)
		}
	}
}

func TestHostCapacitySitsInAMutedFrame(t *testing.T) {
	lines := frame(sampleModel(120, 30, ui.GlyphsSafe))
	top, mid, bottom := ansi.Strip(lines[1]), ansi.Strip(lines[2]), ansi.Strip(lines[3])
	if !strings.HasPrefix(top, "╭") || !strings.HasPrefix(mid, "│ c Host capacity") || !strings.HasPrefix(bottom, "╰") {
		t.Fatalf("capacity frame rows:\n%s\n%s\n%s", top, mid, bottom)
	}
	if !strings.HasSuffix(strings.TrimRight(mid, " "), "│") || ansi.StringWidth(top) != 120 {
		t.Errorf("the frame does not span the width: %q", mid)
	}
	if want := ui.ANSI(ui.ColorMuted); !strings.Contains(lines[1], "138;138;138") && !strings.Contains(lines[1], want) {
		t.Errorf("the capacity frame is not muted: %q", lines[1])
	}
}

func TestSamplingFollowsTheAgentWorktreeAndRotates(t *testing.T) {
	m := sampleModel(120, 40, ui.GlyphsSafe)
	for i := range m.data.Tasks[:3] {
		task := &m.data.Tasks[i]
		task.Sandbox, task.VMID, task.Guest, task.Workdir = "sb-"+task.ID, "vm-"+task.ID, true, "/w"
	}
	sel := m.data.Tasks[1]
	m.samples = map[string]guestSample{sel.ID: {id: sel.ID, sandbox: sel.Sandbox, vmid: sel.VMID, at: time.Now(), available: true,
		work: []guestWork{{path: "/w/dev", branch: "rs-cloud-accounts-ui"}, {path: "/w/dev/.claude/worktrees/reach", branch: "rs-cloud-revoke-reach", nested: true}}}}
	if got := m.task(); got.Workdir != "/w/dev" || !slices.Equal(got.Branches, []string{"rs-cloud-accounts-ui", "rs-cloud-revoke-reach"}) {
		t.Errorf("the selected task reads %q %q, want the agent's worktree", got.Workdir, got.Branches)
	}
	stale := m.data.Tasks[2]
	stale.Sandbox = "other"
	if got := m.withGuestWork(stale); got.Workdir != "/w" {
		t.Errorf("a sample from another sandbox moved the workdir to %q", got.Workdir)
	}
	var probed []string
	run := guestScript
	t.Cleanup(func() { guestScript = run })
	guestScript = func(t Task, _ string, _ ...string) ([]byte, error) {
		probed = append(probed, t.ID)
		return nil, nil
	}
	live := liveGuest
	t.Cleanup(func() { liveGuest = live })
	liveGuest = func(Task) bool { return true }
	if cmd := m.nextSample(); cmd == nil {
		t.Fatal("no sample was scheduled")
	}
	if m.sampleAt < 0 || m.data.Tasks[m.taskOrder()[m.sampleAt]].ID == sel.ID {
		t.Errorf("with the selected sample fresh, the rotation stayed on %q", sel.ID)
	}
}
