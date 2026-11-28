// SPEC: _spec/internal/dashboard/review-requests.puml
package dashboard

import (
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/proveo-ca/proveo/internal/choiceui"
)

// verdictTarget is the pull request a verdict goes to: a review request, or the PR a review run reviews.
type verdictTarget struct {
	url, title, author string
	number             int
}

type verdictEvent struct {
	number int
	choice int
	err    error
}

// verdictFor names the PR the selected task can take a verdict on.
func (m *dashModel) verdictFor(t Task) (verdictTarget, bool) {
	n, ok := reviewPR(t)
	if !ok {
		return verdictTarget{}, false
	}
	if r := t.Review; r != nil {
		return verdictTarget{url: r.URL, title: r.Title, author: r.Author, number: n}, true
	}
	title := strings.TrimPrefix(t.Title, fmt.Sprintf("Review #%d: ", n))
	for _, r := range m.reviews {
		if r.URL == t.Ticket {
			return verdictTarget{url: r.URL, title: r.Title, author: r.Author, number: n}, true
		}
	}
	return verdictTarget{url: t.Ticket, title: title, number: n}, true
}

// openVerdict shows the card that submits the operator's review of the selected task's PR.
func (m *dashModel) openVerdict() {
	v, ok := m.verdictFor(m.task())
	if !ok {
		return
	}
	pr := fmt.Sprintf("#%d %s", v.number, safeText(v.title))
	if v.author != "" {
		pr += " · @" + safeText(v.author)
	}
	m.verdictPR, m.verdictModal = v, choiceui.NewVerdict(pr, "Posts to GitHub as your gh user · you submit it, proveo never does", m.width, m.height)
}

// verdictFlags are gh pr review's flags for Comment, Approve and Request changes.
var verdictFlags = []string{"--comment", "--approve", "--request-changes"}

// verdictDone is the status line after a verdict lands.
var verdictDone = []string{"Commented on", "Approved", "Requested changes on"}

// submitVerdict runs gh pr review for the operator.
func (m *dashModel) submitVerdict(v verdictTarget, choice int, body string) tea.Cmd {
	if choice < 0 || choice >= len(verdictFlags) {
		return nil
	}
	args := []string{"pr", "review", v.url, verdictFlags[choice]}
	if body != "" {
		args = append(args, "--body", body)
	}
	dir := m.CWD
	return func() tea.Msg {
		out, err := reviewCommand(dir, args...)
		if err != nil {
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				if why := lastLine(string(exit.Stderr)); why != "" {
					err = errors.New(why)
				}
			} else if why := lastLine(string(out)); why != "" {
				err = errors.New(why)
			}
		}
		return verdictEvent{number: v.number, choice: choice, err: err}
	}
}

// verdictButton is the Verdict button the file tree ends with when its task has a PR to judge, drawn as the
// pop-ups draw theirs: v is its key.
func (m *dashModel) verdictButton(w int) string {
	label := "Verdict"
	if v, ok := m.verdictFor(m.task()); ok && w >= 22 {
		label += " on #" + strconv.Itoa(v.number)
	}
	return pad(" "+choiceui.Button(label, 'v', false, m.glyphs), w)
}

// runningReview is the agentic review running on the PR the tree shows: the selected task, or the run that took its PR.
func (m *dashModel) runningReview() (Task, bool) {
	v, ok := m.verdictFor(m.task())
	if !ok {
		return Task{}, false
	}
	for _, t := range m.data.Tasks {
		if _, isReview := reviewPR(t); isReview && t.Review == nil && t.Ticket == v.url && t.Live {
			return t, true
		}
	}
	return Task{}, false
}

// reviewCard is the running review's card, compact, for the foot of the file tree: status, title, agent and a attach.
func (m *dashModel) reviewCard(t Task, w int) []string {
	inner := max(1, w-4)
	st := m.statusOf(t)
	mark := glyph(m.glyphs, st.mark)
	if mark != "" {
		mark = st.style.Render(mark) + " "
	}
	title := mark + inkText.Bold(true).Render(ansi.Truncate(safeText(t.Title), max(1, inner-ansi.StringWidth(ansi.Strip(mark))), "…"))
	who := muted.Render(agentLabel(t.Agent)+" · ") + st.style.Render(st.label)
	return m.cardFrame([]string{title, ansi.Truncate(who, inner, "…"), hint("a", "attach to reach a verdict")}, w, m.chip("Agentic review"), false)
}
