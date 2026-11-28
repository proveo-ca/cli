// SPEC: _spec/internal/dashboard/schedule-manager.puml
package choiceui

import tea "charm.land/bubbletea/v2"

// Choice is a dialog whose buttons are its answers; Answer is the one accepted, 0 being Cancel.
type Choice struct{ card dialog }

func (c *Choice) Update(msg tea.Msg) (done bool) {
	next, _ := c.card.Update(msg)
	c.card = next.(dialog)
	return c.card.Done()
}

func (c *Choice) Answer() int { return max(0, c.card.answer) }

func (c *Choice) View() tea.View { return c.card.View() }

// Pause answers, as NewPauseChoice numbers its buttons.
const (
	PauseWeek = iota + 1
	PauseFourWeeks
	PauseForever
)

// NewPauseChoice asks how long a running schedule job holds its runs back.
func NewPauseChoice(job string, lines []string, width, height int) *Choice {
	const title = "Pause"
	d := newDialog(title, append([]string{title, "", job, ""}, lines...), 0,
		dialogButton{Label: "Cancel", Key: 'c'}, dialogButton{Label: "1 week", Key: 'w'},
		dialogButton{Label: "4 weeks", Key: '4'}, dialogButton{Label: "Until resumed", Key: 'u'})
	d.w, d.h = width, height
	return &Choice{card: d}
}

// NewResumeConfirm asks before a paused schedule job runs at its times again.
func NewResumeConfirm(job string, lines []string, width, height int) *Confirmation {
	const title = "Resume"
	d := newDialog(title, append([]string{title, "", job, ""}, lines...), 0,
		dialogButton{Label: "Cancel", Key: 'c'}, dialogButton{Label: "Resume", Key: 'r'})
	d.w, d.h = width, height
	return &Confirmation{card: d}
}

// NewEditRefused names why an edit of schedule.yml was not saved; its safe answer reopens the editor.
func NewEditRefused(file string, lines []string, width, height int) *Confirmation {
	const title = "Not saved"
	body := append([]string{title, "", file + " is unchanged; the draft keeps your edit.", ""}, lines...)
	d := newDialog(title, body, 1, dialogButton{Label: "Discard", Key: 'd', Danger: true}, dialogButton{Label: "Edit again", Key: 'e'})
	d.w, d.h = width, height
	return &Confirmation{card: d}
}
