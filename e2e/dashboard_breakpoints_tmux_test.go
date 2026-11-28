//go:build e2e && !windows

// SPEC: _spec/_paradigms/dashboard-terminal-design.puml
package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"gopkg.in/yaml.v3"

	"github.com/proveo-ca/proveo/internal/schedule"
)

func TestDashboardAdaptsToTerminalSizesOnTmux(t *testing.T) {
	requireTmux(t)
	bin, home, work, tools := buildProveo(t), t.TempDir(), t.TempDir(), t.TempDir()
	work, err := filepath.EvalSymlinks(work)
	if err != nil {
		t.Fatal(err)
	}
	put := func(path string, data []byte) {
		t.Helper()
		mkdirAll(t, filepath.Dir(path))
		writeFile(t, path, data)
	}
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.email=e2e@proveo.test", "-c", "user.name=e2e"}, args...)...)
		cmd.Dir = work
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	for _, f := range []string{"internal/backend/sandbox/session_by_pwd.go", "internal/backend/sandbox/teardown.go", "internal/run/run.go", "README.md"} {
		put(filepath.Join(work, f), []byte("v1\n"))
	}
	git("init", "-q")
	git("add", "-A")
	git("commit", "-qm", "base")
	base := git("rev-parse", "HEAD")
	git("rm", "-q", "internal/backend/sandbox/session_by_pwd.go")
	put(filepath.Join(work, "internal/backend/sandbox/checkpoint.go"), []byte("v2\n"))
	put(filepath.Join(work, "internal/backend/sandbox/teardown.go"), []byte("v2\n"))
	git("add", "-A")
	git("commit", "-qm", "tip")
	tip := git("rev-parse", "HEAD")
	git("reset", "-q", "--hard", base)

	const job, name = "prv-284", "codex-prv-284-history-83f91a"
	git("update-ref", "refs/proveo/run1/proveo/"+name, tip)
	prompt := filepath.Join(tools, "prompt.txt")
	writeFile(t, prompt, []byte("Persist native session history"))
	config, err := yaml.Marshal(schedule.Config{Jobs: map[string]schedule.Job{
		job: {Target: "codex", New: true, Task: "Persist instance history", Ticket: "PRV-284", Workdir: work, PromptFile: prompt},
	}})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, schedule.Path(home), config)
	host, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	record, err := json.Marshal(map[string]string{
		"ID": "codex-83f91a", "Name": name, "VMID": "vm-83f91a", "Target": "codex",
		"Workspace": work, "Repo": work, "Base": base, "Host": host, "Job": job,
	})
	if err != nil {
		t.Fatal(err)
	}
	put(filepath.Join(home, "instances", name, "instance.json"), record)
	finished := time.Now().Add(-time.Hour)
	result, err := json.Marshal(schedule.Result{Job: job, Entry: "manual", Started: finished.Add(-time.Minute), Finished: finished, Outcome: "done"})
	if err != nil {
		t.Fatal(err)
	}
	put(filepath.Join(schedule.LogDir(home, job), "2026-11-24T05-43-00.json"), result)
	if err := os.WriteFile(filepath.Join(tools, "sbx"), []byte("#!/bin/sh\n"+`case "$1" in
settings) printf '{"value":"32g"}\n' ;;
ls) printf '[]\n' ;;
df|daemon) printf '{}\n' ;;
esac
`), 0o755); err != nil {
		t.Fatal(err)
	}

	tmux := dashboardTmux(t)
	sess := schedule.NewTmux("dashboard-sizes", tmux)
	err = sess.Start(120, 40, "env", "TERM=xterm-256color", "COLORTERM=truecolor", "PROVEO_HOME="+home,
		"PROVEO_STATE_ROOT="+home, "PROVEO_SBX_STORAGE="+home, "PROVEO_SBX_DISK_POOL=512g",
		"PATH="+tools+string(os.PathListSeparator)+os.Getenv("PATH"), "sh", "-c", `cd "$1" && exec "$2"`, "sh", work, bin)
	if err != nil {
		t.Fatal(err)
	}
	waitDashboardPane(t, sess, "Ready for review")
	waitDashboardPane(t, sess, "checkpoint.go")

	// settle resizes the pane and polls until the frame meets ok.
	settle := func(t *testing.T, w, h int, ok func([]string) string) []string {
		t.Helper()
		if _, err := tmux("resize-window", "-t", sess.Name, "-x", strconv.Itoa(w), "-y", strconv.Itoa(h)); err != nil {
			t.Fatal(err)
		}
		var lines []string
		why := "no capture"
		if !waitUntil(10*time.Second, func() bool {
			raw, err := sess.Capture()
			if err != nil {
				why = err.Error()
				return false
			}
			lines = expandTabs(strings.Split(strings.TrimRight(raw, "\n"), "\n"), w)
			why = fitsPane(lines, w, h)
			if why == "" {
				why = ok(lines)
			}
			return why == ""
		}) {
			t.Fatalf("%dx%d: %s\n%s", w, h, why, strings.Join(lines, "\n"))
		}
		return lines
	}
	row := func(lines []string, prefix string) (int, string) {
		for i, line := range lines {
			if strings.Contains(line, "╭─ "+prefix) || strings.HasPrefix(line, prefix) {
				return i, line
			}
		}
		return -1, ""
	}
	has := func(want ...string) func([]string) string {
		return func(lines []string) string {
			text := strings.Join(lines, "\n")
			for _, w := range want {
				if !strings.Contains(text, w) {
					return fmt.Sprintf("missing %q", w)
				}
			}
			return ""
		}
	}
	split := func(w int) func([]string) string {
		return func(lines []string) string {
			_, line := row(lines, "Task Dashboard")
			left, _, ok := strings.Cut(line, "╭─ Task Dashboard")
			if want := (w - 1) * 30 / 100; !ok || ansi.StringWidth(left) != want+1 {
				return fmt.Sprintf("the gap between the panes at %d, want %d: %q", ansi.StringWidth(left)-1, want, line)
			}
			return ""
		}
	}

	for _, c := range []struct {
		w, h int
		ok   func([]string) string
	}{
		{39, 9, has("Terminal too small", "39×9", "Ctrl+C quit")},
		{50, 20, func(lines []string) string {
			if _, line := row(lines, "Task Dashboard"); line == "" || strings.Contains(line, "│") {
				return fmt.Sprintf("single pane should not split: %q", line)
			}
			return has("Persist instance history", "Ctrl+C quit")(lines)
		}},
		{84, 46, func(lines []string) string {
			changes, _ := row(lines, "Changes")
			queue, _ := row(lines, "Task Dashboard")
			if changes < 0 || queue <= changes {
				return fmt.Sprintf("portrait: Changes at %d, Task Dashboard at %d", changes, queue)
			}
			return has("Persist instance history", "Ctrl+C quit")(lines)
		}},
		{100, 14, func(lines []string) string {
			if strings.Contains(strings.Join(lines, "\n"), "Host capacity") {
				return "short frames drop the capacity row"
			}
			if why := split(100)(lines); why != "" {
				return why
			}
			return has("Persist instance history", "Ctrl+C quit")(lines)
		}},
		{80, 24, nil}, {80, 25, nil}, {120, 30, nil}, {132, 43, nil}, {160, 48, nil}, {200, 50, nil},
	} {
		t.Run(fmt.Sprintf("%dx%d", c.w, c.h), func(t *testing.T) {
			ok := c.ok
			if ok == nil {
				ok = func(lines []string) string {
					if why := split(c.w)(lines); why != "" {
						return why
					}
					return has("Persist instance history", "Ready for review", "Ctrl+C quit")(lines)
				}
			}
			settle(t, c.w, c.h, ok)
		})
	}

	settle(t, 120, 40, has("Task Dashboard", "teardown.go"))
	if err := sess.SendKeys("Tab", "/"); err != nil {
		t.Fatal(err)
	}
	if err := sess.SendText("chk"); err != nil {
		t.Fatal(err)
	}
	filtered := settle(t, 120, 40, has("/ chk", "1 shown", "checkpoint.go"))
	if strings.Contains(strings.Join(filtered, "\n"), "teardown.go") {
		t.Errorf("the filter kept a non-matching file:\n%s", strings.Join(filtered, "\n"))
	}
	if err := sess.SendKeys("Escape"); err != nil {
		t.Fatal(err)
	}
	settle(t, 120, 40, has("teardown.go", "f See files"))
	if err := sess.SendKeys("-"); err != nil {
		t.Fatal(err)
	}
	settle(t, 120, 40, has("▸ …", "sandbox  +1 ~1 -1"))

	settle(t, 39, 9, has("Terminal too small"))
	if err := sess.SendKeys("C-c"); err != nil {
		t.Fatal(err)
	}
	waitDashboardPane(t, sess, "Exit")
	if err := sess.SendKeys("Right", "Enter"); err != nil {
		t.Fatal(err)
	}
	if !waitUntil(5*time.Second, func() bool { return !sess.Alive() }) {
		t.Fatal("Ctrl+C on the too-small card should still exit")
	}
}

// fitsPane reports the first row that overflows the pane, or "".
func fitsPane(lines []string, w, h int) string {
	if len(lines) > h {
		return fmt.Sprintf("%d rows in a %d-row pane", len(lines), h)
	}
	for i, line := range lines {
		if got := ansi.StringWidth(line); got > w {
			return fmt.Sprintf("row %d is %d cells in a %d-cell pane", i, got, w)
		}
	}
	return ""
}

// expandTabs turns recorded tabs back into cells, clamped to the last column.
func expandTabs(lines []string, w int) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		var b strings.Builder
		col := 0
		for _, r := range line {
			if r == '\t' {
				n := max(0, min(8-col%8, w-1-col))
				b.WriteString(strings.Repeat(" ", n))
				col += n
				continue
			}
			b.WriteRune(r)
			col += ansi.StringWidth(string(r))
		}
		out[i] = b.String()
	}
	return out
}
