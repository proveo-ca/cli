package dashboard

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/proveo-ca/proveo/internal/ui"
)

func TestLinkSpotsFindEveryHyperlinkInCells(t *testing.T) {
	line := "界 " + ansi.SetHyperlink("https://a.example/1") + "\x1b[1mone\x1b[m" + ansi.ResetHyperlink() + " · " +
		"\x1b]8;id=7;https://b.example/2\x1b\\two\x1b]8;;\x1b\\" + " end"
	spots := linkSpots([]string{"plain row", line})
	if len(spots) != 2 {
		t.Fatalf("spots = %+v", spots)
	}
	if s := spots[0]; s.y != 1 || s.x0 != 3 || s.x1 != 6 || s.url != "https://a.example/1" {
		t.Errorf("first = %+v; a wide rune takes two cells", s)
	}
	if s := spots[1]; s.x0 != 9 || s.x1 != 12 || s.url != "https://b.example/2" {
		t.Errorf("second = %+v; the id parameter and an ESC \\ terminator still parse", s)
	}
}

func clickCopies(t *testing.T, m *dashModel, target string) []string {
	t.Helper()
	var copied []string
	m.clipboard = func(s string) error { copied = append(copied, s); return nil }
	m.View()
	for _, s := range m.links {
		if s.url != target {
			continue
		}
		_, cmd := m.Update(tea.MouseClickMsg(tea.Mouse{X: s.x0 + (s.x1-s.x0)/2, Y: s.y, Button: tea.MouseLeft}))
		for _, msg := range runCmd(cmd) {
			m.Update(msg)
		}
		return copied
	}
	t.Fatalf("no link to %s on screen; links %+v", target, m.links)
	return nil
}

func TestClickingALinkInTaskDetailsCopiesIt(t *testing.T) {
	m := detailsModel(140, 44)
	task := m.data.Tasks[1]
	meta := m.metadata[metadataKey(task)]
	meta.pr.Links = []string{"https://loom.example/share/demo-3995"}
	m.metadata[metadataKey(task)] = meta
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "click a link copies it") {
		t.Errorf("the details card does not say a click copies:\n%s", view)
	}
	copied := clickCopies(t, m, "https://loom.example/share/demo-3995")
	if len(copied) != 1 || copied[0] != "https://loom.example/share/demo-3995" || !strings.Contains(m.status, "Copied loom.example/share/demo-3995") && !strings.Contains(m.status, "Copied https://loom.example/share/demo-3995") {
		t.Errorf("copied %q, status %q", copied, m.status)
	}
	if !m.details {
		t.Error("copying a link closed the details")
	}
	copied = clickCopies(t, m, "https://github.com/proveo-ca/cli/pull/104")
	if len(copied) != 1 || copied[0] != "https://github.com/proveo-ca/cli/pull/104" {
		t.Errorf("the PR link copied %q", copied)
	}
}

func TestClickingALinkInAReportCopiesItAndAFileLinkCopiesItsPath(t *testing.T) {
	m, ended := reportModel(t)
	src := "# Review\n\nThe [ticket](https://linear.example/PLUV-1747) and the long https://docs.example/a/very/long/path/that/wraps/across/the/narrow/report/pane/of/the/dashboard.html here.\n"
	if err := os.WriteFile(ended.Report, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if copied := clickCopies(t, m, "https://linear.example/PLUV-1747"); len(copied) != 1 || copied[0] != "https://linear.example/PLUV-1747" {
		t.Errorf("the markdown link copied %q", copied)
	}
	long := "https://docs.example/a/very/long/path/that/wraps/across/the/narrow/report/pane/of/the/dashboard.html"
	if copied := clickCopies(t, m, long); len(copied) != 1 || copied[0] != long {
		t.Errorf("a wrapped address copied %q, not the whole of it", copied)
	}
	if m.sel.on {
		t.Error("a click on a link also started a selection")
	}
	var copied []string
	m.clipboard = func(s string) error { copied = append(copied, s); return nil }
	path := filepath.Join(t.TempDir(), "plan.md")
	for _, msg := range runCmd(m.copyLink("file://" + path)) {
		m.Update(msg)
	}
	if len(copied) != 1 || copied[0] != path {
		t.Errorf("a file link copied %q", copied)
	}
}

func tipRow(m *dashModel) int {
	for i, line := range strings.Split(ansi.Strip(m.View().Content), "\n") {
		if strings.Contains(line, "Copied to clipboard") {
			return i
		}
	}
	return -1
}

func TestCardPRNumbersAreLinksAndACopyRaisesATooltipFor2400ms(t *testing.T) {
	m := reviewModel(t)
	var url string
	for _, task := range m.data.Tasks {
		if task.Review != nil && task.Review.Number == 3976 {
			url = task.Review.URL
		}
	}
	if url == "" {
		t.Fatal("no ghost for #3976")
	}
	m.View()
	var spot linkSpot
	for _, s := range m.links {
		if s.url == url {
			spot = s
		}
	}
	if spot.url == "" || spot.x1-spot.x0 != len("#3976") {
		t.Fatalf("the ghost's #3976 is not a link to its PR: %+v in %+v", spot, m.links)
	}
	if copied := clickCopies(t, m, url); len(copied) != 1 || copied[0] != url {
		t.Fatalf("copied %q", copied)
	}
	first := m.tip.id
	if row := tipRow(m); row < 0 || row >= spot.y {
		t.Errorf("the tooltip sits at row %d; above the click on row %d", row, spot.y)
	}
	clickCopies(t, m, url)
	if _, cmd := m.Update(tipDoneEvent{first}); cmd != nil || tipRow(m) < 0 {
		t.Error("the first tooltip's timer took down the second")
	}
	m.Update(tipDoneEvent{m.tip.id})
	if tipRow(m) >= 0 {
		t.Error("the tooltip outlived its timer")
	}
	if tooltipFor.Milliseconds() != 2400 {
		t.Errorf("tooltipFor = %v", tooltipFor)
	}
}

func TestATaskCardsTicketNumberLinksToItsTicket(t *testing.T) {
	m := sampleModel(140, 44, ui.GlyphsSafe)
	task := &m.data.Tasks[0]
	task.Ticket, task.Phase = "https://github.com/acme/app/pull/3995", "Review"
	m.View()
	found := false
	for _, s := range m.links {
		found = found || (s.url == task.Ticket && s.x1-s.x0 == len("#3995"))
	}
	if !found {
		t.Errorf("the card's #3995 is not a link to its PR: %+v", m.links)
	}
}

func TestAKeyboardCopyRaisesTheTooltipAtTheRightPanesFoot(t *testing.T) {
	m, _ := reportModel(t)
	var copied []string
	m.clipboard = func(s string) error { copied = append(copied, s); return nil }
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.View()
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'Y', Text: "Y"})
	for _, msg := range runCmd(cmd) {
		m.Update(msg)
	}
	if row := tipRow(m); len(copied) != 1 || row < m.rightPane.y+m.rightPane.h-5 || row >= m.rightPane.y+m.rightPane.h {
		t.Errorf("tooltip row %d; the right pane ends at %d", row, m.rightPane.y+m.rightPane.h)
	}
}

// linkLooks are the SGR sequences inside each OSC 8 span of a frame, keyed by target.
func linkLooks(lines []string) map[string][]string {
	looks := map[string][]string{}
	sgr := regexp.MustCompile("\x1b\\[[0-9;:]*m")
	for _, line := range lines {
		for {
			start := strings.Index(line, "\x1b]8;")
			if start < 0 {
				break
			}
			lead := ""
			if before := sgr.FindAllString(line[:start], -1); len(before) > 0 && before[len(before)-1] != "\x1b[m" && before[len(before)-1] != "\x1b[0m" {
				lead = before[len(before)-1]
			}
			line = line[start+4:]
			end := strings.IndexAny(line, "\a\x1b")
			_, target, _ := strings.Cut(line[:max(0, end)], ";")
			if target == "" {
				continue
			}
			body := line
			if stop := strings.Index(body, "\x1b]8;"); stop >= 0 {
				body = body[:stop]
			}
			looks[target] = append(looks[target], lead+strings.Join(sgr.FindAllString(body, -1), ""))
		}
	}
	return looks
}

func TestEveryLinkIsCyanAndUnderlined(t *testing.T) {
	r, g, b := (ui.ColorBrand>>16)&0xff, (ui.ColorBrand>>8)&0xff, ui.ColorBrand&0xff
	cyan := fmt.Sprintf("38;2;%d;%d;%d", r, g, b)
	check := func(name string, lines []string, want int) {
		t.Helper()
		looks := linkLooks(lines)
		if len(looks) < want {
			t.Fatalf("%s: %d links drawn, want %d at least: %v", name, len(looks), want, looks)
		}
		for target, all := range looks {
			for _, sgr := range all {
				if !strings.Contains(sgr, cyan) || !regexp.MustCompile(`\x1b\[(?:[0-9:;]*;)?4[;m:]`).MatchString(sgr) {
					t.Errorf("%s: the link to %s is drawn %q, not cyan and underlined", name, target, sgr)
				}
			}
		}
	}
	m := reviewModel(t)
	task := &m.data.Tasks[0]
	task.Ticket = "https://github.com/acme/app/pull/3995"
	m.metadata[metadataKey(*task)] = taskMetadata{id: task.ID, key: metadataKey(*task),
		pr: prInfo{Number: 104, State: "MERGED", URL: "https://github.com/proveo-ca/cli/pull/104", Links: []string{"https://loom.example/x"}}}
	check("cards", strings.Split(m.View().Content, "\n"), 3)
	m.selected, m.details = 0, true
	for i, x := range m.data.Tasks {
		if x.ID == task.ID {
			m.selected = i
		}
	}
	check("details", strings.Split(m.View().Content, "\n"), 4)
	rm, ended := reportModel(t)
	if err := os.WriteFile(ended.Report, []byte("See [the ticket](https://linear.example/PLUV-1) and https://docs.example/a.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rm.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	check("report", strings.Split(rm.View().Content, "\n"), 2)
}
