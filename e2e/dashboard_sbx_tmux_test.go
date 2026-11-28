//go:build e2e && !windows

// SPEC: _spec/_experiments/dashboard/workspace-instances.puml
package e2e

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/proveo-ca/proveo/internal/sbx"
	"github.com/proveo-ca/proveo/internal/schedule"
)

func TestDashboardMatchesStockSbxOnTmux(t *testing.T) {
	failSkipsWhenOptedIn(t, dashboardLiveGate)
	requireTmux(t)
	requireSbx(t)
	version, err := sbx.Version()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("comparing sbx %s", version)
	bin, home, work := buildProveo(t), t.TempDir(), t.TempDir()
	tmux := dashboardTmux(t)
	stock, embedded := schedule.NewTmux("stock", tmux), schedule.NewTmux("embedded", tmux)
	if err := stock.Start(120, 39, "env", "TERM=xterm-256color", "COLORTERM=truecolor", "NO_COLOR=0", "sbx"); err != nil {
		t.Fatal(err)
	}
	if err := embedded.Start(120, 40, "env", "TERM=xterm-256color", "COLORTERM=truecolor", "NO_COLOR=0", "PROVEO_HOME="+home,
		"PROVEO_STATE_ROOT="+filepath.Join(home, "state"), "sh", "-c", `cd "$1" && exec "$2"`, "sh", work, bin); err != nil {
		t.Fatal(err)
	}
	waitDashboardPane(t, embedded, "Task Dashboard")
	if err := embedded.SendKeys("C-g", "Tab"); err != nil {
		t.Fatal(err)
	}
	waitDashboardPane(t, stock, "Docker Sandboxes")
	waitDashboardPane(t, embedded, "Docker Sandboxes")
	var direct, got string
	compare := func() bool {
		direct, _ = stock.Capture()
		got, _ = embedded.Capture()
		_, child, _ := strings.Cut(got, "\n")
		return normalizedSbxFrame(direct) == normalizedSbxFrame(child)
	}
	if !waitUntil(10*time.Second, compare) {
		t.Fatalf("stock viewport:\n%s\nembedded viewport:\n%s", direct, got)
	}
	if err := embedded.SendKeys("C-g", "Tab"); err != nil {
		t.Fatal(err)
	}
	waitDashboardPane(t, embedded, "Task Dashboard")
	if err := embedded.SendKeys("C-g", "Tab"); err != nil {
		t.Fatal(err)
	}
	waitDashboardPane(t, embedded, "Docker Sandboxes")
	if err := embedded.SendKeys("C-c"); err != nil {
		t.Fatal(err)
	}
	if !waitUntil(5*time.Second, func() bool { return !embedded.Alive() }) {
		t.Fatal("dashboard stayed alive after Ctrl+C")
	}
	if !stock.Alive() {
		t.Fatal("exiting embedded sbx also stopped the stock client")
	}
}

var volatileSbxUptime = regexp.MustCompile(`( · )[0-9]+(?:[smhd][0-9]+)*[smhd](\s*)$`)

func normalizedSbxFrame(frame string) string {
	var rows []string
	for row := range strings.SplitSeq(frame, "\n") {
		rows = append(rows, strings.TrimRight(volatileSbxUptime.ReplaceAllString(row, "${1}<uptime>${2}"), " "))
	}
	return strings.TrimRight(strings.Join(rows, "\n"), "\n")
}
