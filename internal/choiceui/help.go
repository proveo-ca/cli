// SPEC: _spec/internal/choiceui/confirm-dialogs.puml
package choiceui

// HelpSection is one group of keys in a help modal.
type HelpSection struct {
	Title string
	Keys  [][2]string // the key, then what it does
}

// helpCell is one column of a help row: a section heading, or a key and what it does.
type helpCell struct {
	heading    bool
	key, words string
}

// helpChrome is the card's rows around the help rows: frame, title, blank, rule, buttons, blank, key line.
const helpChrome = 8

// helpGap is the space between two help columns.
const helpGap = 3

// NewHelp is a confirmation-style card of keys with one Close button; its sections sit in two columns
// when one column is taller than the terminal and two fit its width.
func NewHelp(title string, sections []HelpSection, width, height int) *Confirmation {
	d := newDialog(title, []string{title}, 0, dialogButton{Label: "Close", Key: 'c'})
	d.w, d.h = width, height
	rows, keyW, colW := helpRows(sections, width, height)
	d.help, d.helpKeyW, d.helpColW = append([][]helpCell{{}}, rows...), keyW, colW
	return &Confirmation{card: d}
}

// helpRows lays the sections out in one column, or two balanced columns, cut to the rows the terminal has.
func helpRows(sections []HelpSection, width, height int) ([][]helpCell, int, int) {
	keyW, colW := 0, 0
	for _, s := range sections {
		for _, k := range s.Keys {
			keyW = max(keyW, textWidth(k[0]))
		}
	}
	for _, s := range sections {
		colW = max(colW, textWidth(s.Title)+modalBodyX-modalTitleX)
		for _, k := range s.Keys {
			colW = max(colW, keyW+2+textWidth(k[1]))
		}
	}
	column := func(part []HelpSection) []helpCell {
		var out []helpCell
		for i, s := range part {
			if i > 0 {
				out = append(out, helpCell{})
			}
			out = append(out, helpCell{heading: true, words: s.Title})
			for _, k := range s.Keys {
				out = append(out, helpCell{key: k[0], words: k[1]})
			}
		}
		return out
	}
	room := max(1, height-helpChrome)
	one := column(sections)
	twoFit := modalBodyX+2*colW+helpGap+modalRight+2 <= width
	if len(one) <= room || !twoFit || len(sections) < 2 {
		rows := make([][]helpCell, 0, len(one))
		for i, c := range one {
			if i == room-1 && len(one) > room {
				rows = append(rows, []helpCell{{words: "… a taller terminal shows the rest"}})
				break
			}
			rows = append(rows, []helpCell{c})
		}
		return rows, keyW, colW
	}
	split, best := 1, len(one)
	for k := 1; k < len(sections); k++ {
		if h := max(len(column(sections[:k])), len(column(sections[k:]))); h < best {
			split, best = k, h
		}
	}
	left, right := column(sections[:split]), column(sections[split:])
	rows := make([][]helpCell, 0, max(len(left), len(right)))
	for i := range max(len(left), len(right)) {
		var row []helpCell
		for _, col := range [][]helpCell{left, right} {
			cell := helpCell{}
			if i < len(col) {
				cell = col[i]
			}
			row = append(row, cell)
		}
		rows = append(rows, row)
	}
	if len(rows) > room {
		rows = append(rows[:room-1], []helpCell{{words: "… a taller terminal shows the rest"}})
	}
	return rows, keyW, colW
}

// helpWidth is the help rows' width from modalBodyX.
func (d dialog) helpWidth() int {
	cols := 0
	for _, row := range d.help {
		cols = max(cols, len(row))
	}
	return cols*d.helpColW + max(0, cols-1)*helpGap
}

// putHelp paints one help row: headings in the accent at the title column, keys bold, their words in ink.
func (d dialog) putHelp(c *canvas, p palette, row []helpCell) {
	for i, cell := range row {
		x := modalBodyX + i*(d.helpColW+helpGap)
		switch {
		case cell.heading:
			c.put(x-(modalBodyX-modalTitleX), p.accent, cell.words)
		case cell.key != "":
			c.put(x, p.key, cell.key)
			c.put(x+d.helpKeyW+2, p.body, cell.words)
		case cell.words != "":
			c.put(x, p.word, cell.words)
		}
	}
}
