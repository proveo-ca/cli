// SPEC: _spec/packages/lib/agent-activity.puml
package activity

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func fakeProc(t *testing.T, procs map[int][2]string) string {
	t.Helper()
	root := t.TempDir()
	for pid, p := range procs {
		dir := filepath.Join(root, strconv.Itoa(pid))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "comm"), []byte(p[0]+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(strconv.Itoa(pid)+" ("+p[0]+") S "+p[1]+" 1 1"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestAgentPIDSkipsTheShellsBetweenAgentAndHook(t *testing.T) {
	proc := fakeProc(t, map[int][2]string{40: {"bash", "30"}, 30: {"sh", "20"}, 20: {"claude", "1"}})
	if got := AgentPID(proc, 40); got != 20 {
		t.Errorf("agent pid = %d, want the claude process 20", got)
	}
	if got := AgentPID(proc, 20); got != 20 {
		t.Errorf("a direct child of the agent = %d", got)
	}
}

func TestMarkWritesOneMarkerPerAgentAndSeesBackgroundWork(t *testing.T) {
	proc := fakeProc(t, map[int][2]string{40: {"sh", "20"}, 20: {"claude", "1"}})
	dir := filepath.Join(t.TempDir(), "activity")
	now := time.UnixMilli(1795629159114)
	m, err := Mark(dir, proc, "claudecode", Idle, 40, strings.NewReader(`{"background_tasks":[{"id":"b1"}]}`), now)
	if err != nil || m.PID != 20 || m.State != Background {
		t.Fatalf("marker = %+v, %v", m, err)
	}
	if m, _ = Mark(dir, proc, "claudecode", Idle, 40, strings.NewReader(`{"background_tasks":[]}`), now); m.State != Idle {
		t.Errorf("an empty background list read as %q", m.State)
	}
	b, err := os.ReadFile(filepath.Join(dir, "20.json"))
	if err != nil || !strings.Contains(string(b), `"state":"idle"`) || !strings.Contains(string(b), `"since":1795629159114`) {
		t.Errorf("marker file = %s, %v", b, err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("%d files, want the one marker and no temp files", len(entries))
	}
	for _, bad := range [][2]string{{"Claude", "busy"}, {"../x", "busy"}, {"codex", "done"}} {
		if _, err := Mark(dir, proc, bad[0], State(bad[1]), 40, nil, now); err == nil {
			t.Errorf("Mark(%q, %q) wrote a marker", bad[0], bad[1])
		}
	}
}

func TestMergeClaudeHookAddsOnceAndKeepsTheRest(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude", "settings.json")
	for range 2 {
		if err := MergeClaudeHook(path, "Notification", "mark waiting", 5, "permission_prompt"); err != nil {
			t.Fatal(err)
		}
	}
	write := func(s string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"theme":"dark","hooks":{"Notification":[{"hooks":[{"type":"command","command":"mark waiting"}]}],"Stop":[{"hooks":[{"command":"mine"}]}]}}`)
	if err := MergeClaudeHook(path, "Stop", "mark idle", 5, ""); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), `"theme": "dark"`) || strings.Count(string(b), "mark waiting") != 1 || !strings.Contains(string(b), `"mine"`) || !strings.Contains(string(b), `"mark idle"`) {
		t.Errorf("settings = %s", b)
	}
	write(`{not json`)
	if err := MergeClaudeHook(path, "Stop", "mark idle", 5, ""); err == nil {
		t.Error("a broken settings file was overwritten")
	}
	if b, _ := os.ReadFile(path); string(b) != `{not json` {
		t.Errorf("the operator's file changed: %s", b)
	}
}

func TestResolvePrefersTheAgentsOwnStateThenTheMostUrgent(t *testing.T) {
	at := func(ms int64) string { return strconv.FormatInt(ms, 10) }
	raw := strings.Join([]string{
		"activity\tclaudecode\tshell\t" + at(2000) + "\t1",
		"marker\t1\t" + `{"harness":"claudecode","pid":9,"state":"busy","since":3000}`,
		"marker\t1\t" + `{"harness":"opencode","pid":7,"state":"idle","since":1000}`,
		"activity\tcodex\ttask_complete\t" + at(1500) + "\t0",
		"activity\tclaudecode\tnonsense\t1\t1",
		"garbage",
	}, "\n")
	samples := Parse(raw)
	if len(samples) != 4 {
		t.Fatalf("samples = %+v", samples)
	}
	if state, since := Resolve(samples); state != Background || since.UnixMilli() != 2000 {
		t.Errorf("resolve = %q at %v; claude's own shell status beats its stale busy marker", state, since)
	}
	if state, _ := Resolve(Parse("activity\tcodex\ttask_started\t1\t0")); state != Exited {
		t.Errorf("a dead agent = %q, want exited", state)
	}
	if state, _ := Resolve(nil); state != Unknown {
		t.Errorf("no signal = %q", state)
	}
	both := Parse("activity\tclaudecode\tidle\t1\t1\nactivity\tclaudecode\twaiting\t2\t1")
	if state, _ := Resolve(both); state != Waiting {
		t.Errorf("two sessions = %q; a waiting one needs the operator first", state)
	}
}
