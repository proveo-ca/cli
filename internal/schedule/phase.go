// SPEC: _spec/_experiments/dashboard/workspace-instances.puml
package schedule

import (
	"fmt"
	"regexp"
	"slices"
)

var revisionID = regexp.MustCompile(`^(?:[a-f0-9]{40}|[a-f0-9]{64})$`)

var phaseTransitions = map[string][]string{
	"Spec":              {"Verification"},
	"Verification":      {"Spec revision", "Live verification"},
	"Spec revision":     {"Verification", "Live verification"},
	"Live verification": {"Spec revision", "Upgrade"},
	"Upgrade":           {"Live verification", "PR"},
	"PR":                {"Review"},
	"Review":            {"Spec revision", "Merge"},
}

func UpdatePhase(home, name, phase, revision string) error {
	if !revisionID.MatchString(revision) {
		return fmt.Errorf("phase evidence needs a full Git commit ID")
	}
	return queueTxn(home, func(q []QueueItem) ([]QueueItem, error) {
		i := queueIndex(q, name)
		if i < 0 {
			return q, fmt.Errorf("%s has no submitted task", name)
		}
		item := &q[i]
		from := item.Job.Phase
		if from == "" {
			from = "Spec"
		}
		if item.Revision != "" && item.Revision != revision && from != "Spec" {
			from = "Spec revision"
		}
		if !slices.Contains(phaseTransitions[from], phase) && (from != "Spec revision" || phase != from) {
			return q, fmt.Errorf("cannot move from %s to %s for this revision", from, phase)
		}
		item.Job.Phase, item.Revision = phase, revision
		return q, nil
	})
}
