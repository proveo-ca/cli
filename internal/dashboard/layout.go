// SPEC: _spec/_paradigms/dashboard-terminal-design.puml
package dashboard

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

const (
	minCols, minRows = 40, 10
	splitCols        = 60
	portraitMaxCols  = 84
	portraitMinRows  = 46
	shortRows        = 16
	capacityRows     = 3 // the host capacity line inside its frame
)

// layout is the cell budget of one Proveo frame below the header row.
type layout struct {
	w, h            int
	tooSmall        bool
	single          bool // one pane fills the width
	portrait        bool // Changes stacked above the queue
	short           bool // no capacity row; details replace the queue
	left, right     int  // split widths
	top             int  // portrait rows for Changes
	body            int  // pane rows between the chrome lines
	capacityVisible bool
}

func newLayout(w, h, sbxSplit int) layout {
	l := layout{w: w, h: h}
	if w < minCols || h < minRows {
		l.tooSmall = true
		return l
	}
	l.short = h < shortRows
	l.capacityVisible = !l.short
	chrome := 2
	if l.capacityVisible {
		chrome += capacityRows
	}
	l.body = max(1, h-chrome)
	switch {
	case w < splitCols:
		l.single = true
	case w <= portraitMaxCols && h >= portraitMinRows:
		l.portrait = true
		l.top = max(6, l.body*35/100)
	default:
		l.left = sbxSplit
		if l.left <= 0 || l.left >= w-20 {
			l.left = max(1, (w-1)*30/100)
		}
		l.right = max(1, w-l.left-1)
	}
	return l
}

// mode names the tier for status lines and tests.
func (l layout) mode() string {
	switch {
	case l.tooSmall:
		return "too small"
	case l.single:
		return "single"
	case l.portrait:
		return "portrait"
	case l.short:
		return "short split"
	}
	return "split"
}

func tooSmallCard(w, h int) string {
	lines := []string{
		brand.Bold(true).Render("Terminal too small"),
		muted.Render(fmt.Sprintf("%d×%d · needs %d×%d", w, h, minCols, minRows)),
		hint("Ctrl+C", "quit"),
	}
	top := max(0, (h-len(lines))/2)
	out := make([]string, 0, h)
	for range top {
		out = append(out, strings.Repeat(" ", w))
	}
	for _, line := range lines {
		if len(out) == h {
			break
		}
		lead := max(0, (w-ansi.StringWidth(line))/2)
		out = append(out, pad(strings.Repeat(" ", lead)+line, w))
	}
	for len(out) < h {
		out = append(out, strings.Repeat(" ", w))
	}
	return strings.Join(out, "\n")
}

// keepTail fits s into n cells by dropping its head.
func keepTail(s string, n int) string {
	w := ansi.StringWidth(s)
	if w <= n {
		return s
	}
	if n <= 1 {
		return ansi.Truncate("…", max(0, n), "")
	}
	return ansi.TruncateLeft(s, w-n+1, "…")
}
