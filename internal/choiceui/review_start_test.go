// SPEC: _spec/internal/dashboard/review-requests.puml
package choiceui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestTheReviewStartCardPicksATemplateAndTakesNotes(t *testing.T) {
	r := NewReviewStart("#3975 Move cloud accounts · @dana", "Claude Code · headless", []ReviewTemplate{
		{"Adversarial review", "map first, adversarial findings"}, {"Concern-led review", "driven by my concerns"}, {"Map + demo", "reviewer aids"},
	}, 120, 40)
	out := plain(r.View().Content)
	for _, want := range []string{"Start a review", "#3975 Move cloud accounts", "Claude Code · headless", "Prompt", "● Adversarial review",
		"○ Concern-led review", "Notes", "optional: concerns", "Start", "Cancel", "↑↓ prompt", "esc cancel", "╭", "╰"} {
		if !strings.Contains(out, want) {
			t.Errorf("the card lacks %q:\n%s", want, out)
		}
	}
	key := func(k tea.KeyPressMsg) (bool, bool) { return r.Update(k) }
	key(tea.KeyPressMsg{Code: tea.KeyUp})
	if sel, _ := r.Result(); sel != 2 {
		t.Errorf("up from the first wraps to the last, got %d", sel)
	}
	key(tea.KeyPressMsg{Code: tea.KeyDown})
	key(tea.KeyPressMsg{Code: tea.KeyDown})
	for _, c := range "auth flow" {
		key(tea.KeyPressMsg{Code: c, Text: string(c)})
	}
	key(tea.KeyPressMsg{Code: tea.KeyBackspace})
	key(tea.KeyPressMsg{Code: tea.KeyLeft})
	key(tea.KeyPressMsg{Code: 'X', Text: "X"})
	r.Update(tea.PasteMsg{Content: "  and\nlogs "})
	if sel, notes := r.Result(); sel != 1 || notes != "auth flXand logso" {
		t.Errorf("template %d, notes %q", sel, notes)
	}
	if !strings.Contains(plain(r.View().Content), "● Concern-led review") {
		t.Error("the chosen template is not marked")
	}
	if done, start := key(tea.KeyPressMsg{Code: 's', Text: "s"}); done || start {
		t.Error("a typed letter is a note, not a button")
	}
	if done, start := key(tea.KeyPressMsg{Code: tea.KeyEnter}); !done || !start {
		t.Error("enter should start")
	}
	c := NewReviewStart("pr", "agent", []ReviewTemplate{{"a", "b"}}, 80, 24)
	if done, start := c.Update(tea.KeyPressMsg{Code: tea.KeyEscape}); !done || start {
		t.Error("esc should cancel")
	}
}

func TestTheVerdictCardOffersGitHubsThreeVerdictsAndNeedsTextForTwo(t *testing.T) {
	v := NewVerdict("#3975 Move cloud accounts · @dana", "Posts to GitHub as your gh user", 120, 40)
	out := plain(v.View().Content)
	for _, want := range []string{"Submit a review", "Verdict", "● Comment", "○ Approve", "○ Request changes", "Comment",
		"required for Comment and Request changes", "Submit", "Cancel", "↑↓ verdict", "↵ submit"} {
		if !strings.Contains(out, want) {
			t.Errorf("the verdict card lacks %q:\n%s", want, out)
		}
	}
	if done, _ := v.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); done {
		t.Fatal("an empty comment was submitted")
	}
	if !strings.Contains(plain(v.View().Content), "GitHub needs a comment for comment") {
		t.Errorf("no reason shown:\n%s", plain(v.View().Content))
	}
	v.Update(tea.KeyPressMsg{Code: 'o', Text: "o"})
	if strings.Contains(plain(v.View().Content), "GitHub needs") {
		t.Error("typing did not clear the warning")
	}
	a := NewVerdict("pr", "who", 120, 40)
	a.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if done, submit := a.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); !done || !submit {
		t.Error("approve without text should submit")
	}
	if choice, body := a.Result(); choice != 1 || body != "" {
		t.Errorf("result = %d %q", choice, body)
	}
}
