// SPEC: _spec/_paradigms/dashboard-terminal-design.puml
package dashboard

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/proveo-ca/proveo/internal/sbx"
	"github.com/proveo-ca/proveo/internal/ui"
)

func TestPOnARunningCardListsItsSbxPortsAndWhatAnswers(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	open := ln.Addr().(*net.TCPAddr).Port
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "package.json"), []byte(`{"scripts":{"dev":"next dev -p 8083"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	m := sampleModel(140, 40, ui.GlyphsSafe)
	m.focus, m.selected = 0, 2
	task := &m.data.Tasks[2]
	task.Guest, task.Sandbox, task.Repo = true, "proveo-claudecode-36915573", repo
	task.Ports = []sbx.ListedPort{{HostIP: "127.0.0.1", HostPort: open, SandboxPort: 8083, Protocol: "tcp4"}, {HostIP: "127.0.0.1", HostPort: 1, SandboxPort: 9229}}
	if actions := ansi.Strip(m.cardActions(*task, true, 60)); !strings.Contains(actions, "p ports") {
		t.Errorf("a running card lacks p ports: %q", actions)
	}
	if foot := ansi.Strip(m.footer(160)); !strings.Contains(foot, "p ports") {
		t.Errorf("footer = %q", foot)
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'p', Text: "p"})
	if cmd == nil || m.confirm == nil {
		t.Fatal("p opened nothing")
	}
	first := ansi.Strip(m.View().Content)
	if !strings.Contains(first, "Ports") || !strings.Contains(first, "checking…") {
		t.Errorf("the notice should open at once, probes pending:\n%s", first)
	}
	m.Update(cmd())
	view := ansi.Strip(m.View().Content)
	for _, want := range []string{"http://127.0.0.1:", "→  8083/tcp  · listening", "next · package.json", "→  9229/tcp  · nothing listening",
		"Sandbox  proveo-claudecode-36915573", "OK"} {
		if !strings.Contains(view, want) {
			t.Errorf("the ports notice lacks %q:\n%s", want, view)
		}
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.confirm != nil || m.portsFor != "" {
		t.Error("esc did not close the ports notice")
	}
	task.Ports = nil
	m.Update(tea.KeyPressMsg{Code: 'p', Text: "p"})
	if !strings.Contains(ansi.Strip(m.View().Content), "sbx publishes no port for this sandbox") {
		t.Error("a sandbox without ports does not say so")
	}
	m.confirm = nil
	task.Guest = false
	if _, cmd := m.Update(tea.KeyPressMsg{Code: 'p', Text: "p"}); cmd != nil || m.confirm != nil {
		t.Error("p opened ports for a task without a running sandbox")
	}
}

func TestCardActionsMakeRoomByDroppingFilesFirst(t *testing.T) {
	m := sampleModel(120, 40, ui.GlyphsSafe)
	task := Task{ID: "x", Guest: true, Sandbox: "sb", Live: true}
	if got := ansi.Strip(m.cardActions(task, true, 60)); got != "↵ attach   t details   f files   p ports" {
		t.Errorf("wide = %q", got)
	}
	if got := ansi.Strip(m.cardActions(task, true, 34)); got != "↵ attach  t details  p ports" || ansi.StringWidth(got) > 34 {
		t.Errorf("narrow = %q", got)
	}
}
