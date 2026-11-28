package dashboard

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/proveo-ca/proveo/internal/activity"
	"github.com/proveo-ca/proveo/internal/schedule"
	"github.com/proveo-ca/proveo/internal/ui"
)

func endedModel(t *testing.T) (*dashModel, *Task) {
	t.Helper()
	m := sampleModel(140, 44, ui.GlyphsSafe)
	m.selected = 0
	ended := &m.data.Tasks[0]
	ended.Job, ended.DashboardJob, ended.Instance, ended.Sandbox = ended.ID, true, "e9cb29effa7dbb76afc4", "claudecode-review-399-e9cb29effa"
	if !m.endedRun(*ended) {
		t.Fatalf("fixture is not an ended run: %+v", *ended)
	}
	return m, ended
}

func TestAnEndedRunOffersFollowUpRetryAndCleanUp(t *testing.T) {
	m, ended := endedModel(t)
	if actions := ansi.Strip(m.cardActions(*ended, true)); !strings.HasPrefix(actions, "↵ follow up   r retry   x clean up") {
		t.Errorf("actions = %q", actions)
	}
	if foot := ansi.Strip(m.footer(160)); !strings.Contains(foot, "↵ Follow up") || !strings.Contains(foot, "r retry") || !strings.Contains(foot, "x clean up") {
		t.Errorf("footer = %q", foot)
	}
	ended.Outcome = "done"
	if actions := ansi.Strip(m.cardActions(*ended, true)); strings.Contains(actions, "retry") || !strings.Contains(actions, "x clean up") {
		t.Errorf("a run that finished offers retry, or lost clean up: %q", actions)
	}
	ended.DashboardJob = false
	if actions := ansi.Strip(m.cardActions(*ended, true)); strings.Contains(actions, "clean up") {
		t.Errorf("a scheduled job from the schedule file offers clean up: %q", actions)
	}
	ended.Live = true
	if m.endedRun(*ended) {
		t.Error("a task with a live session counts as ended")
	}
}

func TestFollowUpResumesTheConversationInItsOwnInstance(t *testing.T) {
	m, ended := endedModel(t)
	ended.Agent = "claudecode"
	if got := strings.Join(followUpArgs(*ended), " "); got != "run claudecode --instance e9cb29effa7dbb76afc4 --continue" {
		t.Errorf("args = %q", got)
	}
	review := *ended
	review.Title, review.Ticket, review.Phase = "Review #3995: Hold E2E", "https://github.com/acme/app/pull/3995", "Review"
	if got := followUpArgs(review); !slices.Contains(got, "--read-only-shares") {
		t.Errorf("a review follows up without its read-only shares: %q", got)
	}
	m.enter()
	view := ansi.Strip(m.View().Content)
	for _, want := range []string{"Follow up", "Cancel", "proveo run claudecode --instance e9cb29effa7dbb76afc4 --continue", "Ctrl+B d"} {
		if !strings.Contains(view, want) {
			t.Errorf("the follow-up card lacks %q:\n%s", want, view)
		}
	}
	if _, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd != nil || m.confirm != nil || m.onConfirm != nil {
		t.Error("enter on the untouched card did not cancel")
	}
	m.enter()
	m.Update(tea.KeyPressMsg{Code: 'f', Text: "f"})
	if _, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd == nil || m.onConfirm != nil {
		t.Error("accepting the follow-up handed nothing off")
	}
	ended.Instance = ""
	m.enter()
	view = ansi.Strip(m.View().Content)
	if !strings.Contains(view, "Nothing to follow up") || !strings.Contains(view, "r  runs the job again") || !strings.Contains(view, "x  cleans it up") {
		t.Errorf("enter on a run with no instance:\n%s", view)
	}
}

func fakeProveo(t *testing.T, reply string) (bin, calls string) {
	t.Helper()
	bin, calls = filepath.Join(t.TempDir(), "proveo"), filepath.Join(t.TempDir(), "calls")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho \"$@\" >> \""+calls+"\"\n"+reply+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, calls
}

func endedEvents(cmd tea.Cmd) []endedEvent {
	var out []endedEvent
	for _, msg := range runCmd(cmd) {
		if e, ok := msg.(endedEvent); ok {
			out = append(out, e)
		}
	}
	return out
}

func TestRetryStartsTheJobAgainOnce(t *testing.T) {
	m, ended := endedModel(t)
	bin, calls := fakeProveo(t, "echo started")
	m.Binary, m.CWD = bin, t.TempDir()
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	if !m.ending[ended.ID] {
		t.Fatal("r did not start a retry")
	}
	if _, again := m.Update(tea.KeyPressMsg{Code: 'r', Text: "r"}); again != nil {
		t.Error("a second r ran another retry while the first runs")
	}
	events := endedEvents(cmd)
	if len(events) != 1 || events[0].err != nil {
		t.Fatalf("events = %+v", events)
	}
	if b, _ := os.ReadFile(calls); strings.TrimSpace(string(b)) != "schedule retry plan-17" {
		t.Errorf("ran %q", b)
	}
	m.Update(events[0])
	if m.ending[ended.ID] || !strings.Contains(m.status, "Started again") {
		t.Errorf("status %q, ending %v", m.status, m.ending)
	}
	ended.Outcome = "done"
	if _, cmd := m.Update(tea.KeyPressMsg{Code: 'r', Text: "r"}); cmd != nil {
		t.Error("r retried a run that finished")
	}
}

func TestCleanUpAsksThenForgetsTheRunAndSaysWhyItFailed(t *testing.T) {
	m, ended := endedModel(t)
	ended.Title, ended.Ticket, ended.Phase = "Review #3995: Hold E2E", "https://github.com/acme/app/pull/3995", "Review"
	bin, calls := fakeProveo(t, "echo 'error: sandbox still holds unpushed work' >&2; exit 1")
	m.Binary, m.CWD = bin, t.TempDir()
	m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	view := ansi.Strip(m.View().Content)
	for _, want := range []string{"Clean up", "sandbox claudecode-review-399-e9cb29effa", "instance e9cb29effa7dbb76afc4",
		"job plan-17", "Keeps    the logs", "#3995 returns to Review requests"} {
		if !strings.Contains(view, want) {
			t.Errorf("the clean-up card lacks %q:\n%s", want, view)
		}
	}
	if _, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEsc}); cmd != nil || m.ending[ended.ID] {
		t.Error("esc cleaned up")
	}
	m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.ending[ended.ID] {
		t.Fatal("accepting did not start the clean-up")
	}
	events := endedEvents(cmd)
	if len(events) != 1 || events[0].err == nil {
		t.Fatalf("events = %+v", events)
	}
	if b, _ := os.ReadFile(calls); strings.TrimSpace(string(b)) != "schedule drop plan-17" {
		t.Errorf("ran %q", b)
	}
	m.Update(events[0])
	if !m.alert || !strings.Contains(m.status, "clean up: error: sandbox still holds unpushed work") || m.ending[ended.ID] {
		t.Errorf("status %q alert %v", m.status, m.alert)
	}
}

func TestAFailedJobWithItsSandboxStillUpIsEndedNotInAnotherTerminal(t *testing.T) {
	m, ended := endedModel(t)
	ended.Guest, ended.VMID, ended.Outcome = true, "a0977046", "failed"
	ended.Reason = "the agent exited 1 before it was ready: host Chrome from sandbox " + ended.Sandbox
	if !m.endedRun(*ended) {
		t.Fatal("a job whose run recorded its end reads as live while its sandbox is kept")
	}
	if actions := ansi.Strip(m.cardActions(*ended, true)); !strings.HasPrefix(actions, "↵ follow up   r retry   x clean up") {
		t.Errorf("actions = %q", actions)
	}
	m.enter()
	if view := ansi.Strip(m.View().Content); strings.Contains(view, "another terminal") || !strings.Contains(view, "--continue") {
		t.Errorf("enter on a kept sandbox of an ended run:\n%s", view)
	}
	m.confirm, m.onConfirm = nil, nil
	ended.Job, ended.Outcome = "", ""
	if m.endedRun(*ended) {
		t.Error("a running guest with no job and no recorded end counts as ended; it may be an agent in another terminal")
	}
}

func TestASandboxWhoseSeedRefusedTheAgentIsEndedAndCleansUpWithStop(t *testing.T) {
	m := sampleModel(140, 44, ui.GlyphsSafe)
	lone := Task{ID: "87c69e8816aeb45b5207", Instance: "87c69e8816aeb45b5207", Title: "feat(integrations): add Teamwork Projects connector",
		Agent: "claudecode", Sandbox: "claudecode-feat-integ-87c69e8816", VMID: "vm", Guest: true, State: schedule.Running, Phase: "Working"}
	m.data.Tasks = append(m.data.Tasks, lone)
	m.selected = len(m.data.Tasks) - 1
	raw := "cpu=1.0\nseed\trefused\t_spec is read-only, so edits cannot reach the host\n"
	s := guestSample{id: lone.ID, sandbox: lone.Sandbox, vmid: lone.VMID, available: true, at: sampleNow, seedRefused: parseSeedRefused(raw), activity: activity.Exited}
	m.samples = map[string]guestSample{lone.ID: s}
	if st := m.statusOf(lone); st.label != "Seed refused: _spec is read-only, so edits cannot reach the host" {
		t.Errorf("status = %q", st.label)
	}
	if !m.endedRun(lone) || m.canFollowUp(lone) {
		t.Fatalf("ended %v, follow up %v; a refused seed leaves nothing to attach to and refuses again", m.endedRun(lone), m.canFollowUp(lone))
	}
	if actions := ansi.Strip(m.cardActions(lone, true)); !strings.HasPrefix(actions, "x clean up") || strings.Contains(actions, "follow up") {
		t.Errorf("actions = %q", actions)
	}
	m.enter()
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "refused to start the agent: _spec is read-only") || strings.Contains(view, "another terminal") {
		t.Errorf("enter:\n%s", view)
	}
	m.confirm = nil
	bin, calls := fakeProveo(t, "true")
	m.Binary, m.CWD = bin, t.TempDir()
	m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	for _, msg := range runCmd(cmd) {
		m.Update(msg)
	}
	if b, _ := os.ReadFile(calls); strings.TrimSpace(string(b)) != "schedule drop 87c69e8816aeb45b5207 --stop" {
		t.Errorf("ran %q; a running sandbox needs --stop", b)
	}
	if !strings.Contains(guestActivityScript, "/dev/shm/proveo-seed-refused") {
		t.Error("the guest probe does not read the seed's refusal")
	}
}
