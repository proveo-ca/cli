// SPEC: _spec/internal/sbx/sbx-kit-v3.puml
package claudecfg

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// firstRun are the ~/.claude.json flags that keep Claude Code's first-run dialogs from holding an unattended start.
var firstRun = []string{"bypassPermissionsModeAccepted", "hasCompletedOnboarding", "hasSeenAutoDefaultNudge"}

// AcceptWorkspace merges the first-run flags and trust for / and dir into home's ~/.claude.json, keeping every other key.
func AcceptWorkspace(home, dir string) error {
	path := filepath.Join(home, ".claude.json")
	j := map[string]any{}
	if b, err := os.ReadFile(path); err == nil {
		if json.Unmarshal(b, &j) != nil || j == nil {
			j = map[string]any{}
		}
	}
	for _, k := range firstRun {
		j[k] = true
	}
	projects, _ := j["projects"].(map[string]any)
	if projects == nil {
		projects = map[string]any{}
	}
	for _, d := range []string{"/", dir} {
		if d == "" {
			continue
		}
		p, _ := projects[d].(map[string]any)
		if p == nil {
			p = map[string]any{}
		}
		p["hasTrustDialogAccepted"] = true
		projects[d] = p
	}
	j["projects"] = projects
	b, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}
