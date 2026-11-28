// SPEC: _spec/_conventions/tui-design-language.puml
package contract_test

import (
	"go/ast"
	"go/token"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// colourAllowList names the drawers that still choose their own colours; each migration deletes its row.
var colourAllowList = map[string]string{
	"cmd/proveo/help.go":              "I4: help drops its raw italic SGR",
	"internal/choiceui/dialog_run.go": "I4: choiceui styles() reads ui.Theme tones",
	"internal/dashboard/highlight.go": "exception, not a migration: code takes a chroma syntax theme (operator, 2026-11-25)",
	"internal/dashboard/items.go":     "I3: the dashboard paints through ui.Styles",
	"internal/dashboard/view.go":      "I3: the dashboard stops painting #1B1B20 and its own palette",
}

var (
	sgrColour = regexp.MustCompile("\x1b\\[[0-9;]*m")
	hexColour = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
)

// colourChoices lists, per file outside internal/ui, every place that picks a colour itself.
func colourChoices(t *testing.T) map[string][]string {
	t.Helper()
	root := repoRoot(t)
	found := map[string][]string{}
	for _, dir := range []string{"internal", "cmd"} {
		walkGoFiles(t, root, dir, func(rel string, file *ast.File, fset *token.FileSet) {
			if strings.HasSuffix(rel, "_test.go") || strings.HasPrefix(rel, "internal/ui/") || strings.HasPrefix(rel, "internal/contract/") {
				return
			}
			report := func(pos token.Pos, what string) {
				found[rel] = append(found[rel], rel+":"+strconv.Itoa(fset.Position(pos).Line)+" "+what)
			}
			ast.Inspect(file, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.SelectorExpr:
					if pkg, ok := n.X.(*ast.Ident); ok {
						switch {
						case pkg.Name == "lipgloss" && n.Sel.Name == "Color":
							report(n.Pos(), "lipgloss.Color")
						case pkg.Name == "ui" && strings.HasPrefix(n.Sel.Name, "Identity"):
							report(n.Pos(), "ui."+n.Sel.Name+" (layer 0; paint a ui.Tone)")
						}
					}
				case *ast.BasicLit:
					if n.Kind != token.STRING {
						return true
					}
					s, err := strconv.Unquote(n.Value)
					if err != nil {
						return true
					}
					if sgrColour.MatchString(s) {
						report(n.Pos(), "raw SGR "+strconv.Quote(sgrColour.FindString(s)))
					}
					if hexColour.MatchString(s) {
						report(n.Pos(), "hex colour "+s)
					}
				}
				return true
			})
		})
	}
	return found
}

func TestNoColourIsChosenOutsideTheTheme(t *testing.T) {
	t.Parallel()
	found := colourChoices(t)
	for _, rel := range slices.Sorted(maps.Keys(found)) {
		if _, allowed := colourAllowList[rel]; !allowed {
			t.Errorf("%s chooses its own colour:\n    %s\n    paint through ui.Theme and ui.Styles; "+
				"see _spec/_conventions/tui-design-language.puml (ONE PRODUCT IDENTITY)", rel, strings.Join(found[rel], "\n    "))
		}
	}
}

func TestEveryColourAllowListRowStillChoosesAColour(t *testing.T) {
	t.Parallel()
	found := colourChoices(t)
	for rel, why := range colourAllowList {
		if len(found[rel]) == 0 {
			t.Errorf("%s no longer chooses a colour (%s): delete its allow-list row", rel, why)
		}
	}
}

func TestForbidigoMirrorsTheColourAllowList(t *testing.T) {
	t.Parallel()
	cfg := readRepoFile(t, ".golangci.yml")
	for _, want := range []string{"    - forbidigo\n", `pattern: ^lipgloss\.Color$`, `pattern: ^ui\.Identity`, "path: ^internal/ui/\n"} {
		if !strings.Contains(cfg, want) {
			t.Errorf(".golangci.yml lacks %q: the lint half of the colour ban is gone", want)
		}
	}
	_, rows, _ := strings.Cut(cfg, "# colour ban allow-list")
	excluded := map[string]bool{}
	for _, m := range regexp.MustCompile(`path: \^([^$\n]+)\$`).FindAllStringSubmatch(rows, -1) {
		excluded[strings.ReplaceAll(m[1], `\.`, ".")] = true
	}
	if !maps.Equal(excluded, maps.Collect(func(yield func(string, bool) bool) {
		for rel := range colourAllowList {
			if !yield(rel, true) {
				return
			}
		}
	})) {
		t.Errorf("forbidigo exclusions %v differ from colourAllowList %v", slices.Sorted(maps.Keys(excluded)), slices.Sorted(maps.Keys(colourAllowList)))
	}
}
