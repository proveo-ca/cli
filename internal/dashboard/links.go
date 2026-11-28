// SPEC: _spec/internal/dashboard/run-reports.puml
package dashboard

import (
	"net/url"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/proveo-ca/proveo/internal/ui"
)

// linkSpot is one OSC 8 hyperlink on screen: its row, its cells [x0, x1) and its target.
type linkSpot struct {
	y, x0, x1 int
	url       string
}

// linkSpots finds every OSC 8 hyperlink in a drawn frame, one line per screen row.
func linkSpots(lines []string) []linkSpot {
	var spots []linkSpot
	for y, line := range lines {
		if !strings.Contains(line, "\x1b]8;") {
			continue
		}
		var state byte
		x, start, target := 0, 0, ""
		flush := func() {
			if target != "" && x > start {
				spots = append(spots, linkSpot{y: y, x0: start, x1: x, url: target})
			}
		}
		for line != "" {
			seq, w, n, next := ansi.DecodeSequence(line, state, nil)
			state = next
			if body, ok := strings.CutPrefix(seq, "\x1b]8;"); ok {
				body = strings.TrimSuffix(strings.TrimSuffix(body, "\a"), "\x1b\\")
				_, uri, _ := strings.Cut(body, ";")
				flush()
				target, start = uri, x
			}
			x += w
			if n <= 0 {
				break
			}
			line = line[n:]
		}
		flush()
	}
	return spots
}

// linkAt is the hyperlink under cell (x, y) in the last frame drawn.
func (m *dashModel) linkAt(x, y int) (string, bool) {
	for _, s := range m.links {
		if s.y == y && x >= s.x0 && x < s.x1 {
			return s.url, true
		}
	}
	return "", false
}

// copyLink puts a clicked link on the clipboard: a web address as it is, a file link as its path.
func (m *dashModel) copyLink(target string) tea.Cmd {
	if u, err := url.Parse(target); err == nil && u.Scheme == "file" {
		target = u.Path
	}
	return m.copyText(target, keepTail(safeText(target), 72))
}

// linkStyle is every link's look: brand cyan, underlined in the same colour.
var linkStyle = brand.Underline(true).UnderlineColor(hexColor(ui.ColorBrand))

// linked makes text an OSC 8 hyperlink to target in linkStyle; text stays as it is when target is no address.
func linked(target, text string) string {
	if !strings.Contains(target, "://") {
		return text
	}
	return ansi.SetHyperlink(target) + linkStyle.Render(ansi.Strip(text)) + ansi.ResetHyperlink()
}

// ticketURL is where the card's ticket label points: the task's ticket when it is an address, else its Linear issue.
func (m *dashModel) ticketURL(t Task) string {
	if strings.Contains(t.Ticket, "://") {
		return t.Ticket
	}
	return m.metadata[metadataKey(t)].linear.URL
}
