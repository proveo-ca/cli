// SPEC: _spec/internal/sbx/clone-workspace.puml
package credentials

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestStageProjectEnvDropsOnlyTheBrokeredKeys(t *testing.T) {
	t.Parallel()
	src := filepath.Join(t.TempDir(), ".env")
	body := "# db\nDB_URL=postgres://x\nexport ANTHROPIC_API_KEY=sk-ant\nOPENAI_API_KEY = sk-o\nFEATURE=on\n"
	if err := os.WriteFile(src, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "project-env")
	path, dropped, err := StageProjectEnv(src, dir, []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if string(b) != "# db\nDB_URL=postgres://x\nFEATURE=on\n" {
		t.Errorf("staged:\n%s", b)
	}
	if !slices.Equal(dropped, []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY"}) {
		t.Errorf("dropped = %v", dropped)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v; the staged copy still holds project secrets", fi.Mode().Perm())
	}
	if _, kept, _ := StageProjectEnv(src, dir, nil); len(kept) != 0 {
		t.Errorf("forward mode drops nothing, got %v", kept)
	}
}

func TestAHostOnlyKeyNeverEntersTheSandboxAndSurvivesTheMergeHome(t *testing.T) {
	t.Parallel()
	src := filepath.Join(t.TempDir(), ".env")
	host := "DB_URL=postgres://x\nLINEAR_API_KEY=lin_api_host\n"
	if err := os.WriteFile(src, []byte(host), 0o600); err != nil {
		t.Fatal(err)
	}
	path, dropped, err := StageProjectEnv(src, filepath.Join(t.TempDir(), "project-env"), HostOnlyVars)
	if err != nil {
		t.Fatal(err)
	}
	base, _ := os.ReadFile(path)
	if string(base) != "DB_URL=postgres://x\n" || !slices.Equal(dropped, []string{"LINEAR_API_KEY"}) {
		t.Fatalf("staged %q, dropped %v", base, dropped)
	}
	edited := string(base) + "LINEAR_API_KEY=lin_api_agent\nFEATURE=on\n"
	m := MergeProjectEnv([]byte(host), base, []byte(edited), HostOnlyVars)
	if got := string(m.Body); got != host+"FEATURE=on\n" || !slices.Contains(m.Refused, "LINEAR_API_KEY") {
		t.Errorf("merged %q, refused %v; the host keeps its own token and refuses the agent's", got, m.Refused)
	}
}
