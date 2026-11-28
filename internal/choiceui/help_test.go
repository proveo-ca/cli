// SPEC: _spec/internal/choiceui/confirm-dialogs.puml
package choiceui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

var helpSample = []HelpSection{
	{Title: "Tasks", Keys: [][2]string{{"↑↓ wheel", "move through tasks"}, {"t", "task details"}, {"n", "new instance"}, {"c", "host capacity"}}},
	{Title: "Files", Keys: [][2]string{{"f g", "files · changes"}, {"/", "filter files"}, {"`", "tree or flat list"}}},
	{Title: "Reading a file", Keys: [][2]string{{"n p", "next · previous file"}, {"d", "file or diff"}}},
	{Title: "Anywhere", Keys: [][2]string{{"Ctrl+G Tab", "proveo or sbx"}, {"Ctrl+C", "quit"}}},
}

func TestTheHelpModalIsAConfirmationCardOfKeys(t *testing.T) {
	h := NewHelp("Keys", helpSample, 120, 40)
	out := plain(h.View().Content)
	for _, want := range []string{"Keys", "Tasks", "Reading a file", "↑↓ wheel    move through tasks", "Ctrl+G Tab  proveo or sbx", "Close", "esc close", "? close", "╭", "╰"} {
		if !strings.Contains(out, want) {
			t.Errorf("the help card lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "choose") {
		t.Errorf("one button needs no ←→ choose:\n%s", out)
	}
	if done, _ := h.Update(tea.KeyPressMsg{Code: tea.KeyEscape}); !done {
		t.Error("esc did not close the help")
	}
}

func TestTheHelpModalTakesTwoColumnsWhenOneIsTooTall(t *testing.T) {
	rows, _, _ := helpRows(helpSample, 120, 40)
	if len(rows[0]) != 1 {
		t.Errorf("a tall terminal split the help into %d columns", len(rows[0]))
	}
	rows, _, colW := helpRows(helpSample, 120, 20)
	if len(rows[0]) != 2 || len(rows) > 20-helpChrome {
		t.Fatalf("a short wide terminal: %d columns, %d rows", len(rows[0]), len(rows))
	}
	out := plain(NewHelp("Keys", helpSample, 120, 20).View().Content)
	line := ""
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "Tasks") {
			line = l
		}
	}
	if !strings.Contains(line, "Reading a file") && !strings.Contains(line, "Files") {
		t.Errorf("the second column does not start beside the first (col %d): %q", colW, line)
	}
	rows, _, _ = helpRows(helpSample, 50, 14)
	last := rows[len(rows)-1]
	if len(rows) > 14-helpChrome || last[0].words != "… a taller terminal shows the rest" {
		t.Errorf("a small terminal: %d rows ending %+v", len(rows), last)
	}
}
