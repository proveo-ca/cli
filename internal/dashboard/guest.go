// SPEC: _spec/_experiments/dashboard/workspace-instances.puml
package dashboard

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/proveo-ca/proveo/internal/sbx"
)

type guestSample struct {
	id, sandbox, vmid, cpu, ram, disk string
	at                                time.Time
	fresh, available                  bool
}

const usageProbe = `
awk 'BEGIN{n=0} /^[0-9]/ {n+=$1} END {printf "cpu=%d%%\n",n}' <<EOF
$(ps -e -o pcpu= | awk '{print $1}')
EOF
awk '/^MemTotal:/{t=$2} /^MemAvailable:/{a=$2} END{if(t)printf "ram=%.0f\n",(t-a)*1024}' /proc/meminfo
(df -Pk /var/lib/docker 2>/dev/null || df -Pk /) | awk 'NR == 2 {printf "disk=%.0f\n",$3*1024}'
if command -v fresh >/dev/null 2>&1; then printf 'fresh=yes\n'; fi
`

func (m *dashModel) readGuest(t Task) tea.Cmd {
	if m.guestPending || !t.Guest || t.Workdir == "" || m.sbxTab {
		return nil
	}
	m.guestPending = true
	return func() tea.Msg {
		s := guestSample{id: t.ID, sandbox: t.Sandbox, vmid: t.VMID, at: time.Now()}
		if !verifiedGuest(t) {
			return s
		}
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "sbx", "exec", "-u", "agent", "-w", t.Workdir, t.Sandbox, "--", "sh", "-c", usageProbe)
		cmd.WaitDelay = time.Second
		raw, err := cmd.Output()
		if err != nil {
			return s
		}
		s.available = true
		for line := range strings.SplitSeq(string(raw), "\n") {
			key, value, _ := strings.Cut(line, "=")
			switch key {
			case "cpu":
				s.cpu = value
			case "ram":
				n, _ := strconv.ParseUint(value, 10, 64)
				s.ram = formatGiB(n)
			case "disk":
				n, _ := strconv.ParseUint(value, 10, 64)
				s.disk = formatGiB(n)
			case "fresh":
				s.fresh = value == "yes"
			}
		}
		return s
	}
}

func verifiedGuest(t Task) bool {
	host, err := os.Hostname()
	if err != nil || host != t.Host || t.VMID == "" {
		return false
	}
	rows, ok := sbx.List()
	if !ok {
		return false
	}
	for _, row := range rows {
		if row.ID == t.VMID && row.Name == t.Sandbox && row.Running() {
			return true
		}
	}
	return false
}
