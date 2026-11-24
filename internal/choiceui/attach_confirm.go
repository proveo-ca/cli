// SPEC: _spec/internal/choiceui/confirm-dialogs.puml
package choiceui

import tea "charm.land/bubbletea/v2"

type AttachConfirm struct{ card dialog }

func NewAttachConfirm(agent, task string, width, height int) *AttachConfirm {
	const title = "Attach"
	d := newDialog(title, []string{title, "",
		"You will now enter the " + agent + " session in charge of the task",
		task,
	}, 0, dialogButton{Label: "Cancel", Key: 'c'}, dialogButton{Label: "Attach", Key: 'a'})
	d.w, d.h = width, height
	return &AttachConfirm{card: d}
}

func (c *AttachConfirm) Update(msg tea.Msg) (done, attach bool) {
	next, _ := c.card.Update(msg)
	c.card = next.(dialog)
	return c.card.Done(), c.card.answer == 1
}

func (c *AttachConfirm) View() tea.View { return c.card.View() }
