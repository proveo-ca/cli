// SPEC: _spec/packages/lib/agent-activity.puml
package contract_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSeedRegistersActivityHooksOnceBesideGitSync(t *testing.T) {
	t.Parallel()
	bash := bashOrSkip(t)
	bin := buildEntrypoint(t)
	home := t.TempDir()
	script := `source "$1/packages/lib/entrypoint-lib.sh"
agent_home="$2"
export HOME="$2" PROVEO_GIT_SYNC_HOOK="$3" PATH="$(dirname "$4"):$PATH" PROVEO_ACTIVITY_PLUGIN="$5"
_proveo_agent_home() { printf '%s' "$agent_home"; }
proveo_install_git_sync_hooks claudecode
proveo_install_activity_hooks claudecode
proveo_install_activity_hooks claudecode
proveo_install_activity_hooks opencode
echo DONE`
	plugin := filepath.Join(repoRoot(t), "packages", "lib", "hooks", "proveo-activity.js")
	out, err := exec.Command(bash, "-c", script, "bash", repoRoot(t), home, gitSyncScript(t), bin, plugin).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "DONE") {
		t.Fatalf("seed step failed: %v\n%s", err, out)
	}
	b, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var j struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Command string `json:"command"`
				Timeout int    `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &j); err != nil {
		t.Fatalf("settings.json: %v\n%s", err, b)
	}
	want := map[string]string{
		"UserPromptSubmit": "proveo-entrypoint activity claudecode busy",
		"Stop":             "proveo-entrypoint activity claudecode idle",
		"Notification":     "proveo-entrypoint activity claudecode waiting",
	}
	for event, cmd := range want {
		n := 0
		for _, g := range j.Hooks[event] {
			for _, h := range g.Hooks {
				if h.Command == cmd {
					n++
					if h.Timeout == 0 || h.Timeout > 10 {
						t.Errorf("%s: timeout %d; a marker write must never hold the turn", event, h.Timeout)
					}
					if event == "Notification" && g.Matcher != "permission_prompt|elicitation_dialog" {
						t.Errorf("Notification matcher %q marks every notification as waiting", g.Matcher)
					}
				}
			}
		}
		if n != 1 {
			t.Errorf("%s registered %d times after two seeds, want 1:\n%s", event, n, b)
		}
	}
	if !strings.Contains(string(b), "git-sync-turn.sh") {
		t.Errorf("the git-sync Stop hook did not survive:\n%s", b)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "opencode", "plugins", "proveo-activity.js")); err != nil {
		t.Errorf("the OpenCode activity plugin was not installed: %v", err)
	}
}

func TestStaticHookConfigsMarkCodexAndCursorActivity(t *testing.T) {
	t.Parallel()
	codex := readRepoFile(t, "defs/codex/managed/requirements.toml")
	for _, want := range []string{"[[hooks.UserPromptSubmit.hooks]]", "proveo-entrypoint activity codex busy", "proveo-entrypoint activity codex idle"} {
		if !strings.Contains(codex, want) {
			t.Errorf("codex requirements lack %q", want)
		}
	}
	var cursor struct {
		Hooks map[string][]struct {
			Command string `json:"command"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(readRepoFile(t, "defs/cursor/defaults/hooks.json")), &cursor); err != nil {
		t.Fatal(err)
	}
	for event, cmd := range map[string]string{"beforeSubmitPrompt": "activity cursor busy", "stop": "activity cursor idle"} {
		found := false
		for _, h := range cursor.Hooks[event] {
			found = found || strings.HasSuffix(h.Command, cmd)
		}
		if !found {
			t.Errorf("cursor %s lacks %q", event, cmd)
		}
	}
	for _, rel := range []string{"defs/base/Dockerfile", "defs/opencode/Dockerfile"} {
		if !strings.Contains(readRepoFile(t, rel), "packages/lib/hooks/proveo-activity.js") {
			t.Errorf("%s must COPY proveo-activity.js", rel)
		}
	}
}
