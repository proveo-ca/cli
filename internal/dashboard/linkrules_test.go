package dashboard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

const blacksmithRules = `# Task Details: links to leave out of PR descriptions.
links:
  block:
    - host: "*blacksmith.sh"
  allow:
    - host: "*blacksmith.sh"
      path: /track/run-proof
`

func TestLinkRulesBlockAHostGlobButAllowAPath(t *testing.T) {
	file := filepath.Join(t.TempDir(), "dashboard.yml")
	if err := os.WriteFile(file, []byte(blacksmithRules), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := readDashboardConfig(file)
	if err != nil {
		t.Fatal(err)
	}
	for link, want := range map[string]bool{
		"https://app.blacksmith.sh/RainTechnologiesInc/codesmith":        false,
		"https://backend.blacksmith.sh/track/proof-report?id=9":          false,
		"https://blacksmith.sh/":                                         false,
		"https://APP.Blacksmith.SH/x":                                    false,
		"https://dashboardbackend.blacksmith.sh/track/run-proof?run=abc": true,
		"https://blacksmith.sh/track/run-proof":                          true,
		"https://github.com/acme/app/pull/1":                             true,
		"https://notblacksmith.shop/x":                                   true,
		"https://claude.ai/code/artifact/abc":                            true,
	} {
		if got := c.Links.shows(link); got != want {
			t.Errorf("shows(%s) = %v, want %v", link, got, want)
		}
	}
	if (linkRules{}).shows("https://app.blacksmith.sh/x") != true {
		t.Error("no rules hid a link")
	}
}

func TestDashboardConfigParsesStrictly(t *testing.T) {
	dir := t.TempDir()
	write := func(body string) string {
		t.Helper()
		file := filepath.Join(dir, "dashboard.yml")
		if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return file
	}
	if c, err := readDashboardConfig(filepath.Join(dir, "missing.yml")); err != nil || len(c.Links.Block) != 0 {
		t.Errorf("a missing file = %+v, %v", c, err)
	}
	if c, err := readDashboardConfig(write("# nothing yet\n")); err != nil || len(c.Links.Block) != 0 {
		t.Errorf("a comment-only file = %+v, %v", c, err)
	}
	for body, why := range map[string]string{
		"links:\n  blok:\n    - host: x\n":      "field blok not found",
		"links:\n  block:\n    - path: /x\n":    "every rule needs a host",
		"links:\n  block:\n    - host: \"[\"\n": "syntax error in pattern",
	} {
		if _, err := readDashboardConfig(write(body)); err == nil || !strings.Contains(err.Error(), why) {
			t.Errorf("%q: err %v, want %q", body, err, why)
		}
	}
}

func TestTaskDetailsHideBlockedLinksAndPickUpEdits(t *testing.T) {
	m := detailsModel(160, 50)
	m.Home = t.TempDir()
	task := m.data.Tasks[1]
	meta := m.metadata[metadataKey(task)]
	meta.pr.Links = []string{
		"https://app.blacksmith.sh/RainTechnologiesInc/codesmith",
		"https://dashboardbackend.blacksmith.sh/track/run-proof?run=abc",
		"https://backend.blacksmith.sh/track/proof-report",
		"https://loom.example/demo",
	}
	m.metadata[metadataKey(task)] = meta
	view := func() string { return ansi.Strip(m.View().Content) }
	if v := view(); !strings.Contains(v, "app.blacksmith.sh") {
		t.Fatalf("without dashboard.yml every link shows:\n%s", v)
	}
	file := filepath.Join(m.Home, dashboardConfigFile)
	if err := os.WriteFile(file, []byte(blacksmithRules), 0o600); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Minute)
	if err := os.Chtimes(file, future, future); err != nil {
		t.Fatal(err)
	}
	v := view()
	if strings.Contains(v, "app.blacksmith.sh") || strings.Contains(v, "proof-report") ||
		!strings.Contains(v, "dashboardbackend.blacksmith.sh/track/run-proof") || !strings.Contains(v, "loom.example/demo") {
		t.Errorf("an edit to dashboard.yml did not apply:\n%s", v)
	}
	if err := os.WriteFile(file, []byte("links:\n  blok: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	later := future.Add(time.Minute)
	if err := os.Chtimes(file, later, later); err != nil {
		t.Fatal(err)
	}
	if v := view(); !strings.Contains(v, "app.blacksmith.sh") || !m.alert || !strings.Contains(m.status, "dashboard.yml") {
		t.Errorf("a broken dashboard.yml: status %q, alert %v", m.status, m.alert)
	}
}
