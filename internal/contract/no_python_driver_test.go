// SPEC: _spec/tests/testing-strategy.puml
package contract_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestPythonIsAToolchainTargetOnly(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	const executable = "python(?:[0-9]+(?:\\.[0-9]+)*)?"
	expressions := []string{
		"(?mi)^[ \\t]*#![^\\n]*\\b" + executable + "\\b",
		"(?m)\\b" + executable + "\\s+(?:-[IBu]+\\s+)*(?:-c\\s|-(?:[^\\n]*)<<)",
		`"` + executable + `"\s*,\s*"(?:-[IBuc]+|-)"`,
	}
	patterns := make([]*regexp.Regexp, 0, len(expressions))
	for _, expr := range expressions {
		patterns = append(patterns, regexp.MustCompile(expr))
	}
	for _, dir := range []string{"cmd", "internal", "defs", "packages", "e2e"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if entry.Name() == "node_modules" || entry.Name() == ".venv" || entry.Name() == ".git" {
					return filepath.SkipDir
				}
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			if filepath.Ext(path) == ".py" {
				t.Errorf("%s is a Python source file. Place tooling fixtures in E2E test data archives.", rel)
			}
			if filepath.Ext(path) == ".txtar" || strings.HasPrefix(entry.Name(), ".") || entry.Type()&os.ModeSymlink != 0 ||
				path == filepath.Join(root, "internal", "contract", "no_python_driver_test.go") {
				return nil
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, pattern := range patterns {
				if match := pattern.Find(body); match != nil {
					t.Errorf("%s invokes Python as an implementation or fixture driver (%q)", rel, match)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
