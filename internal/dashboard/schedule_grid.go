// SPEC: _spec/internal/dashboard/schedule-manager.puml
package dashboard

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/proveo-ca/proveo/internal/schedule"
	"github.com/proveo-ca/proveo/internal/ui"
)

// weekGrid draws a week of runs: a row per half hour that holds one, a column per day, each cell cut into equal slices.
type weekGrid struct {
	slots  []schedule.Slot
	week   time.Time // Monday 00:00
	now    time.Time
	sel    string // the selected job, drawn brand
	glyphs ui.GlyphTier
}

// gridCell is where a run shows in the grid, for a click to select its job.
type gridCell struct {
	row, x0, x1 int
	job         string
}

const gridLabel = 6 // "18:30 "

// gridRow is the clock time one grid row covers.
const gridRow = 30 * time.Minute

// cellWidth is the slices per day a row has room for, at most 6 (five minutes each).
func cellWidth(w int) int {
	room := (w-gridLabel)/7 - 1
	for _, cw := range []int{6, 5, 4, 3, 2} {
		if cw <= room {
			return cw
		}
	}
	return 0
}

// rowStarts are the minutes after midnight, on the grid's clock, of each row any run touches during the week.
func (g weekGrid) rowStarts() []int {
	per := int(gridRow / time.Minute)
	on := make([]bool, 24*60/per)
	end := g.week.AddDate(0, 0, 7)
	for _, s := range g.slots {
		from, to := s.Start, s.End
		if from.Before(g.week) {
			from = g.week
		}
		if to.After(end) {
			to = end
		}
		f := from.In(g.week.Location())
		for t := time.Date(f.Year(), f.Month(), f.Day(), f.Hour(), f.Minute()/per*per, 0, 0, f.Location()); t.Before(to); t = t.Add(gridRow) {
			c := t.In(g.week.Location())
			on[(c.Hour()*60+c.Minute())/per] = true
		}
	}
	var out []int
	for i, ok := range on {
		if ok {
			out = append(out, i*per)
		}
	}
	return out
}

func (g weekGrid) render(w int) ([]string, []gridCell) {
	cw := cellWidth(w)
	if cw == 0 || len(g.slots) == 0 {
		return nil, nil
	}
	loc := g.week.Location()
	slice := gridRow / time.Duration(cw)
	today := g.now.In(loc)
	nowMin := today.Hour()*60 + today.Minute()
	head := strings.Repeat(" ", gridLabel)
	for d := range 7 {
		day := g.week.AddDate(0, 0, d)
		label := day.Format("Mon 2")
		if len(label) > cw {
			label = day.Format("Mon")[:min(3, cw)]
		}
		label = centre(label, cw)
		if sameDay(day, today) {
			label = brand.Bold(true).Render(label)
		} else {
			label = muted.Render(label)
		}
		head += label + " "
	}
	rows := []string{head}
	var cells []gridCell
	for _, start := range g.rowStarts() {
		clock := fmt.Sprintf("%02d:%02d", start/60, start%60)
		label := muted.Render(clock) + " "
		if nowMin >= start && nowMin < start+int(gridRow/time.Minute) {
			label = brand.Render(clock) + " "
		}
		line := label
		x := gridLabel
		for d := range 7 {
			day := g.week.AddDate(0, 0, d)
			row := time.Date(day.Year(), day.Month(), day.Day(), 0, start, 0, 0, loc)
			for s := range cw {
				from := row.Add(time.Duration(s) * slice)
				glyphText, job := g.slice(from, from.Add(slice))
				line += glyphText
				if job != "" {
					cells = append(cells, gridCell{row: len(rows), x0: x + s, x1: x + s + 1, job: job})
				}
			}
			line += " "
			x += cw + 1
		}
		rows = append(rows, line)
	}
	rows = append(rows, g.legend(w))
	return rows, cells
}

// slice is the glyph for the runs inside [from, to), and the job a click there selects.
func (g weekGrid) slice(from, to time.Time) (string, string) {
	var live []schedule.Slot
	var paused []string
	for _, s := range g.slots {
		if !s.Start.Before(to) || !s.End.After(from) {
			continue
		}
		if s.Paused {
			paused = append(paused, s.Job)
		} else {
			live = append(live, s)
		}
	}
	clashes := false
	for i, a := range live {
		for _, b := range live[i+1:] {
			clashes = clashes || (a.Start.Before(b.End) && b.Start.Before(a.End))
		}
	}
	pick := ""
	for _, s := range live {
		if pick == "" || s.Job == g.sel {
			pick = s.Job
		}
	}
	block, clash, held := "█", "▒", "░"
	if g.glyphs == ui.GlyphsASCII || g.glyphs == ui.GlyphsOff {
		block, clash, held = "#", "x", "."
	}
	switch {
	case clashes:
		return warnText.Render(clash), pick
	case pick != "" && pick == g.sel:
		return brand.Render(block), pick
	case pick != "":
		return muted.Render(block), pick
	case len(paused) > 0:
		return ruleText.Render(held), paused[0]
	}
	return " ", ""
}

func (g weekGrid) legend(w int) string {
	block, clash, held := "█", "▒", "░"
	if g.glyphs == ui.GlyphsASCII || g.glyphs == ui.GlyphsOff {
		block, clash, held = "#", "x", "."
	}
	parts := []string{
		brand.Render(block) + muted.Render(" selected"),
		warnText.Render(clash) + muted.Render(" overlap"),
		ruleText.Render(held) + muted.Render(" paused"),
		muted.Render("times " + g.week.Format("MST")),
	}
	out := ""
	for _, p := range parts {
		if ansi.StringWidth(out)+ansi.StringWidth(p)+3 > w {
			break
		}
		if out != "" {
			out += muted.Render(" · ")
		}
		out += p
	}
	return out
}

func centre(s string, w int) string {
	gap := w - len(s)
	if gap <= 0 {
		return s
	}
	return strings.Repeat(" ", gap/2) + s + strings.Repeat(" ", gap-gap/2)
}

func sameDay(a, b time.Time) bool {
	return a.Year() == b.Year() && a.YearDay() == b.YearDay()
}
