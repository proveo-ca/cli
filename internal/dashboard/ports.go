// SPEC: _spec/_paradigms/dashboard-terminal-design.puml
package dashboard

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/proveo-ca/proveo/internal/choiceui"
	"github.com/proveo-ca/proveo/internal/devports"
	"github.com/proveo-ca/proveo/internal/sbx"
)

// portsEvent is what the probe learned about a task's ports: what serves each sandbox port, and which answer.
type portsEvent struct {
	id     string
	labels map[int]string
	live   map[int]bool
}

// openPorts shows the running task's published ports at once, then probes them.
func (m *dashModel) openPorts(t Task) tea.Cmd {
	if !t.Guest || t.Sandbox == "" {
		return nil
	}
	m.portsFor, m.confirm = t.ID, m.portsNotice(t, nil, nil)
	root := t.Repo
	if root == "" {
		root = m.CWD
	}
	ports, id := t.Ports, t.ID
	return func() tea.Msg {
		labels, live := map[int]string{}, map[int]bool{}
		for _, c := range devports.Discover(root) {
			if _, seen := labels[c.Port]; !seen {
				labels[c.Port] = c.Tool + " · " + c.Source
			}
		}
		for _, p := range ports {
			live[p.HostPort] = answers(p)
		}
		return portsEvent{id: id, labels: labels, live: live}
	}
}

// answers reports whether something accepts a connection on the host side of p.
func answers(p sbx.ListedPort) bool {
	host := p.HostIP
	if host == "" || host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	c, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(p.HostPort)), 300*time.Millisecond)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// portsNotice lists the sandbox's published ports: host URL, sandbox port, what serves it and whether it answers.
func (m *dashModel) portsNotice(t Task, labels map[int]string, live map[int]bool) *choiceui.Confirmation {
	var lines []string
	if len(t.Ports) == 0 {
		lines = append(lines, "sbx publishes no port for this sandbox.",
			"`sbx port publish "+safeText(t.Sandbox)+" <host>:<port>` publishes one.")
	}
	for _, p := range t.Ports {
		host := p.HostIP
		if host == "" || host == "0.0.0.0" {
			host = "127.0.0.1"
		}
		proto := strings.TrimRight(p.Protocol, "46")
		if proto == "" {
			proto = "tcp"
		}
		state := "checking…"
		if live != nil {
			state = "nothing listening"
			if live[p.HostPort] {
				state = "listening"
			}
		}
		line := fmt.Sprintf("http://%s:%d  →  %d/%s  · %s", host, p.HostPort, p.SandboxPort, proto, state)
		if label := labels[p.SandboxPort]; label != "" {
			line += "  · " + safeText(label)
		}
		lines = append(lines, line)
	}
	lines = append(lines, "", "Sandbox  "+safeText(t.Sandbox))
	return choiceui.NewNotice("Ports", lines, m.width, m.height)
}
