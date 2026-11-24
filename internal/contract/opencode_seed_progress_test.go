// SPEC: _spec/packages/lib/opencode-seed-progress.puml
package contract_test

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOpenCodeSeedProgressFixtures(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	home := t.TempDir()
	status := filepath.Join(home, "status")
	marker := filepath.Join(home, "instructions")
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "TERM=xterm-256color",
		"PROVEO_SEED_PROGRESS=1", "PROVEO_SEED_STATUS_FILE=" + status, "SANDBOX_VM_ID=seed-test",
		"PROVEO_WORKDIR=" + home, "PROVEO_INSTRUCTIONS_MARKER=" + marker,
		"PROVEO_SEED_DONE_MARKER=" + filepath.Join(home, "done"), "PROVEO_SEED_REFUSED=" + filepath.Join(home, "refused")}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	for _, target := range []string{"claudecode", "opencode"} {
		cmd := exec.CommandContext(ctx, "bash", "-c", `source "$1"; proveo_seed_step setup "seeding configuration folders" "$2"`, "bash", filepath.Join(root, "packages/lib/entrypoint-lib.sh"), target)
		cmd.Env, cmd.Dir = env, home
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s seed: %v %s", target, err, out)
		}
		if got := strings.TrimSpace(string(out)); (target == "opencode" && got != "proveo-seed: setup: seeding configuration folders") || (target != "opencode" && got != "") {
			t.Errorf("%s progress = %q", target, got)
		}
	}
	if err := os.WriteFile(status, []byte("workspace: preparing shared folders\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	await := filepath.Join(root, "packages/lib/proveo-await-seed")
	cmd := exec.CommandContext(ctx, "sh", await, "--cwd")
	cmd.Env = append(env, "PROVEO_INSTRUCTIONS_WAIT=5")
	cmd.Dir = home
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(stderr)
	first, readErr := reader.ReadString('\n')
	if readErr == nil {
		if err := os.WriteFile(marker, nil, 0o600); err != nil {
			t.Error(err)
		}
	}
	realHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil || readErr != nil || !strings.Contains(first, "workspace: preparing shared folders") ||
		strings.Contains(first, "\x1b") || (stdout.String() != home+"\n" && stdout.String() != realHome+"\n") {
		t.Fatalf("seed handoff: %q / %q (%v, %v)", first, stdout.String(), readErr, err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	cmd = exec.CommandContext(ctx, "sh", await, "printf", "%s\\n", "hello world")
	cmd.Env = append(env, "PROVEO_INSTRUCTIONS_WAIT=1")
	cmd.Dir = home
	stdout.Reset()
	var timed bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &timed
	if err := cmd.Run(); err != nil || stdout.String() != "hello world\n" ||
		!strings.Contains(timed.String(), "Waiting for seed") || !strings.Contains(timed.String(), "launching anyway") {
		t.Fatalf("seed timeout: %q / %q (%v)", stdout.String(), timed.String(), err)
	}
}

func TestOpenCodeImageEnablesSeedProgressOnlyForOpenCode(t *testing.T) {
	t.Parallel()
	dockerfile := readRepoFile(t, "defs/opencode/Dockerfile")
	if !strings.Contains(dockerfile, "PROVEO_SEED_PROGRESS=1") {
		t.Error("OpenCode image lacks PROVEO_SEED_PROGRESS=1")
	}
	for _, path := range []string{"defs/claudecode/mcp/Dockerfile", "defs/codex/Dockerfile", "defs/cursor/Dockerfile"} {
		if strings.Contains(readRepoFile(t, path), "PROVEO_SEED_PROGRESS=1") {
			t.Errorf("OpenCode-only progress enabled in %s", path)
		}
	}
}
