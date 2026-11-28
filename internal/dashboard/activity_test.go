// SPEC: _spec/packages/lib/agent-activity.puml
package dashboard

import (
	"strings"
	"testing"
	"time"

	"github.com/proveo-ca/proveo/internal/activity"
	"github.com/proveo-ca/proveo/internal/ui"
)

func TestAnIdleAgentNeedsTheOperatorAndAWorkingOneStaysWorking(t *testing.T) {
	m := sampleModel(120, 40, ui.GlyphsSafe)
	live := &m.data.Tasks[2]
	live.Sandbox, live.VMID, live.Guest = "proveo-claudecode-1b3fcb7f", "vm-2", true
	sample := func(state activity.State) {
		m.samples = map[string]guestSample{live.ID: {id: live.ID, sandbox: live.Sandbox, vmid: live.VMID, available: true,
			at: sampleNow, activity: state, activitySince: sampleNow.Add(-12 * time.Minute)}}
	}
	sample(activity.Idle)
	if st := m.statusOf(*live); st.label != "Idle 12m" || m.groupLabel(*live) != "Attention required" {
		t.Errorf("idle: %q under %q", st.label, m.groupLabel(*live))
	}
	if order := m.taskOrder(); m.data.Tasks[order[0]].ID != live.ID && m.data.Tasks[order[1]].ID != live.ID && m.data.Tasks[order[2]].ID != live.ID {
		t.Errorf("an idle agent sorted outside Attention required: %v", order)
	}
	card := strings.Join(m.taskCard(2, *live, 40), "\n")
	if strings.Contains(card, "Working") {
		t.Errorf("an idle card still says Working:\n%s", card)
	}
	sample(activity.Busy)
	if st := m.statusOf(*live); st.label != "Working" || m.groupLabel(*live) != "Working" {
		t.Errorf("busy: %q under %q", st.label, m.groupLabel(*live))
	}
	sample(activity.Background)
	if st := m.statusOf(*live); st.label != "Background work" || m.groupLabel(*live) != "Working" {
		t.Errorf("background: %q under %q", st.label, m.groupLabel(*live))
	}
	sample(activity.Waiting)
	if st := m.statusOf(*live); !strings.HasPrefix(st.label, "Waiting for you") || st.mark != markAttention {
		t.Errorf("waiting: %+v", st)
	}
	sample(activity.Unknown)
	if st := m.statusOf(*live); st.label != "Running" || m.groupLabel(*live) != "Working" {
		t.Errorf("no signal: %q under %q; unknown never claims idle", st.label, m.groupLabel(*live))
	}
}

func TestTheProbeOutputResolvesBesideUsageAndWork(t *testing.T) {
	out := "cpu=0.1\ncores=4\n" +
		"activity\tclaudecode\tshell\t1795629159114\t1\n" +
		"marker\t1\t{\"harness\":\"claudecode\",\"pid\":9,\"state\":\"busy\",\"since\":1795629160000}\n" +
		"work\t/w/dev\trs-cloud-accounts-ui\tabc\n"
	state, since := activity.Resolve(activity.Parse(out))
	if state != activity.Background || since.UnixMilli() != 1795629159114 {
		t.Errorf("resolved %q at %v", state, since)
	}
	if !strings.Contains(guestActivityScript, "/dev/shm/proveo-activity") || !strings.Contains(guestActivityScript, ".codex/sessions") {
		t.Error("the probe no longer reads the markers or the Codex log")
	}
}
