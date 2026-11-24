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
