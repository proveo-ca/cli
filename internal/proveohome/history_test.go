// SPEC: _spec/internal/sbx/state-sync.puml
package proveohome

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNativeHistoryCheckpointRoundTrip(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ target, name, row string }{
		{"codex", ".codex/sessions/rollout.jsonl", `{"type":"session_meta","payload":{"id":"codex-123","cwd":"/app"}}`},
		{"claudecode", ".claude/projects/claude-123.jsonl", `{"sessionId":"claude-123","cwd":"/app","type":"user"}`},
	} {
		t.Run(tc.target, func(t *testing.T) {
			t.Parallel()
			home, dir, restored := t.TempDir(), t.TempDir(), t.TempDir()
			path := filepath.Join(home, tc.name)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tc.row+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			first, err := CheckpointSession(dir, home, tc.target, "/app", "attempt-1", "git-tip", time.Time{})
			if err != nil {
				t.Fatal(err)
			}
			if first.NativeID == "" || first.SHA256 == "" {
				t.Fatalf("incomplete checkpoint: %+v", first)
			}
			again, err := CheckpointSession(dir, home, tc.target, "/app", "attempt-1", "other", time.Time{})
			if err != nil || again.SHA256 != first.SHA256 || again.GitTip != "git-tip" {
				t.Errorf("retried checkpoint = %+v, %v", again, err)
			}
			got, err := FindCheckpoint(dir, first.NativeID)
			if err != nil || got.SHA256 != first.SHA256 {
				t.Fatalf("find checkpoint: %+v, %v", got, err)
			}
			if err := RestoreCheckpoint(dir, restored, got); err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(filepath.Join(restored, tc.name))
			if err != nil || string(b) != tc.row+"\n" {
				t.Errorf("restored transcript = %q, %v", b, err)
			}
			restoredPath := filepath.Join(restored, tc.name)
			if err := os.WriteFile(restoredPath, []byte("{\"partial\":"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := RestoreCheckpoint(dir, restored, got); err != nil {
				t.Fatalf("restore over a broken working copy: %v", err)
			}
			if b, err := os.ReadFile(restoredPath); err != nil || string(b) != tc.row+"\n" {
				t.Fatalf("repaired working copy = %q (%v)", b, err)
			}
			if err := os.WriteFile(path, []byte(tc.row+"\n{\"partial\":"), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := CheckpointSession(dir, home, tc.target, "/app", "attempt-2", "new-tip", time.Time{}); err == nil {
				t.Fatal("published a truncated native event")
			}
			head, err := FindCheckpoint(dir, "")
			if err != nil || head != first {
				t.Fatalf("failed export advanced head: %+v (%v)", head, err)
			}
			if err := os.WriteFile(filepath.Join(dir, got.Attempt, "session.jsonl"), []byte("damaged"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := RestoreCheckpoint(dir, t.TempDir(), got); err == nil {
				t.Error("restored a damaged checkpoint")
			}
		})
	}
}
