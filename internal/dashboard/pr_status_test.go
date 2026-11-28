package dashboard

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/proveo-ca/proveo/internal/ui"
)

func TestAPRsStatusesSayWhatStandsBetweenItAndAMerge(t *testing.T) {
	ok := []checkItem{{Typename: "CheckRun", Name: "Unit", Status: "COMPLETED", Conclusion: "SUCCESS"}}
	for _, c := range []struct {
		r    ReviewRequest
		want string
	}{
		{ReviewRequest{Mergeable: "MERGEABLE", Checks: checksOf(ok)}, "ready"},
		{ReviewRequest{Mergeable: "UNKNOWN", Checks: checksOf(ok)}, "checking conflicts"},
		{ReviewRequest{Draft: true, Mergeable: "MERGEABLE", Checks: checksOf(ok)}, "draft"},
		{ReviewRequest{Mergeable: "CONFLICTING", Checks: checksOf(append(ok,
			checkItem{Typename: "StatusContext", Context: "ci/legacy", State: "ERROR"},
			checkItem{Typename: "CheckRun", Name: "Deploy preview", Status: "COMPLETED", Conclusion: "TIMED_OUT"}))},
			"conflicts · errors: 2 checks failed"},
		{ReviewRequest{Mergeable: "MERGEABLE", Checks: checksOf(append(ok, checkItem{Typename: "CheckRun", Name: "E2E", Status: "QUEUED"},
			checkItem{Typename: "StatusContext", Context: "ci/x", State: "PENDING"}))}, "checks running"},
	} {
		if got := ansi.Strip(c.r.statusLine()); got != c.want {
			t.Errorf("%+v: %q, want %q", c.r, got, c.want)
		}
	}
	c := checksOf([]checkItem{{Name: "Typecheck", Conclusion: "FAILURE"}, {Name: "Typecheck", Conclusion: "FAILURE"},
		{Name: "Deploy", Conclusion: "CANCELLED"}, {Name: "Lint", Conclusion: "NEUTRAL"}, {Name: "E2E", Conclusion: "SKIPPED"}})
	if c.Failed != 3 || c.Passed != 2 || strings.Join(c.FailedNames, ", ") != "Typecheck, Deploy (cancelled)" {
		t.Errorf("checks = %+v", c)
	}
	if got := ansi.Strip(c.line()); got != "3 failed · 2 passed" {
		t.Errorf("line = %q", got)
	}
}

func TestReviewRequestCardsAndDetailsShowTheirStatus(t *testing.T) {
	m := reviewModel(t)
	if m.reviews[0].Number != 3976 || m.reviews[1].Mergeable != "CONFLICTING" || m.reviews[1].Checks.Failed != 1 {
		t.Fatalf("reviews = %+v", m.reviews)
	}
	view := ansi.Strip(m.View().Content)
	for _, want := range []string{"conflicts · errors: 1 check failed", "draft · checks running"} {
		if !strings.Contains(view, want) {
			t.Errorf("the cards lack %q:\n%s", want, view)
		}
	}
	for i, task := range m.data.Tasks {
		if task.Review != nil && task.Review.Number == 3975 {
			m.selected = i
		}
	}
	m.reviewFailures = map[string]string{"https://github.com/acme/app/pull/3975": "the agent exited 1"}
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "last review failed: the agent exited 1") ||
		!strings.Contains(view, "2 files · +40 -7 · conflicts") {
		t.Errorf("a failure message pushes the status onto the size line:\n%s", view)
	}
	m.reviewFailures, m.details = nil, true
	view = ansi.Strip(m.View().Content)
	for _, want := range []string{"Status   conflicts · errors: 1 check failed", "Reviews  approved by @kai · 1 commented", "Checks   1 failed · 2 passed", "Failed   Typecheck"} {
		if !strings.Contains(view, want) {
			t.Errorf("the details lack %q:\n%s", want, view)
		}
	}
}

func TestAReviewRequestWithAReviewAlreadyCarriesAChip(t *testing.T) {
	m := reviewModel(t)
	view := ansi.Strip(m.View().Content)
	found := false
	for _, line := range strings.Split(view, "\n") {
		found = found || (strings.Contains(line, "✓ Approved by @kai") && strings.Contains(line, "╭"))
	}
	if !found {
		t.Errorf("the approved request's top edge lacks its chip:\n%s", view)
	}
	if strings.Count(view, "Approved by") != 1 {
		t.Errorf("a request nobody reviewed carries a chip:\n%s", view)
	}
	for _, c := range []struct {
		r    ReviewRequest
		want string
	}{
		{ReviewRequest{Reviews: prReviews{Approved: []string{"a", "b"}}}, " ✓ Approved by @a +1 "},
		{ReviewRequest{Decision: "APPROVED"}, " ✓ Approved "},
		{ReviewRequest{Decision: "CHANGES_REQUESTED", Reviews: prReviews{Approved: []string{"a"}, ChangesRequested: []string{"b"}}}, " Changes requested by @b "},
		{ReviewRequest{Decision: "CHANGES_REQUESTED"}, " Changes requested "},
		{ReviewRequest{Decision: "REVIEW_REQUIRED", Reviews: prReviews{Commented: []string{"bot"}}}, ""},
	} {
		if got := ansi.Strip(m.reviewChip(c.r)); got != c.want {
			t.Errorf("%+v: chip %q, want %q", c.r, got, c.want)
		}
	}
	m.glyphs = ui.GlyphsASCII
	if got := ansi.Strip(m.reviewChip(ReviewRequest{Decision: "APPROVED"})); got != "[Approved]" {
		t.Errorf("ASCII chip = %q", got)
	}
	if got := reviewsOf([]reviewItem{{State: "APPROVED"}, {Author: struct{ Login string }{"kai"}, State: "DISMISSED"}}); len(got.Approved)+len(got.ChangesRequested)+len(got.Commented) != 0 {
		t.Errorf("a review without an author or a dismissed one counted: %+v", got)
	}
}
