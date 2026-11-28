//go:build e2e && !windows

// SPEC: _spec/_experiments/dashboard/workspace-instances.puml
package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"gopkg.in/yaml.v3"

	"github.com/proveo-ca/proveo/internal/dashboard"
	"github.com/proveo-ca/proveo/internal/schedule"
)

func TestNativeDashboardTabsOnTmux(t *testing.T) {
	requireTmux(t)
	bin := buildProveo(t)
	home, work, tools := t.TempDir(), t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(work, "README.md"), []byte("Dashboard file preview marker\n"))
	sbx := filepath.Join(tools, "sbx")
	const stock = `#!/bin/sh
draw() {
  set -- $(stty size)
  cols=$(($2 / 4 + 3))
  printf '\033[2J\033[H\033[31mStock child pane %s\033[0m\n' "$2"
  i=1
  while [ "$i" -lt "$(($1 - 1))" ]; do
    printf '%*s│Task pane\n' "$cols" ''
    i=$((i + 1))
  done
}
case "$1" in
settings) printf '{"value":"32g"}\n' ;;
df) printf '{}\n' ;;
daemon) printf '{}\n' ;;
"") printf '\033[?1049h'; stty -echo; trap draw WINCH; draw; while :; do sleep 1; done ;;
esac
`
	if err := os.WriteFile(sbx, []byte(stock), 0o755); err != nil {
		t.Fatal(err)
	}
	tmux := dashboardTmux(t)
	sess := schedule.NewTmux("native-dashboard", tmux)
	const line = `cd "$1" || exit
before=$(stty -g)
"$2"
rc=$?
after=$(stty -g)
printf '\nDASHBOARD_EXIT=%s\n' "$rc"
if [ "$before" = "$after" ]; then printf 'TERMINAL_RESTORED\n'; fi
exec cat`
	err := sess.Start(120, 40, "env", "TERM=xterm-256color", "COLORTERM=truecolor", "NO_COLOR=0", "PROVEO_HOME="+home,
		"PROVEO_STATE_ROOT="+home, "PROVEO_SBX_STORAGE="+home, "PATH="+tools+string(os.PathListSeparator)+os.Getenv("PATH"), "sh", "-c", line, "sh", work, bin)
	if err != nil {
		t.Fatal(err)
	}
	waitDashboardPane(t, sess, "Task Dashboard")
	keys := func(keys ...string) {
		t.Helper()
		if err := sess.SendKeys(keys...); err != nil {
			t.Fatal(err)
		}
	}
	keys("t", "c")
	if frame := waitDashboardPane(t, sess, "Disk reservation per sandbox"); strings.Contains(frame, "Task details") {
		t.Fatalf("task details opened in an empty workspace:\n%s", frame)
	}
	keys("2")
	waitDashboardPane(t, sess, "Task graph")
	keys("3")
	waitDashboardPane(t, sess, "Schedule")
	keys("1", "f")
	if frame := waitDashboardPane(t, sess, "Esc back to tasks"); strings.Contains(frame, "README.md") || !strings.Contains(frame, "Select a task to see its") || !strings.Contains(frame, "Files") {
		t.Fatalf("Files must list the task's sandbox, never the host checkout, and offer the way back:\n%s", frame)
	}
	keys("Escape")
	if !waitUntil(5*time.Second, func() bool {
		frame, err := sess.Capture()
		return err == nil && strings.Contains(frame, "Task Dashboard") && !strings.Contains(frame, "Esc back to tasks")
	}) {
		t.Fatal("Esc did not return focus to the tasks")
	}
	keys("C-g", "Tab")
	frame := waitDashboardPane(t, sess, "Stock child pane 120")
	firstSplit := dashboard.SbxDivider(frame, 120)
	if firstSplit != 33 {
		t.Fatalf("child divider at %d columns, want 33\n%s", firstSplit, frame)
	}
	styled, err := tmux("capture-pane", "-ep", "-t", sess.Name)
	if err != nil || !strings.Contains(styled, "\x1b[31mStock child pane") {
		t.Errorf("styled sbx capture: %q, %v", styled, err)
	}
	keys("C-g", "Tab")
	assertDashboardDivider(t, waitDashboardPane(t, sess, "Task Dashboard"), firstSplit)
	keys("C-c")
	waitDashboardPane(t, sess, "Cancel ↵")
	keys("Enter")
	assertDashboardDivider(t, waitDashboardPane(t, sess, "Task Dashboard"), firstSplit)
	if _, err := tmux("resize-window", "-t", sess.Name, "-x", "80", "-y", "24"); err != nil {
		t.Fatal(err)
	}
	keys("C-g", "Tab")
	frame = waitDashboardPane(t, sess, "Stock child pane 80")
	secondSplit := dashboard.SbxDivider(frame, 80)
	if secondSplit != 23 {
		t.Fatalf("resized child divider at %d columns, want 23\n%s", secondSplit, frame)
	}
	keys("C-g", "Tab")
	assertDashboardDivider(t, waitDashboardPane(t, sess, "Task Dashboard"), secondSplit)
	keys("C-g", "Tab")
	waitDashboardPane(t, sess, "Stock child pane 80")
	keys("C-c")
	waitDashboardPane(t, sess, "DASHBOARD_EXIT=130")
	waitDashboardPane(t, sess, "TERMINAL_RESTORED")
}

func assertDashboardDivider(t *testing.T, frame string, want int) {
	t.Helper()
	for line := range strings.SplitSeq(frame, "\n") {
		if left, _, ok := strings.Cut(line, "╭─ Task Dashboard"); ok {
			if got := ansi.StringWidth(left) - 1; got != want {
				t.Errorf("the gap between Proveo's panes at %d columns, want %d", got, want)
			}
			return
		}
	}
	t.Fatalf("review queue has no divider:\n%s", frame)
}

func TestNativeDashboardAttachAndNewInstanceOnTmux(t *testing.T) {
	requireTmux(t)
	bin, home, work, tools := buildProveo(t), t.TempDir(), t.TempDir(), t.TempDir()
	prompt := filepath.Join(work, "prompt.txt")
	writeFile(t, prompt, []byte("Original review prompt"))
	const job = "dashboard-attach"
	config, err := yaml.Marshal(schedule.Config{Jobs: map[string]schedule.Job{
		job: {Target: "codex", Workdir: work, PromptFile: prompt},
	}})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, schedule.Path(home), config)
	const sandboxName, vmID = "codex-dashboard-1234", "vm-dashboard-1234"
	host, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	work, err = filepath.EvalSymlinks(work)
	if err != nil {
		t.Fatal(err)
	}
	instanceDir := filepath.Join(home, "instances", sandboxName)
	mkdirAll(t, instanceDir)
	record, err := json.Marshal(map[string]string{
		"ID": "codex-1234", "Name": sandboxName, "VMID": vmID, "Target": "codex",
		"Workspace": work, "Host": host, "Job": job,
	})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(instanceDir, "instance.json"), record)
	guest := filepath.Join(tools, "guest")
	writeFile(t, guest, []byte("active"))
	freshLog := filepath.Join(tools, "fresh.log")
	sbx := filepath.Join(tools, "sbx")
	if err := os.WriteFile(sbx, []byte("#!/bin/sh\n"+`case "$1" in
settings) printf '{"value":"32g"}\n' ;;
ls)
  if test -f "$FIXTURE_GUEST"; then
    printf '[{"name":"codex-dashboard-1234","id":"vm-dashboard-1234","status":"running"}]\n'
  else
    printf '[]\n'
  fi ;;
exec)
  if [ "$2" = -i ]; then
    printf '%s\n' "$@" >> "$FIXTURE_FRESH_LOG"
  else
    printf 'cpu=2%%\nram=1073741824\ndisk=2147483648\nfresh=yes\n'
  fi ;;
esac
`), 0o755); err != nil {
		t.Fatal(err)
	}
	tmux := dashboardTmux(t)
	agent := schedule.NewTmux(schedule.SessionName(job), tmux)
	if err := agent.Start(120, 40, "sh", "-c", `printf 'AGENT_READY\n'; exec cat`); err != nil {
		t.Fatal(err)
	}
	waitDashboardPane(t, agent, "AGENT_READY")
	agentPID, err := agent.PanePID()
	if err != nil {
		t.Fatal(err)
	}
	sess := schedule.NewTmux("native-dashboard", tmux)
	err = sess.Start(120, 40, "env", "TMUX=", "TERM=xterm-256color", "PROVEO_HOME="+home,
		"PROVEO_STATE_ROOT="+home, "PROVEO_SBX_STORAGE="+home, "PROVEO_SBX_DISK_POOL=1g",
		"FIXTURE_GUEST="+guest, "FIXTURE_FRESH_LOG="+freshLog,
		"PATH="+tools+string(os.PathListSeparator)+os.Getenv("PATH"), "sh", "-c", `cd "$1" && exec "$2"`, "sh", work, bin)
	if err != nil {
		t.Fatal(err)
	}
	keys := func(keys ...string) {
		t.Helper()
		if err := sess.SendKeys(keys...); err != nil {
			t.Fatal(err)
		}
	}
	enterText := func(value string) {
		t.Helper()
		if err := sess.SendText(value); err != nil {
			t.Fatal(err)
		}
		keys("Enter")
	}
	frame := waitDashboardPane(t, sess, "e Fresh")
	for _, hidden := range []string{"Task details", "Original review prompt", "Attention required", "1 2 3"} {
		if strings.Contains(frame, hidden) {
			t.Errorf("unexpected %q before opening task details:\n%s", hidden, frame)
		}
	}
	if queued, button := strings.Index(frame, "Queued"), strings.Index(frame, "new instance"); queued < 0 || button < queued {
		t.Errorf("the new instance card should sit under the Queued heading, shown even with nothing queued:\n%s", frame)
	}
	keys("t")
	if card := waitDashboardPane(t, sess, "Original review prompt"); !strings.Contains(card, "╭") || !strings.Contains(card, "Usage") {
		t.Fatalf("t should open the details card over the queue:\n%s", card)
	}
	keys("t")
	if frame := waitDashboardPane(t, sess, "See details"); strings.Contains(frame, "Original review prompt") {
		t.Fatalf("closing task details left the prompt visible:\n%s", frame)
	}
	keys("t")
	waitDashboardPane(t, sess, "Original review prompt")
	keys("Enter")
	waitDashboardPane(t, sess, "Cancel ↵")
	keys("Enter")
	waitDashboardPane(t, sess, "Task Dashboard")
	keys("Enter", "Left")
	waitDashboardPane(t, sess, "Attach ↵")
	keys("Enter")
	waitDashboardPane(t, sess, "AGENT_READY")
	enterText("typed inside agent")
	waitDashboardPane(t, agent, "typed inside agent")
	keys("C-b", "d")
	waitDashboardPane(t, sess, "Original review prompt")
	if got, err := agent.PanePID(); err != nil || got != agentPID {
		t.Fatalf("agent PID after detach = %q (%v), want %q", got, err, agentPID)
	}

	waitDashboardPane(t, sess, "e Fresh")
	keys("e")
	if !waitUntil(5*time.Second, func() bool {
		return strings.Contains(readIn(tools, "fresh.log"), "\nfresh\n.\n")
	}) {
		t.Fatal("Fresh was not launched in the live guest")
	}
	got := readIn(tools, "fresh.log")
	if !strings.Contains(got, "-u\nagent\n-w\n"+filepath.Join(instanceDir, "workspace")+"\n"+sandboxName+"\n") {
		t.Errorf("Fresh guest argv = %q", got)
	}
	waitDashboardPane(t, sess, "Task Dashboard")
	if err := os.Remove(guest); err != nil {
		t.Fatal(err)
	}
	keys("e")
	if !waitUntil(12*time.Second, func() bool {
		frame, err := sess.Capture()
		return err == nil && strings.Contains(frame, "Task Dashboard") && !strings.Contains(frame, "e Fresh")
	}) {
		t.Fatal("Fresh remained visible after the guest disappeared")
	}
	if after := readIn(tools, "fresh.log"); after != got {
		t.Fatal("Fresh was invoked again after the guest disappeared")
	}

	keys("n")
	tasks := waitDashboardPane(t, sess, "type to filter the tasks")
	for _, want := range []string{"New instance", "Blank session", "Plans", "Pull requests", "Linear"} {
		if !strings.Contains(tasks, want) {
			t.Errorf("n should open the task picker with its default sources; it lacks %q:\n%s", want, tasks)
		}
	}
	keys("Enter")
	picker := waitDashboardPane(t, sess, "run> ")
	if !strings.Contains(picker, "codex") || strings.Contains(picker, job) {
		t.Errorf("n should open proveo run --queue on its harness list, without running tasks:\n%s", picker)
	}
	keys("Escape")
	waitDashboardPane(t, sess, "Task Dashboard")
	keys("C-c")
	waitDashboardPane(t, sess, "Exit Proveo?")
	keys("Right")
	waitDashboardPane(t, sess, "Exit ↵")
	keys("Enter")
	if !waitUntil(5*time.Second, func() bool { return !sess.Alive() }) || !agent.Alive() {
		t.Fatal("confirming exit should close the dashboard and leave the agent running")
	}
}
