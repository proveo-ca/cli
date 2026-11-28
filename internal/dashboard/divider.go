// SPEC: _spec/internal/dashboard/workspace-instances.puml
package dashboard

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

func SbxDivider(frame string, width int) int {
	rows := strings.Split(frame, "\n")
	type run struct{ length, row int }
	columns := make([]run, width)
	best, longest := 0, 0
	minimum := max(3, len(rows)/3)
	for row, line := range rows {
		line = ansi.Strip(line)
		for offset, char := range line {
			if !strings.ContainsRune("│┃║▏▎▕", char) {
				continue
			}
			col := ansi.StringWidth(line[:offset])
			if col < max(1, width/5) || col >= width/2 {
				continue
			}
			r := &columns[col]
			if r.row != row-1 {
				r.length = 0
			}
			r.row, r.length = row, r.length+1
			if r.length >= minimum && (r.length > longest || (r.length == longest && col > best)) {
				best, longest = col, r.length
			}
		}
	}
	return best
}
