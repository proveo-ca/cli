// SPEC: _spec/internal/dashboard/ended-runs.puml
package dashboard

import (
	"errors"
	"os/exec"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/proveo-ca/proveo/internal/activity"
	"github.com/proveo-ca/proveo/internal/choiceui"
)

// endedRun is an Attention card whose agent no longer runs: no session to attach to, and either no guest or a job
// whose run recorded its end, so a guest still up is a kept sandbox, not an agent in another terminal. A guest whose own
// sample says every agent exited, or whose seed refused the agent, is ended too.
func (m *dashModel) endedRun(t Task) bool {
	if t.ID == "" || t.Review != nil || t.Live || m.groupOf(t) != 1 {
		return false
	}
	st, _ := m.activityOf(t)
	return !t.Guest || t.Job != "" && t.Outcome != "" || st == activity.Exited
}

// canFollowUp is true when the run left an instance whose conversation `proveo run --continue` resumes, and its
// sandbox did not refuse the agent: mounts are fixed at creation, so a refused seed refuses again.
func (m *dashModel) canFollowUp(t Task) bool {
	return t.Instance != "" && t.Agent != "" && m.seedRefusal(t) == ""
}

// seedRefusal is why the task's sandbox refused to release its agent, as the last guest sample read it.
func (m *dashModel) seedRefusal(t Task) string {
	if s, ok := m.sampleFor(t); ok && t.Guest {
		return s.seedRefused
	}
	return ""
}

// canRetry is true when the job's last run failed, as `proveo schedule retry` requires.
func canRetry(t Task) bool { return t.Job != "" && (t.Outcome == "failed" || t.Outcome == "not-ready") }

// canCleanUp is true for what `proveo schedule drop` forgets: a job the dashboard made, or an instance no job owns.
func canCleanUp(t Task) bool {
	return t.DashboardJob || (t.Job == "" && t.Instance != "" && t.ID == t.Instance)
}

// followUpArgs resume t's conversation in its own instance; a review keeps its read-only shares.
func followUpArgs(t Task) []string {
	args := []string{"run", t.Agent, "--instance", t.Instance, "--continue"}
	if _, review := reviewPR(t); review {
		args = append(args, "--read-only-shares")
	}
	return args
}

// openFollowUp asks before resuming an ended run's conversation as a new session; without an instance it says what is left.
func (m *dashModel) openFollowUp(t Task) {
	if !m.canFollowUp(t) {
		m.confirm = m.endedNotice(t)
		return
	}
	lines := []string{}
	if t.Sandbox != "" {
		lines = append(lines, "Sandbox  "+safeText(t.Sandbox))
	}
	lines = append(lines, "Runs     proveo "+safeText(strings.Join(followUpArgs(t), " ")), "",
		"It opens in its own tmux session; Ctrl+B d detaches back here.")
	m.confirm = choiceui.NewFollowUpConfirm(agentLabel(t.Agent), safeText(m.titleFor(t)), lines, m.width, m.height)
	m.onConfirm = func() tea.Cmd {
		title := safeText(m.titleFor(t))
		return m.handoffTo("Following up on "+title, strings.Join(nonEmpty(t.Sandbox, agentLabel(t.Agent)), " · "),
			hint("Ctrl+B d", "detaches back here"), "Back from "+title, m.Binary, followUpArgs(t)...)
	}
}

// endedNotice is what Enter says on an ended run with no conversation to resume: the keys that are left.
func (m *dashModel) endedNotice(t Task) *choiceui.Confirmation {
	lines := []string{"This run has ended and left no conversation to resume.", ""}
	if why := m.seedRefusal(t); why != "" {
		lines = []string{"Its sandbox refused to start the agent: " + why + ".",
			"The same sandbox would refuse again; n starts a new instance with today's mounts.", ""}
	}
	if canRetry(t) {
		lines = append(lines, "r  runs the job again from the start")
	}
	if canCleanUp(t) {
		lines = append(lines, "x  cleans it up")
	}
	lines = append(lines, "t  shows its details")
	return choiceui.NewNotice("Nothing to follow up", lines, m.width, m.height)
}

type endedEvent struct {
	id, verb string
	err      error
}

// retry starts the ended job again through `proveo schedule retry`.
func (m *dashModel) retry(t Task) tea.Cmd {
	if !m.endedRun(t) || !canRetry(t) || m.ending[t.ID] {
		return nil
	}
	m.markEnding(t.ID)
	m.say("Retrying " + safeText(m.titleFor(t)) + "…")
	return tea.Batch(m.scheduleCmd(t.ID, "retry", "retry", t.Job), m.loadTick())
}

// openCleanUp asks before `proveo schedule drop` forgets the ended run, naming what goes and what stays.
func (m *dashModel) openCleanUp(t Task) {
	if !m.endedRun(t) || !canCleanUp(t) || m.ending[t.ID] {
		return
	}
	var lines []string
	if t.Sandbox != "" {
		lines = append(lines, "Removes  sandbox "+safeText(t.Sandbox)+", after its work comes home")
	}
	if t.Instance != "" {
		lines = append(lines, "Forgets  instance "+safeText(t.Instance)+" and its conversation")
	}
	if t.DashboardJob {
		lines = append(lines, "Forgets  job "+safeText(t.Job)+" and its queue entry")
	}
	lines = append(lines, "Keeps    the logs and the commits under refs/proveo/")
	if n, review := reviewPR(t); review {
		lines = append(lines, "", "#"+strconv.Itoa(n)+" returns to Review requests while your review is still requested.")
	}
	id := t.ID
	if t.Job != "" {
		id = t.Job
	}
	m.confirm = choiceui.NewCleanUpConfirm(safeText(m.titleFor(t)), lines, m.width, m.height)
	m.onConfirm = func() tea.Cmd {
		m.markEnding(t.ID)
		m.say("Cleaning up " + safeText(m.titleFor(t)) + "…")
		args := []string{"drop", id}
		if t.Guest {
			args = append(args, "--stop")
		}
		return tea.Batch(m.scheduleCmd(t.ID, "clean up", args...), m.loadTick())
	}
}

func (m *dashModel) markEnding(id string) {
	if m.ending == nil {
		m.ending = map[string]bool{}
	}
	m.ending[id] = true
}

// scheduleCmd runs `proveo schedule <args>` in the dashboard's workspace and reports its last line on failure.
func (m *dashModel) scheduleCmd(id, label string, args ...string) tea.Cmd {
	bin, cwd := m.Binary, m.CWD
	return func() tea.Msg {
		cmd := exec.Command(bin, append([]string{"schedule"}, args...)...)
		cmd.Dir = cwd
		out, err := cmd.CombinedOutput()
		if err != nil {
			if why := lastLine(string(out)); why != "" {
				err = errors.New(why)
			}
		}
		return endedEvent{id: id, verb: label, err: err}
	}
}
