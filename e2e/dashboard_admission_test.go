//go:build e2e && !windows

// SPEC: _spec/internal/schedule/resource-queue.puml
package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/proveo-ca/proveo/internal/schedule"
)

func TestDashboardQueueDispatchesWhenDiskFits(t *testing.T) {
	requireTmux(t)
	bin, home, work, tools := buildProveo(t), t.TempDir(), t.TempDir(), t.TempDir()
	disk := filepath.Join(home, "disk")
	writeFile(t, disk, []byte(`{"value":"64g"}`))
	sbx := filepath.Join(tools, "sbx")
	const command = `#!/bin/sh
case "$1" in
settings) cat "$PROVEO_FIXTURE_DISK" ;;
ls) printf '[]\n' ;;
esac
`
	if err := os.WriteFile(sbx, []byte(command), 0o755); err != nil {
		t.Fatal(err)
	}
	prompt := filepath.Join(home, "initial")
	writeFile(t, prompt, []byte("capacity fixture"))
	job := schedule.Job{Target: "codex", Workdir: work, PromptFile: prompt, Mode: "plain", Ready: "CAPACITY_READY",
		Command: []string{"sh", "-c", `printf 'CAPACITY_READY\n'; IFS= read -r line; printf '%s\n' "$line" > received`}}
	data, err := yaml.Marshal(schedule.Config{Jobs: map[string]schedule.Job{"disk-job": job}})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, schedule.Path(home), data)
	tmux := dashboardTmux(t)
	session := schedule.NewTmux("submit", tmux)
	env := []string{"env", "PROVEO_HOME=" + home, "PROVEO_STATE_ROOT=" + home, "XDG_CACHE_HOME=" + home, "PROVEO_SBX_STORAGE=" + home,
		"PROVEO_SBX_DISK_POOL=4g", "PROVEO_FIXTURE_DISK=" + disk, "PATH=" + tools + string(os.PathListSeparator) + os.Getenv("PATH")}
	argv := append(env, "sh", "-c", `"$1" schedule run disk-job && exec cat`, "sh", bin)
	if err := session.Start(120, 40, argv...); err != nil {
		t.Fatal(err)
	}
	if !waitUntil(18*time.Second, func() bool {
		q, err := schedule.ReadQueue(home)
		return err == nil && len(q) == 1 && q[0].State == schedule.Queued && q[0].Reason != ""
	}) {
		t.Fatal("disk request did not remain queued")
	}
	runner := schedule.NewTmux(schedule.SessionName("disk-job"), tmux)
	if runner.Alive() {
		t.Error("agent started while reservation exceeded capacity")
	}
	writeFile(t, disk, []byte(`{"value":"1g"}`))
	if !waitUntil(25*time.Second, func() bool {
		out, err := os.ReadFile(filepath.Join(work, "received"))
		return err == nil && len(out) > 0
	}) {
		t.Fatal("agent did not receive its prompt after disk capacity changed")
	}
	if !waitUntil(25*time.Second, func() bool {
		q, err := schedule.ReadQueue(home)
		return err == nil && len(q) == 1 && q[0].State == schedule.Attention
	}) {
		t.Fatal("completed agent did not reach review")
	}
}

func TestDashboardQueueRestartsFromPersistedSubmission(t *testing.T) {
	requireTmux(t)
	bin, home, work, tools := buildProveo(t), t.TempDir(), t.TempDir(), t.TempDir()
	capacity := filepath.Join(home, "capacity")
	writeFile(t, capacity, []byte(`{"value":"64g"}`))
	if err := os.WriteFile(filepath.Join(tools, "sbx"), []byte("#!/bin/sh\n"+`case "$1" in
settings) cat "$FIXTURE_CAPACITY" ;;
ls) printf '[]\n' ;;
esac
`), 0o755); err != nil {
		t.Fatal(err)
	}
	prompt := filepath.Join(work, "prompt.md")
	writeFile(t, prompt, []byte("persisted prompt"))
	job := schedule.Job{Target: "codex", Mode: "plain", Ready: "FIXTURE_READY", PromptFile: prompt, Workdir: work,
		Command: []string{"sh", "-c", `printf 'FIXTURE_READY\n'; IFS= read -r line; printf '%s\n' "$line" > received`}}
	if err := schedule.Submit(home, "restored-task", "manual", job, time.Now()); err != nil {
		t.Fatal(err)
	}
	tmux := dashboardTmux(t)
	start := func(name string) *schedule.TmuxSession {
		t.Helper()
		sess := schedule.NewTmux(name, tmux)
		if err := sess.Start(120, 40, "env", "PROVEO_HOME="+home, "PROVEO_STATE_ROOT="+home, "PROVEO_SBX_STORAGE="+home,
			"PROVEO_SBX_DISK_POOL=4g", "FIXTURE_CAPACITY="+capacity, "PATH="+tools+string(os.PathListSeparator)+os.Getenv("PATH"),
			bin, "schedule", "dispatch"); err != nil {
			t.Fatal(err)
		}
		return sess
	}
	first := start("first-dispatcher")
	if !waitUntil(12*time.Second, func() bool {
		q, err := schedule.ReadQueue(home)
		return err == nil && len(q) == 1 && q[0].Reason != ""
	}) {
		t.Fatal("first dispatcher did not observe disk pressure")
	}
	if _, ok := stopRun(first, 5*time.Second); !ok {
		t.Fatal("first dispatcher did not exit")
	}
	writeFile(t, capacity, []byte(`{"value":"1g"}`))
	writeFile(t, prompt, []byte("changed source prompt"))
	start("next-dispatcher")
	if !waitUntil(20*time.Second, func() bool {
		return strings.Contains(readIn(work, "received"), "persisted prompt")
	}) {
		t.Fatal("restart did not dispatch the persisted prompt")
	}
}
