//go:build e2e

// SPEC: _spec/internal/sbx/state-sync.puml
package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/proveo-ca/proveo/internal/proveohome"
	"github.com/proveo-ca/proveo/internal/sbx"
)

func TestDashboardCheckpointChoosesExactResumeID(t *testing.T) {
	bin, home, work, state := buildProveo(t), t.TempDir(), t.TempDir(), t.TempDir()
	work, err := filepath.EvalSymlinks(work)
	if err != nil {
		t.Fatal(err)
	}
	const name, id, session = "codex-test-a1", "instance-a1", "session-a1"
	dir := filepath.Join(state, "instances", name)
	mkdirAll(t, dir)
	record, err := json.Marshal(map[string]string{"ID": id, "Name": name, "Target": "codex", "Workspace": work})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "instance.json"), record)
	agentHome := filepath.Join(home, "instances", id)
	native := filepath.Join(agentHome, ".codex", "sessions")
	mkdirAll(t, native)
	line, err := json.Marshal(map[string]any{"type": "session_meta", "payload": map[string]string{"id": session, "cwd": work}})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(native, "rollout.jsonl"), append(line, '\n'))
	if _, err := proveohome.CheckpointSession(filepath.Join(dir, "history"), agentHome, "codex", work, "attempt-a1", "", time.Time{}); err != nil {
		t.Fatal(err)
	}
	env := append(childEnvArgsNoCredential(t), "PROVEO_HOME="+home, "PROVEO_STATE_ROOT="+state)
	args := append(env, bin, "run", "codex", "--instance", id, "--continue", "--input", work, "--print")
	out, err := exec.Command("env", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("print resume: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "resume") || !strings.Contains(string(out), session) || strings.Contains(string(out), "--last") {
		t.Errorf("resume plan lacks exact session ID:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(agentHome, ".codex", "sessions", "rollout.jsonl")); err != nil {
		t.Fatal(err)
	}
}

func TestDashboardResumesTwoNativeSessionsAcrossRebuiltVMs(t *testing.T) {
	failSkipsWhenOptedIn(t, dashboardLiveGate)
	const target = "codex"
	requireHarness(t, target)
	bin, home, work, state := buildProveo(t), t.TempDir(), t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(work, "README.md"), []byte("History fixture\n"))
	gitInit(t, work)
	env := append(childEnvArgs(t), "PROVEO_HOME="+home, "PROVEO_STATE_ROOT="+state)
	type nativeRun struct {
		*kitRun
		id, vm, native, marker string
	}
	var runs [2]nativeRun
	for i := range runs {
		r := &runs[i]
		r.marker = fmt.Sprintf("proveo-history-%d-%d", time.Now().UnixNano(), i)
		r.kitRun = launchKit(t, bin, target, work, env, "--new", "--task", fmt.Sprintf("history-%d", i), "--yes")
		r.vm = dashboardVMID(t, r.name)
		if err := r.sess.SendText("Reply with exactly " + r.marker + "."); err != nil {
			t.Fatal(err)
		}
		if err := r.sess.Enter(); err != nil {
			t.Fatal(err)
		}
		if !waitUntil(time.Minute, func() bool {
			out, rc, _ := r.try("grep -rl -- "+r.marker+" \"$HOME/.codex/sessions\"", 10*time.Second)
			return rc == 0 && strings.Contains(out, ".jsonl")
		}) {
			t.Fatalf("no native transcript for %s", r.name)
		}
	}
	for i := len(runs) - 1; i >= 0; i-- {
		r := &runs[i]
		if out, ok := r.end(); !ok {
			t.Fatalf("%s did not settle:\n%s", r.name, out)
		}
		dir := filepath.Join(state, "instances", r.name)
		record, err := os.ReadFile(filepath.Join(dir, "instance.json"))
		var instance struct{ ID string }
		if err != nil || json.Unmarshal(record, &instance) != nil || instance.ID == "" {
			t.Fatalf("read instance for %s: %v", r.name, err)
		}
		r.id = instance.ID
		checkpoint, err := proveohome.FindCheckpoint(filepath.Join(dir, "history"), "")
		if err != nil || checkpoint.NativeID == "" {
			t.Fatalf("read checkpoint for %s: %+v (%v)", r.name, checkpoint, err)
		}
		r.native = checkpoint.NativeID
		data, err := os.ReadFile(filepath.Join(dir, "history", checkpoint.Attempt, "session.jsonl"))
		if err != nil || !strings.Contains(string(data), r.marker) {
			t.Fatalf("checkpoint for %s lost its own turn: %v", r.name, err)
		}
		if sbx.Exists(r.name) {
			if out, err := removeSandbox(t, r.name); err != nil {
				t.Fatalf("remove checkpointed %s: %v\n%s", r.name, err, out)
			}
		}
	}
	if runs[0].native == runs[1].native {
		t.Fatal("parallel instances published the same native ID")
	}
	for _, r := range runs {
		resumed := launchKitWithExisting(t, bin, target, work, env, r.name, "--instance", r.id, "--continue", "--yes")
		if now := dashboardVMID(t, resumed.name); now == r.vm {
			t.Fatalf("resume for %s reused the original VM", resumed.name)
		}
		if out, rc := resumed.exec("grep -rl -- "+r.marker+" \"$HOME/.codex/sessions\"", 15*time.Second); rc != 0 || !strings.Contains(out, r.native) {
			t.Errorf("rebuilt %s did not restore native session %s: %s", resumed.name, r.native, out)
		}
		if out, ok := resumed.end(); !ok {
			t.Errorf("rebuilt %s did not settle:\n%s", resumed.name, out)
		}
	}
}

func dashboardVMID(t *testing.T, name string) string {
	t.Helper()
	rows, ok := sbx.List()
	if ok {
		for _, row := range rows {
			if row.Name == name && row.ID != "" && row.Running() {
				return row.ID
			}
		}
	}
	t.Fatalf("no running VM ID for %s", name)
	return ""
}
