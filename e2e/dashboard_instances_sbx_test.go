//go:build e2e

// SPEC: _spec/internal/sbx/instance-lifecycle.puml
package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/proveo-ca/proveo/internal/sbx"
	"github.com/proveo-ca/proveo/internal/schedule"
)

func TestDashboardThreeParallelSandboxInstancesSharePWD(t *testing.T) {
	const target = "claudecode"
	requireHarness(t, target)
	bin := buildProveo(t)
	work, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home, state := t.TempDir(), t.TempDir()
	path := filepath.Join(work, "probe.txt")
	writeFile(t, path, []byte("base\n"))
	gitInit(t, work)
	writeFile(t, path, []byte("host edit\n"))

	var jobs [3]struct {
		task string
		sess *schedule.TmuxSession
		row  sbx.Listed
	}
	label := fmt.Sprintf("e%04x", uint16(time.Now().UnixNano()))
	isMine := func(row sbx.Listed) bool {
		return strings.HasPrefix(row.Name, target+"-"+label+"-") && slices.ContainsFunc(row.Workspaces, func(path string) bool {
			return filepath.Clean(workspacePath(path)) == work
		})
	}
	t.Cleanup(func() {
		if rows, ok := sbx.List(); ok {
			for _, row := range rows {
				if isMine(row) {
					_, _ = removeSandbox(t, row.Name)
				}
			}
		}
	})
	env := append(childEnvArgs(t), "PROVEO_HOME="+home, "PROVEO_STATE_ROOT="+state)
	for i := range jobs {
		job := &jobs[i]
		job.task = fmt.Sprintf("%s-%d", label, i)
		job.sess = schedule.NewTmux(job.task, nil)
		t.Cleanup(job.sess.Kill)
		args := append(slices.Clone(env), bin, "run", target, "--new", "--task", job.task, "--input", work, "--yes")
		if err := job.sess.Start(160, 40, append([]string{"env"}, args...)...); err != nil {
			t.Fatal(err)
		}
	}
	timeout := durationEnv(t, "PROVEO_TEST_TIMEOUT", 8*time.Minute)
	if !waitUntil(timeout, func() bool {
		rows, _ := sbx.List()
		for i := range jobs {
			jobs[i].row = sbx.Listed{}
			for _, row := range rows {
				if isMine(row) && strings.HasPrefix(row.Name, target+"-"+jobs[i].task+"-") && row.Running() {
					jobs[i].row = row
				}
			}
		}
		for _, job := range jobs {
			if job.row.ID == "" {
				return false
			}
		}
		return true
	}) {
		for _, job := range jobs {
			out, _ := job.sess.CaptureAll()
			t.Logf("%s\n%s", job.task, out)
		}
		t.Fatal("three running sandboxes with IDs did not appear")
	}

	seen := map[string]bool{}
	for _, job := range jobs {
		if seen[job.row.ID] {
			t.Errorf("sandbox %s reused VM ID %s", job.row.Name, job.row.ID)
		}
		seen[job.row.ID] = true
		k := &kitRun{t: t, sess: job.sess, name: job.row.Name, dir: work, target: target}
		k.awaitSeed()
		script := "test \"$(cat probe.txt)\" = base && git branch --show-current && printf '%s\\n' " +
			shellQuote([]string{job.task}) + " > probe.txt && mkdir -p reports && cp probe.txt reports/probe.txt"
		out, rc := k.exec(script, time.Minute)
		if rc != 0 || strings.TrimSpace(out) != "proveo/"+job.row.Name {
			t.Errorf("sandbox %s checkout = %q (status %d), want its own branch and untouched copy of base", job.row.Name, out, rc)
		}
	}
	for _, job := range jobs {
		k := &kitRun{t: t, sess: job.sess, name: job.row.Name, dir: work, target: target}
		out, rc := k.exec("cat probe.txt", time.Minute)
		if rc != 0 || strings.TrimSpace(out) != job.task {
			t.Errorf("sandbox %s probe = %q (status %d), want %s", job.row.Name, out, rc, job.task)
		}
	}
	first := jobs[0]
	data, err := os.ReadFile(filepath.Join(state, "instances", first.row.Name, "instance.json"))
	if err != nil {
		t.Fatal(err)
	}
	var record struct{ ID string }
	if err := json.Unmarshal(data, &record); err != nil || record.ID == "" {
		t.Fatalf("read instance ID for %s: %v", first.row.Name, err)
	}
	args := append(slices.Clone(env), bin, "run", target, "--instance", record.ID, "--input", work, "--yes")
	if out, err := exec.Command("env", args...).CombinedOutput(); err == nil || !strings.Contains(string(out), "already has a Proveo owner") {
		t.Errorf("second owner for %s = %v, %s", record.ID, err, out)
	}
	if out, ok := stopRun(first.sess, durationEnv(t, "PROVEO_TEST_TEARDOWN", 3*time.Minute)); !ok {
		t.Errorf("first instance did not settle:\n%s", out)
	}
	for _, job := range jobs[1:] {
		if !sbx.Running(job.row.Name) {
			t.Errorf("settling the first instance stopped %s", job.row.Name)
		}
	}
	for _, job := range jobs[1:] {
		if out, ok := stopRun(job.sess, durationEnv(t, "PROVEO_TEST_TEARDOWN", 3*time.Minute)); !ok {
			t.Errorf("instance %s did not settle:\n%s", job.row.Name, out)
		}
	}
	refs := strings.Fields(mustOutput(t, work, "git", "for-each-ref", "--format=%(refname)", "refs/proveo/"))
	for _, job := range jobs {
		i := slices.IndexFunc(refs, func(ref string) bool { return strings.HasSuffix(ref, "/proveo/"+job.row.Name) })
		if i < 0 {
			t.Errorf("no carried ref for %s in %v", job.row.Name, refs)
		} else if got := mustOutput(t, work, "git", "show", refs[i]+":probe.txt"); got != job.task+"\n" {
			t.Errorf("carried ref %s contains %q, want %s", refs[i], got, job.task)
		}
		exports, err := filepath.Glob(filepath.Join(state, "instances", job.row.Name, "outputs", "*", "reports", "probe.txt"))
		if err != nil || len(exports) != 1 {
			t.Errorf("outputs for %s = %v (%v), want one export", job.row.Name, exports, err)
		} else if got, err := os.ReadFile(exports[0]); err != nil || string(got) != job.task+"\n" {
			t.Errorf("output %s contains %q (%v), want %s", exports[0], got, err, job.task)
		}
	}
	if got := readIn(work, "probe.txt"); got != "host edit\n" {
		t.Errorf("host working tree contains %q, want host edit", got)
	}
}

func TestDashboardPrintPlansPrivateScopedInstance(t *testing.T) {
	work := t.TempDir()
	scope := filepath.Join(work, "packages", "sample")
	mkdirAll(t, scope)
	writeFile(t, filepath.Join(scope, "README.md"), []byte("scope\n"))
	gitInit(t, work)
	state, home := t.TempDir(), t.TempDir()
	args := append(childEnvArgsNoCredential(t), "PROVEO_HOME="+home, "PROVEO_STATE_ROOT="+state,
		buildProveo(t), "run", "codex", "--new", "--task", "PRV-284", "--input", work,
		"--scope", "packages/sample", "--print")
	out, err := exec.Command("env", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("print scoped instance: %v\n%s", err, out)
	}
	guestWork, err := filepath.EvalSymlinks(scope)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"codex-prv-284-", "PROVEO_WORKDIR=" + guestWork, "branch:", "--clone"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("private instance plan missing %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(filepath.Join(state, "instances")); !os.IsNotExist(err) {
		t.Errorf("print created an instance registry: %v", err)
	}
}
