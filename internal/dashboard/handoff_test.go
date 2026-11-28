// SPEC: _spec/_paradigms/dashboard-terminal-design.puml
package dashboard

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/proveo-ca/proveo/internal/ui"
)

func TestAHandoffPaintsItsCardOnTheAltScreenBeforeTheProgram(t *testing.T) {
	var out bytes.Buffer
	h := &handoff{cmd: exec.Command("sh", "-c", "printf PROGRAM"), card: "card line\nsecond"}
	h.SetStdout(&out)
	if err := h.Run(); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	enter := strings.Index(got, ansi.SetModeAltScreenSaveCursor)
	card, program := strings.Index(got, "card line\r\nsecond"), strings.Index(got, "PROGRAM")
	leave := strings.LastIndex(got, ansi.ResetModeAltScreenSaveCursor)
	if enter != 0 || card < enter || program < card || leave < program {
		t.Errorf("order: alt %d, card %d, program %d, leave %d in %q", enter, card, program, leave, got)
	}
}

func TestTheLoadingCardIsCentredAndFillsTheFrame(t *testing.T) {
	m := sampleModel(100, 30, ui.GlyphsSafe)
	frame := m.loadingCard("Attaching to Persist instance history", "proveo-codex-prv-284", hint("Ctrl+B d", "detaches back here"), "●")
	lines := strings.Split(frame, "\n")
	if len(lines) != 30 {
		t.Fatalf("%d rows, want 30", len(lines))
	}
	for i, l := range lines {
		if ansi.StringWidth(l) != 100 {
			t.Errorf("row %d is %d cells", i, ansi.StringWidth(l))
		}
	}
	got := ansi.Strip(frame)
	for _, want := range []string{"● Attaching to Persist instance history", "proveo-codex-prv-284", "Ctrl+B d detaches back here", "╭", "╰"} {
		if !strings.Contains(got, want) {
			t.Errorf("card lacks %q:\n%s", want, got)
		}
	}
	if row := strings.Index(got, "Attaching") / 101; row < 12 || row > 16 {
		t.Errorf("title on row %d; the card should sit mid-frame", row)
	}
}

func TestABlankSbxFrameShowsLoadingOnlyAfterADelay(t *testing.T) {
	m := sampleModel(100, 30, ui.GlyphsSafe)
	if _, loading := m.sbxLoading("   \n  "); loading {
		t.Error("a frame blank for no time already shows loading")
	}
	m.childBlankAt = sampleNow.Add(-time.Second)
	card, loading := m.sbxLoading("")
	if !loading || !strings.Contains(ansi.Strip(card), "Starting sbx") || !strings.Contains(ansi.Strip(card), "Ctrl+G Tab back to proveo") {
		t.Fatalf("first start:\n%s", ansi.Strip(card))
	}
	if n := len(strings.Split(card, "\n")); n != 29 {
		t.Errorf("the card takes %d rows; the header keeps one of 30", n)
	}
	if _, loading := m.sbxLoading("\x1b[1msbx\x1b[0m list"); loading || !m.childPainted || !m.childBlankAt.IsZero() {
		t.Error("a painted frame still shows loading")
	}
	m.childBlankAt = sampleNow.Add(-time.Second)
	if card, _ := m.sbxLoading(""); !strings.Contains(ansi.Strip(card), "Waiting for sbx") {
		t.Errorf("after sbx drew once, a blank frame is an attach or return:\n%s", ansi.Strip(card))
	}
}

func TestConfirmingAnAttachHandsOffBehindACard(t *testing.T) {
	m := sampleModel(120, 40, ui.GlyphsSafe)
	m.selected = 2
	m.enter()
	if m.confirm == nil {
		t.Fatal("enter on a live task asks first")
	}
	m.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("confirming the attach ran nothing")
	}
	if spin := m.spinner(); spin == "" {
		t.Error("no spinner frame")
	}
}

func TestTheTreeShowsALoadingCardOnlyAfterABeat(t *testing.T) {
	m := sampleModel(120, 30, ui.GlyphsSafe)
	m.changes = nil
	m.changesLoad = treeLoad{req: 1, pending: true, since: sampleNow.Add(-50 * time.Millisecond)}
	pane := ansi.Strip(strings.Join(m.renderFiles(40, 20), "\n"))
	if strings.Contains(pane, "No changes") || strings.Contains(pane, "Reading changes") {
		t.Errorf("a fresh read already says something:\n%s", pane)
	}
	m.changesLoad.since = sampleNow.Add(-time.Second)
	pane = ansi.Strip(strings.Join(m.renderFiles(40, 20), "\n"))
	for _, want := range []string{"Reading changes", "from the carried tip 1c2f6e4", "╭"} {
		if !strings.Contains(pane, want) {
			t.Errorf("the loading card lacks %q:\n%s", want, pane)
		}
	}
	if strings.Contains(pane, "No changes in this task") {
		t.Errorf("a read in flight claims there are no changes:\n%s", pane)
	}
	m.showFiles = true
	m.filesLoad = treeLoad{req: 1, pending: true, since: sampleNow.Add(-time.Second)}
	if pane := ansi.Strip(strings.Join(m.renderFiles(40, 20), "\n")); !strings.Contains(pane, "Listing files") {
		t.Errorf("files read:\n%s", pane)
	}
}

func TestARefreshSpinsInTheHeaderUnlessItIsTheBackgroundPoll(t *testing.T) {
	m := sampleModel(120, 30, ui.GlyphsSafe)
	m.changesLoad = treeLoad{req: 1, pending: true, since: sampleNow.Add(-time.Second)}
	head := func() string { return ansi.Strip(m.renderFiles(60, 20)[0]) }
	if !strings.Contains(head(), m.spinner()) {
		t.Errorf("a refresh the operator asked for shows no spinner: %q", head())
	}
	m.changesLoad.quiet = true
	if strings.Contains(head(), m.spinner()) {
		t.Errorf("the 5 s poll spins the header: %q", head())
	}
	if body := ansi.Strip(strings.Join(m.renderFiles(60, 20), "\n")); !strings.Contains(body, "teardown.go") {
		t.Errorf("a refresh hid the rows already shown:\n%s", body)
	}
}

func TestOnlyTheLatestReadEndsTheLoading(t *testing.T) {
	m := sampleModel(120, 30, ui.GlyphsSafe)
	first := m.changesLoad.start(sampleNow, false)
	second := m.changesLoad.start(sampleNow.Add(time.Second), true)
	if m.changesLoad.since != sampleNow || m.changesLoad.quiet {
		t.Errorf("a quiet read on top of a loud one = %+v", m.changesLoad)
	}
	task := m.task()
	m.Update(changesEvent{id: task.ID, revision: task.Tip, req: first})
	if !m.changesLoad.pending {
		t.Error("a stale reply ended the newer read")
	}
	m.Update(changesEvent{id: task.ID, revision: task.Tip, req: second})
	if m.changesLoad.pending {
		t.Error("the latest reply left the tree loading")
	}
	if cmd := m.loadTick(); cmd != nil {
		t.Error("the spinner keeps ticking with nothing in flight")
	}
	m.filesLoad.start(sampleNow, false)
	if cmd := m.loadTick(); cmd == nil || m.loadTick() != nil {
		t.Error("one tick chain should run while a read is in flight")
	}
}

func TestABusySandboxGetsTimeAndSaysWhyWhenItFails(t *testing.T) {
	if timeoutFor(guestChangesScript) != time.Minute || timeoutFor(guestFilesScript) != time.Minute || timeoutFor(usageProbe) != 6*time.Second {
		t.Error("tree reads need a minute on a loaded guest; sampling stays short")
	}
	live, run := liveGuest, guestScript
	t.Cleanup(func() { liveGuest, guestScript = live, run })
	liveGuest = func(Task) bool { return true }
	guestScript = func(Task, string, ...string) ([]byte, error) { return nil, context.DeadlineExceeded }
	task := Task{ID: "sbx:1", Sandbox: "proveo-claudecode-1b3fcb7f", Guest: true, Workdir: "/w"}
	if _, err := Changes(task, "", ""); err == nil || !strings.Contains(err.Error(), "busy") {
		t.Errorf("changes error = %v; with no carried copy the timeout must surface", err)
	}
	if _, _, err := TaskFiles(task); err == nil {
		t.Error("files hid the guest failure")
	}

	m := sampleModel(120, 30, ui.GlyphsSafe)
	m.changes = nil
	sel := &m.data.Tasks[m.selected]
	sel.Sandbox, sel.VMID, sel.Guest = "proveo-claudecode-1b3fcb7f", "vm", true
	m.samples = map[string]guestSample{sel.ID: {id: sel.ID, sandbox: sel.Sandbox, vmid: sel.VMID, available: true, busy: 13.9, cores: 14}}
	m.changesLoad = treeLoad{req: 3, pending: true, since: sampleNow.Add(-23 * time.Second)}
	pane := ansi.Strip(strings.Join(m.renderFiles(50, 20), "\n"))
	for _, want := range []string{"Reading changes · 23s", "from proveo-claudecode-1b3fcb7f", "sandbox busy · CPU 13.9/14 cores"} {
		if !strings.Contains(pane, want) {
			t.Errorf("the card lacks %q:\n%s", want, pane)
		}
	}
	m.quietReads = true
	if cmd := m.readChanges(m.task()); cmd != nil || m.changesLoad.req != 3 {
		t.Error("the background poll restarted a read still running; on a loaded guest none would ever finish")
	}
	m.quietReads = false
	m.Update(changesEvent{id: m.task().ID, revision: m.task().Tip, req: 3, err: errors.New("the sandbox took over a minute to answer; it is busy")})
	pane = ansi.Strip(strings.Join(m.renderFiles(50, 20), "\n"))
	if !strings.Contains(pane, "Couldn't read changes: the sandbox took over a") || strings.Contains(pane, "No changes in this task") {
		t.Errorf("a failed read reads as no changes:\n%s", pane)
	}
}

func TestQuestionMarkOpensTheKeysAsAModalAndClosesIt(t *testing.T) {
	m := sampleModel(120, 40, ui.GlyphsSafe)
	press := func(code rune, text string) { m.Update(tea.KeyPressMsg{Code: code, Text: text}) }
	press('?', "?")
	if m.helpModal == nil {
		t.Fatal("? opened nothing")
	}
	out := ansi.Strip(m.View().Content)
	for _, want := range []string{"Keys", "Reading a file", "n p", "next · previous file", "Close"} {
		if !strings.Contains(out, want) {
			t.Errorf("the help modal lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Task Dashboard") {
		t.Errorf("the modal draws over a cleared frame like the confirmations:\n%s", out)
	}
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	if m.helpModal == nil || !strings.Contains(ansi.Strip(m.View().Content), "Anywhere") {
		t.Error("a resize lost the help or its last section")
	}
	press('?', "?")
	if m.helpModal != nil {
		t.Error("? did not close the help")
	}
	press('?', "?")
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.helpModal != nil {
		t.Error("esc did not close the help")
	}
}

func TestASandboxHostedInARunSessionAttachesToThatSession(t *testing.T) {
	m := sampleModel(120, 40, ui.GlyphsSafe)
	task := &m.data.Tasks[2]
	task.Live, task.Session, task.Sandbox = true, "proveo-run-claudecode-abc123", "proveo-claudecode-1b3fcb7f"
	m.selected = 2
	m.enter()
	if m.confirm == nil || m.attachID != "proveo-run-claudecode-abc123" {
		t.Fatalf("enter on a run-hosted sandbox: confirm %v, attach %q", m.confirm != nil, m.attachID)
	}
	m.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	if _, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd == nil {
		t.Error("confirming attached nothing")
	}
}
