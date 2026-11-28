// SPEC: _spec/internal/dashboard/review-requests.puml
package dashboard

import (
	"fmt"
	"slices"
	"strings"

	"github.com/proveo-ca/proveo/internal/ui"
)

// checkItem is one entry of gh's statusCheckRollup: a check run (status, conclusion) or a commit status (state).
type checkItem struct {
	Typename                                 string `json:"__typename"`
	Name, Context, Status, Conclusion, State string
}

// prChecks counts a PR's checks by how they ended; FailedNames names the ones that did not pass.
type prChecks struct {
	Failed, Pending, Passed int
	FailedNames             []string
}

// checksOf sorts the rollup: a failure, a timeout, a cancellation or a required action fails; anything unfinished is
// pending; a success, a skip or a neutral result passes.
func checksOf(items []checkItem) prChecks {
	var c prChecks
	for _, it := range items {
		name := safeText(firstNonEmpty(it.Name, it.Context, "a check"))
		switch outcome := firstNonEmpty(it.Conclusion, it.State); outcome {
		case "FAILURE", "ERROR", "TIMED_OUT", "CANCELLED", "ACTION_REQUIRED", "STARTUP_FAILURE":
			c.Failed++
			if outcome != "FAILURE" && outcome != "ERROR" {
				name += " (" + strings.ToLower(strings.ReplaceAll(outcome, "_", " ")) + ")"
			}
			if !slices.Contains(c.FailedNames, name) {
				c.FailedNames = append(c.FailedNames, name)
			}
		case "PENDING", "EXPECTED":
			c.Pending++
		case "":
			if it.Status != "" && it.Status != "COMPLETED" {
				c.Pending++
			} else {
				c.Passed++
			}
		default:
			c.Passed++
		}
	}
	return c
}

// statuses are what stands between a PR and a merge, in its tone: draft, conflicts, errors, checks running; ready
// when nothing does; checking conflicts while GitHub has not computed mergeability.
func (r ReviewRequest) statuses() []string {
	var out []string
	if r.Draft {
		out = append(out, muted.Render("draft"))
	}
	if r.Mergeable == "CONFLICTING" {
		out = append(out, errorText.Render("conflicts"))
	}
	switch c := r.Checks; {
	case c.Failed > 0:
		out = append(out, errorText.Render(fmt.Sprintf("errors: %d %s failed", c.Failed, plural(c.Failed, "check", "checks"))))
	case c.Pending > 0:
		out = append(out, warnText.Render("checks running"))
	}
	switch {
	case len(out) > 0:
	case r.Mergeable == "MERGEABLE":
		out = append(out, successText.Render("ready"))
	default:
		out = append(out, muted.Render("checking conflicts"))
	}
	return out
}

func (r ReviewRequest) statusLine() string { return strings.Join(r.statuses(), muted.Render(" · ")) }

// line is "1 failed · 3 running · 21 passed" for the details card.
func (c prChecks) line() string {
	var parts []string
	if c.Failed > 0 {
		parts = append(parts, errorText.Render(fmt.Sprintf("%d failed", c.Failed)))
	}
	if c.Pending > 0 {
		parts = append(parts, warnText.Render(fmt.Sprintf("%d running", c.Pending)))
	}
	if c.Passed > 0 {
		parts = append(parts, muted.Render(fmt.Sprintf("%d passed", c.Passed)))
	}
	if len(parts) == 0 {
		return muted.Render("none reported")
	}
	return strings.Join(parts, muted.Render(" · "))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// reviewItem is one entry of gh's latestReviews: a reviewer and the state of their latest review.
type reviewItem struct {
	Author struct{ Login string }
	State  string
}

// prReviews are a PR's reviewers by the state of their latest review.
type prReviews struct {
	Approved, ChangesRequested, Commented []string
}

func reviewsOf(items []reviewItem) prReviews {
	var r prReviews
	for _, it := range items {
		who := safeText(it.Author.Login)
		if who == "" {
			continue
		}
		switch it.State {
		case "APPROVED":
			r.Approved = append(r.Approved, who)
		case "CHANGES_REQUESTED":
			r.ChangesRequested = append(r.ChangesRequested, who)
		case "COMMENTED":
			r.Commented = append(r.Commented, who)
		}
	}
	return r
}

// byWhom is "@a", or "@a +2" when more share the state.
func byWhom(who []string) string {
	if len(who) == 0 {
		return ""
	}
	s := "@" + who[0]
	if len(who) > 1 {
		s += fmt.Sprintf(" +%d", len(who)-1)
	}
	return s
}

var (
	approvedChip = chipStyle.Foreground(hexColor(ui.ColorSuccess))
	changesChip  = chipStyle.Foreground(hexColor(ui.ColorWarn))
)

// reviewChip rides a review request's top edge when someone already reviewed it: changes requested before an approval.
func (m *dashModel) reviewChip(r ReviewRequest) string {
	text, style := "", approvedChip
	switch rv := r.Reviews; {
	case len(rv.ChangesRequested) > 0:
		text, style = "Changes requested by "+byWhom(rv.ChangesRequested), changesChip
	case r.Decision == "CHANGES_REQUESTED":
		text, style = "Changes requested", changesChip
	case len(rv.Approved) > 0:
		text = "✓ Approved by " + byWhom(rv.Approved)
	case r.Decision == "APPROVED":
		text = "✓ Approved"
	}
	if text == "" {
		return ""
	}
	if m.glyphs == ui.GlyphsASCII || m.glyphs == ui.GlyphsOff {
		return muted.Render("[" + strings.TrimPrefix(text, "✓ ") + "]")
	}
	return style.Render(" " + text + " ")
}

// line is "approved by @a · changes requested by @b · 2 commented" for the details card.
func (rv prReviews) line() string {
	var parts []string
	if len(rv.Approved) > 0 {
		parts = append(parts, successText.Render("approved by @"+strings.Join(rv.Approved, ", @")))
	}
	if len(rv.ChangesRequested) > 0 {
		parts = append(parts, warnText.Render("changes requested by @"+strings.Join(rv.ChangesRequested, ", @")))
	}
	if n := len(rv.Commented); n > 0 {
		parts = append(parts, muted.Render(fmt.Sprintf("%d commented", n)))
	}
	if len(parts) == 0 {
		return muted.Render("none yet")
	}
	return strings.Join(parts, muted.Render(" · "))
}
