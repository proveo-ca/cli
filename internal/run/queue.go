// SPEC: _spec/_experiments/dashboard/workspace-instances.puml
package run

import (
	"path/filepath"
	"strconv"
	"strings"
)

// QueueRequest is a run the card resolved, to submit instead of launching.
type QueueRequest struct {
	Target, Task string
	Prompt       string   // the opening prompt the attended session types once the agent is ready
	Args         []string // proveo run flags the scheduled run repeats; the card's answers replay from the settings cache
}

// queueArgs are the flags a scheduled `proveo run --yes` needs to repeat this run.
func queueArgs(p Params, scope, repoRoot string) []string {
	var args []string
	add := func(flag, value string) {
		if value != "" {
			args = append(args, flag, value)
		}
	}
	if p.CredsSet {
		add("--credentials", p.Credentials)
	}
	if p.CloneSet {
		args = append(args, "--clone="+strconv.FormatBool(p.Clone))
	}
	add("--model", p.Model)
	add("--local-model", p.LocalModel)
	add("--input", p.Input)
	if p.Output != filepath.Join(repoRoot, "reports") {
		add("--output", p.Output)
	}
	add("--data-dir", p.DataDir)
	add("--browser-profile", p.BrowserProfile)
	add("--branch", p.Branch)
	add("--scope", strings.Trim(scope, "/"))
	if p.ImageSet {
		add("--image", p.Image)
	}
	if p.AddonsSet {
		for _, a := range p.Addons {
			add("--addon", a)
		}
	}
	if len(p.Extra) > 0 {
		args = append(append(args, "--"), p.Extra...)
	}
	return args
}
