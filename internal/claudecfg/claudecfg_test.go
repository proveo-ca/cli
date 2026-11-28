// SPEC: _spec/internal/sbx/sbx-kit-v3.puml
package claudecfg

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestAcceptWorkspaceSetsTheFirstRunFlagsAndKeepsTheRest(t *testing.T) {
	home := t.TempDir()
	restored := `{"mcpServers":{"serena":{"command":"serena"}},"projects":{"/w":{"allowedTools":["Bash"]}}}`
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(restored), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := AcceptWorkspace(home, "/w"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(home, ".claude.json"))
	var j map[string]any
	if err := json.Unmarshal(b, &j); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"bypassPermissionsModeAccepted", "hasCompletedOnboarding", "hasSeenAutoDefaultNudge"} {
		if j[k] != true {
			t.Errorf("%s = %v; an unattended start stops on its dialog", k, j[k])
		}
	}
	projects := j["projects"].(map[string]any)
	for _, d := range []string{"/", "/w"} {
		if projects[d].(map[string]any)["hasTrustDialogAccepted"] != true {
			t.Errorf("%s is not trusted", d)
		}
	}
	if j["mcpServers"] == nil || projects["/w"].(map[string]any)["allowedTools"] == nil {
		t.Errorf("restored keys were dropped: %s", b)
	}
	fresh := t.TempDir()
	if err := AcceptWorkspace(fresh, "/x"); err != nil {
		t.Fatalf("a home without ~/.claude.json: %v", err)
	}
}
