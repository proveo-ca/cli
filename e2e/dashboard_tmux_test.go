//go:build e2e && !windows

// SPEC: _spec/internal/choiceui/confirm-dialogs.puml, _spec/internal/schedule/schedule.puml
package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/proveo-ca/proveo/internal/schedule"
	"github.com/proveo-ca/proveo/internal/testx"
)

func TestDashboardDialogStyleBaselineOnTmux(t *testing.T) {
	requireTmux(t)
	t.Parallel()
	tmux := dashboardTmux(t)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	sess := schedule.NewTmux("post-exit", tmux)
	done := filepath.Join(t.TempDir(), "answer")
	if err := sess.Start(120, 40, "env", "TERM=xterm-256color", "PROVEO_GLYPHS=safe",
		"PROVEO_LIVE_POSTEXIT_HELPER="+done, binary, "-test.run=^TestLivePostExitPromptHelper$",
		"-test.timeout=45s"); err != nil {
		t.Fatal(err)
	}

	golden := testx.Open(t, "dashboard-dialog-tmux")
	frame := waitDashboardPane(t, sess, "Leave ↵")
	golden.Assert("safe-default.txt", dashboardDialogCard(frame))
	if err := sess.SendKeys("Right"); err != nil {
		t.Fatal(err)
	}
	frame = waitDashboardPane(t, sess, "Fetch ↵")
	golden.Assert("other-selected.txt", dashboardDialogCard(frame))
}

func dashboardDialogCard(frame string) string {
	var lines []string
	for line := range strings.SplitSeq(frame, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "╭") || strings.HasPrefix(line, "│") ||
			strings.HasPrefix(line, "├") || strings.HasPrefix(line, "╰") {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n") + "\n"
}

func TestDashboardAttachBaselineOnTmux(t *testing.T) {
	requireTmux(t)
	t.Parallel()
	binary := buildProveo(t)
	for _, tc := range []struct {
		name      string
		inside    bool
		returnKey string
	}{
		{name: "direct-detach", returnKey: "d"},
		{name: "switch-back-inside-tmux", inside: true, returnKey: "L"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tmux := dashboardTmux(t)
			const job = "dashboard-e2e"
			agent := schedule.NewTmux(schedule.SessionName(job), tmux)
			if err := agent.Start(120, 40, "sh", "-c", `printf 'AGENT_READY\n'
while IFS= read -r line; do printf 'AGENT_RECEIVED: %s\n' "$line"; done`); err != nil {
				t.Fatal(err)
			}
			waitDashboardPane(t, agent, "AGENT_READY")
			agentPID, err := agent.PanePID()
			if err != nil {
				t.Fatal(err)
			}

			const invoke = `printf 'ORIGIN_READY\n'
IFS= read -r line
"$1" attach "$2"
printf 'ATTACH_EXIT=%s\n' "$?"
exec cat`
			command := []string{"env", "TERM=xterm-256color", "PROVEO_HOME=" + t.TempDir(), "PROVEO_STATE_ROOT=" + t.TempDir()}
			driver := []string{"sh", "-c", invoke, "sh", binary, job}
			if tc.inside {
				origin := schedule.NewTmux("origin", tmux)
				if err := origin.Start(120, 40, append(command, driver...)...); err != nil {
					t.Fatal(err)
				}
				driver = []string{"tmux", "attach", "-t", origin.Name}
			}
			terminal := schedule.NewTmux("terminal", tmux)
			if err := terminal.Start(120, 40, append(append(command, "TMUX="), driver...)...); err != nil {
				t.Fatal(err)
			}
			waitDashboardPane(t, terminal, "ORIGIN_READY")
			if err := terminal.Enter(); err != nil {
				t.Fatal(err)
			}
			waitDashboardPane(t, terminal, "AGENT_READY")
			if err := terminal.SendText("before-return"); err != nil {
				t.Fatal(err)
			}
			if err := terminal.Enter(); err != nil {
				t.Fatal(err)
			}
			waitDashboardPane(t, agent, "AGENT_RECEIVED: before-return")
			if err := terminal.SendKeys("C-b", tc.returnKey); err != nil {
				t.Fatal(err)
			}
			waitDashboardPane(t, terminal, "ATTACH_EXIT=0")
			if got, err := agent.PanePID(); err != nil || got != agentPID {
				t.Errorf("proveo attach %s after return: agent pane PID = %q (%v), want %q", job, got, err, agentPID)
			}
		})
	}
}

func waitDashboardPane(t *testing.T, sess *schedule.TmuxSession, want string) string {
	t.Helper()
	frame, err := sess.WaitFor(want, 10*time.Second)
	if err != nil {
		t.Fatalf("capture %s waiting for %q: %v\n%s", sess.Name, want, err, frame)
	}
	return frame
}

func dashboardTmux(t *testing.T) schedule.TmuxRunner {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "proveo-e2e-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	run := func(args ...string) (string, error) {
		cmd := exec.Command("tmux", append([]string{"-f", "/dev/null", "-u"}, args...)...)
		cmd.Env = append(os.Environ(), "TMUX=", "TMUX_TMPDIR="+dir)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	t.Cleanup(func() { _, _ = run("kill-server") })
	return run
}
