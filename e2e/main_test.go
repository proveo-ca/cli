//go:build e2e

// SPEC: _spec/_devops/agent-update-prompt.puml, _spec/cmd/proveo/run-session.puml
package e2e

import (
	"os"
	"testing"

	"github.com/proveo-ca/proveo/internal/agentupdate"
	"github.com/proveo-ca/proveo/internal/schedule"
)

// TestMain turns the agent-update check and the run's own tmux session off for every run the suite launches,
// unless the caller set them: the suite drives runs through its own ptys and tmux sessions.
func TestMain(m *testing.M) {
	for k, v := range map[string]string{agentupdate.EnvSwitch: "0", "PROVEO_RUN_TMUX": "off"} {
		if _, set := os.LookupEnv(k); !set {
			_ = os.Setenv(k, v)
		}
		schedule.TmuxEnv = append(schedule.TmuxEnv, k+"="+os.Getenv(k))
	}
	os.Exit(m.Run())
}
