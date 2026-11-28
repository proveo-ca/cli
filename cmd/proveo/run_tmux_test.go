// SPEC: _spec/cmd/proveo/run-session.puml
package main

import (
	"os"
	"strings"
	"testing"

	"github.com/proveo-ca/proveo/internal/schedule"
)

func TestTheHostedSessionCarriesThisTerminalsEnvironment(t *testing.T) {
	got := envScript([]string{"PATH=/a:/b", "SSH_AUTH_SOCK=/tmp/agent", "QUOTE=it's", "TMUX=/tmp/x,1,0", "TMUX_PANE=%1",
		"PROVEO_RUN_SESSION=stale", "1BAD=x", "NOEQUALS"}, "proveo-run-claudecode-abc123")
	for _, want := range []string{"export PATH='/a:/b'\n", "export SSH_AUTH_SOCK='/tmp/agent'\n", `export QUOTE='it'\''s'` + "\n",
		"export PROVEO_RUN_SESSION='proveo-run-claudecode-abc123'\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("env script lacks %q:\n%s", want, got)
		}
	}
	for _, gone := range []string{"TMUX=", "TMUX_PANE", "stale", "1BAD", "NOEQUALS"} {
		if strings.Contains(got, gone) {
			t.Errorf("env script carries %q:\n%s", gone, got)
		}
	}
}

func TestOnlyAnInteractiveRunOutsideASessionHostsItsAgentInTmux(t *testing.T) {
	if !schedule.TmuxAvailable() {
		t.Skip("tmux not on PATH")
	}
	prevIn, prevOut := stdinTerminal, stdoutTerminal
	t.Cleanup(func() { stdinTerminal, stdoutTerminal = prevIn, prevOut })
	stdinTerminal, stdoutTerminal = func() bool { return true }, func() bool { return true }
	for _, k := range []string{"PROVEO_RUN_SESSION", "PROVEO_SCHEDULE_JOB", "PROVEO_RUN_TMUX"} {
		t.Setenv(k, "")
		_ = os.Unsetenv(k)
	}
	if !(&runFlags{}).hostsInTmux() {
		t.Fatal("an interactive run should host its agent in tmux")
	}
	for name, f := range map[string]*runFlags{"--print": {printOnly: true}, "--queue": {queue: true}, "--ls": {listSessions: true}} {
		if f.hostsInTmux() {
			t.Errorf("%s hosted in tmux", name)
		}
	}
	for k, v := range map[string]string{"PROVEO_RUN_SESSION": "proveo-run-x", "PROVEO_SCHEDULE_JOB": "nightly", "PROVEO_RUN_TMUX": "off"} {
		t.Setenv(k, v)
		if (&runFlags{}).hostsInTmux() {
			t.Errorf("%s=%s still hosts in tmux", k, v)
		}
		_ = os.Unsetenv(k)
	}
	stdinTerminal = func() bool { return false }
	if (&runFlags{}).hostsInTmux() {
		t.Error("a run without a terminal hosted in tmux")
	}
}

func TestAttachTakesARunSessionByItsOwnName(t *testing.T) {
	scheduleHome(t, "", schedule.Tick{})
	calls := stubTmux(t, 0, "proveo-run-claudecode-abc123")
	_, stderr, err := proveoOut(t, "attach", "proveo-run-claudecode-abc123")
	b, _ := os.ReadFile(calls)
	if err != nil || stderr != "" || !strings.HasPrefix(string(b), "attach -t proveo-run-claudecode-abc123 ") {
		t.Errorf("attach of a run session = %v, stderr %q, tmux %q", err, stderr, b)
	}
	stubTmux(t, 0)
	if _, stderr, _ := proveoOut(t, "attach", "proveo-run-gone-000000"); !strings.Contains(stderr, "proveo-run-gone-000000 — not running") {
		t.Errorf("a gone run session = %q", stderr)
	}
}

func TestHostInTmuxStartsTheSessionWithTheSameCommandAndAttaches(t *testing.T) {
	calls := stubTmux(t, 0)
	stdoutTerminal = func() bool { return false }
	if err := hostInTmux("claudecode", []string{"run", "claudecode", "--clone=false"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(calls)
	got := string(b)
	if !strings.HasPrefix(got, "new-session -d -s proveo-run-claudecode-") || !strings.Contains(got, " run claudecode --clone=false TMUX=") ||
		!strings.Contains(got, "\nattach -t proveo-run-claudecode-") {
		t.Errorf("tmux calls:\n%q", got)
	}
}
