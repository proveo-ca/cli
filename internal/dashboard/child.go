// SPEC: _spec/internal/dashboard/workspace-instances.puml
package dashboard

import tea "charm.land/bubbletea/v2"

type childTerminal interface {
	Frame() (string, *tea.Cursor)
	Input(tea.Msg)
	Resize(int, int)
	Status() (string, bool)
	Close()
}
