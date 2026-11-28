// SPEC: _spec/internal/choiceui/confirm-dialogs.puml
package choiceui

import tea "charm.land/bubbletea/v2"

type Confirmation struct{ card dialog }

func NewAttachConfirm(agent, task string, width, height int) *Confirmation {
	const title = "Attach"
	d := newDialog(title, []string{title, "",
		"You will now enter the " + agent + " session in charge of the task",
		task,
	}, 0, dialogButton{Label: "Cancel", Key: 'c'}, dialogButton{Label: "Attach", Key: 'a'})
	d.w, d.h = width, height
	return &Confirmation{card: d}
}

func NewDashboardExitConfirm(width, height int) *Confirmation {
	const title = "Exit Proveo?"
	d := newDialog(title, []string{title, "", "Queued and running tasks continue."}, 0,
		dialogButton{Label: "Cancel", Key: 'c'}, dialogButton{Label: "Exit", Key: 'e'})
	d.w, d.h = width, height
	return &Confirmation{card: d}
}

func (c *Confirmation) Update(msg tea.Msg) (done, confirmed bool) {
	next, _ := c.card.Update(msg)
	c.card = next.(dialog)
	return c.card.Done(), c.card.answer == 1
}

func (c *Confirmation) View() tea.View { return c.card.View() }

// NewNotice is a confirmation card that only informs: the title, its lines and one OK button.
func NewNotice(title string, lines []string, width, height int) *Confirmation {
	d := newDialog(title, append([]string{title, ""}, lines...), 0, dialogButton{Label: "OK", Key: 'o'})
	d.w, d.h = width, height
	return &Confirmation{card: d}
}

// NewFollowUpConfirm asks before resuming an ended run's conversation in its own instance, as a new session.
func NewFollowUpConfirm(agent, task string, lines []string, width, height int) *Confirmation {
	const title = "Follow up"
	body := append([]string{title, "", "You will resume the " + agent + " conversation that ran the task", task, ""}, lines...)
	d := newDialog(title, body, 0, dialogButton{Label: "Cancel", Key: 'c'}, dialogButton{Label: "Follow up", Key: 'f'})
	d.w, d.h = width, height
	return &Confirmation{card: d}
}

// NewCleanUpConfirm asks before forgetting an ended run: its lines name what goes and what stays.
func NewCleanUpConfirm(task string, lines []string, width, height int) *Confirmation {
	const title = "Clean up"
	d := newDialog(title, append([]string{title, "", task, ""}, lines...), 0,
		dialogButton{Label: "Cancel", Key: 'c'}, dialogButton{Label: "Clean up", Key: 'x', Danger: true})
	d.w, d.h = width, height
	return &Confirmation{card: d}
}
