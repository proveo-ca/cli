package choiceui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func pickerSample() *TaskPicker {
	return NewTaskPicker([]TaskSection{
		{Title: "Plans", Items: []TaskItem{
			{Title: "Secret custody", Status: "PLANNED", Prompt: "Implement the plan in _spec/_plans/secret-custody.puml.", Task: "Secret custody"},
			{Title: "Ship and reach", Status: "no status line", Prompt: "Implement ship-and-reach.", Task: "Ship and reach"},
		}},
		{Title: "Pull requests", Items: []TaskItem{
			{Title: "#3731–#3738 PLUV-1402: Classify PDFs", Status: "8 PRs: 8 ready", Tone: "ok", Prompt: "Continue PLUV-1402.", Task: "PLUV-1402"},
		}},
		{Title: "Linear", Note: "loading…"},
	}, 120, 40)
}

func pickKey(t *TaskPicker, k string) (bool, bool) {
	switch k {
	case "enter":
		return t.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	case "esc":
		return t.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	case "down":
		return t.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	case "backspace":
		return t.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	var done, start bool
	for _, r := range k {
		done, start = t.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	return done, start
}

func TestTheTaskPickerListsSectionsUnderAFilter(t *testing.T) {
	p := pickerSample()
	view := ansi.Strip(p.View().Content)
	for _, want := range []string{"New instance", "type to filter the tasks", "Blank session", "Plans  2", "Secret custody", "PLANNED",
		"Pull requests  1", "8 PRs: 8 ready", "Linear", "loading…", "↵ pick"} {
		if !strings.Contains(view, want) {
			t.Errorf("the picker lacks %q:\n%s", want, view)
		}
	}
	pickKey(p, "pdf")
	view = ansi.Strip(p.View().Content)
	if !strings.Contains(view, "PLUV-1402") || strings.Contains(view, "Secret custody") || !strings.Contains(view, "nothing matches") {
		t.Errorf("filtering by pdf:\n%s", view)
	}
	pickKey(p, "backspace")
	pickKey(p, "backspace")
	pickKey(p, "backspace")
	p.SetSection(2, TaskSection{Title: "Linear", Items: []TaskItem{{Title: "CONN-123 Connector errors hide why", Status: "In Progress", Tone: "ok", Prompt: "Work on CONN-123."}}})
	if view := ansi.Strip(p.View().Content); !strings.Contains(view, "CONN-123") || strings.Contains(view, "loading…") {
		t.Errorf("a section that answered later did not fill in:\n%s", view)
	}
	if score, ok := fuzzy("scu", "Secret custody"); !ok || score <= 0 {
		t.Errorf("fuzzy(scu) = %d, %v", score, ok)
	}
	if _, ok := fuzzy("zz", "Secret custody"); ok {
		t.Error("fuzzy matched letters the text lacks")
	}
}

func TestPickingATaskOpensItsPromptToEditThenStarts(t *testing.T) {
	p := pickerSample()
	pickKey(p, "down")
	if done, _ := pickKey(p, "enter"); done || !p.editing {
		t.Fatal("enter on a task did not open its prompt")
	}
	view := ansi.Strip(p.View().Content)
	for _, want := range []string{"Secret custody", "Prompt", "Implement the plan in _spec/_plans/secret-custody.puml.", "Start", "esc back to tasks"} {
		if !strings.Contains(view, want) {
			t.Errorf("the prompt step lacks %q:\n%s", want, view)
		}
	}
	pickKey(p, " Keep D3 out.")
	pickKey(p, "esc")
	if p.editing || p.done {
		t.Fatal("esc did not return to the tasks")
	}
	pickKey(p, "down")
	pickKey(p, "enter")
	if done, start := pickKey(p, "enter"); !done || !start {
		t.Fatal("enter on the prompt did not start")
	}
	if task, prompt, branch := p.Result(); task != "Ship and reach" || prompt != "Implement ship-and-reach." || branch != "" {
		t.Errorf("result = %q, %q", task, prompt)
	}
}

func TestTheBlankSessionStartsWithNoTaskAndEscCancels(t *testing.T) {
	p := pickerSample()
	if done, start := pickKey(p, "enter"); !done || !start {
		t.Fatal("enter on the blank session did not start")
	}
	if task, prompt, branch := p.Result(); task != "" || prompt != "" || branch != "" {
		t.Errorf("a blank session carried %q, %q", task, prompt)
	}
	p = pickerSample()
	if done, start := pickKey(p, "esc"); !done || start {
		t.Error("esc did not cancel")
	}
}

func TestWrapRunesBreaksAtSpacesAndNewlinesAndFindsTheCursor(t *testing.T) {
	text := []rune("Continue PLUV-1402:\n- #3731 ready\n- #3732 ready and a long tail that wraps")
	lines, cur := wrapRunes(text, len(text), 20)
	if len(lines) < 4 || string(lines[0]) != "Continue PLUV-1402:" || string(lines[1]) != "- #3731 ready" {
		t.Fatalf("lines = %q", lines)
	}
	if cur[0] != len(lines)-1 || cur[1] != len(lines[len(lines)-1]) {
		t.Errorf("cursor = %v for %d lines", cur, len(lines))
	}
}

func TestALongSectionShowsEightRowsUntilTheFilterIsTyped(t *testing.T) {
	var items []TaskItem
	for i := range 11 {
		items = append(items, TaskItem{Title: "Plan " + itoa(i+1) + " alpha", Prompt: "p"})
	}
	p := NewTaskPicker([]TaskSection{{Title: "Plans", Items: items}, {Title: "Linear", Items: []TaskItem{{Title: "CONN-1 beta"}}}}, 120, 40)
	view := ansi.Strip(p.View().Content)
	if !strings.Contains(view, "Plans  11") || !strings.Contains(view, "Plan 8 alpha") || strings.Contains(view, "Plan 9 alpha") ||
		!strings.Contains(view, "+3 more · type to filter") || !strings.Contains(view, "CONN-1 beta") {
		t.Errorf("an unfiltered long section:\n%s", view)
	}
	pickKey(p, "alpha")
	view = ansi.Strip(p.View().Content)
	if !strings.Contains(view, "Plan 11 alpha") || strings.Contains(view, "more · type to filter") || strings.Contains(view, "CONN-1") {
		t.Errorf("a filter should reach every row:\n%s", view)
	}
}

func TestThePromptStepCarriesABranchTabEditsIt(t *testing.T) {
	p := NewTaskPicker([]TaskSection{{Title: "Linear", Items: []TaskItem{{Title: "CONN-123 Connector errors", Prompt: "Work on CONN-123.", Branch: "CONN-123"}}}}, 120, 40)
	pickKey(p, "down")
	pickKey(p, "enter")
	view := ansi.Strip(p.View().Content)
	if !strings.Contains(view, "Branch") || !strings.Contains(view, "› CONN-123") || !strings.Contains(view, "tab prompt · branch") {
		t.Errorf("the prompt step lacks its branch:\n%s", view)
	}
	p.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	pickKey(p, " oauth errors")
	if _, prompt, branch := p.Result(); branch != "CONN-123-oauth-errors" || prompt != "Work on CONN-123." {
		t.Errorf("after tab and typing: prompt %q, branch %q; spaces become dashes", prompt, branch)
	}
	for range len("-oauth-errors") + len("CONN-123") {
		pickKey(p, "backspace")
	}
	if view := ansi.Strip(p.View().Content); !strings.Contains(view, "none: proveo/<instance>, kept on this machine") {
		t.Errorf("an empty branch does not say where the work stays:\n%s", view)
	}
}
