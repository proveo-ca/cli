// SPEC: _spec/_experiments/dashboard/workspace-instances.puml
package dashboard

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/proveo-ca/proveo/internal/activity"
	"github.com/proveo-ca/proveo/internal/sbx"
)

type guestSample struct {
	id, sandbox, vmid string
	busy              float64 // cores' worth of CPU in use
	cores             int
	ram, ramTotal     uint64
	disk              uint64
	uptime            time.Duration
	at                time.Time
	fresh, available  bool
	work              []guestWork    // where the agent works, its own checkout first
	activity          activity.State // what the agent is doing, from its own state or proveo's hook markers
	activitySince     time.Time
	seedRefused       string // why the seed refused to release the agent; it never started in this sandbox
}

const usageProbe = `ps -e -o pcpu= | awk '{n+=$1} END {printf "cpu=%.1f\n",n}'
printf 'cores=%s\n' "$(nproc 2>/dev/null || getconf _NPROCESSORS_ONLN)"
awk '/^MemTotal:/{t=$2} /^MemAvailable:/{a=$2} END{if(t)printf "ram=%.0f\nramtotal=%.0f\n",(t-a)*1024,t*1024}' /proc/meminfo
(df -Pk /var/lib/docker 2>/dev/null || df -Pk /) | awk 'NR == 2 {printf "disk=%.0f\n",$3*1024}'
awk '{printf "uptime=%d\n",$1}' /proc/uptime
if command -v fresh >/dev/null 2>&1; then printf 'fresh=yes\n'; fi`

func (m *dashModel) readGuest(t Task) tea.Cmd {
	if m.guestPending || !t.Guest || t.Sandbox == "" || m.sbxTab {
		return nil
	}
	m.guestPending = true
	return func() tea.Msg {
		s := guestSample{id: t.ID, sandbox: t.Sandbox, vmid: t.VMID, at: time.Now()}
		if !verifiedGuest(t) {
			return s
		}
		raw, err := guestScript(t, usageProbe+"\n"+guestActivityScript+"\n"+guestWorkScript, t.Workdir)
		if err != nil {
			return s
		}
		parseUsage(&s, string(raw))
		s.work = parseGuestWork(string(raw))
		s.activity, s.activitySince = activity.Resolve(activity.Parse(string(raw)))
		if s.seedRefused = parseSeedRefused(string(raw)); s.seedRefused != "" && s.activity == activity.Unknown {
			s.activity = activity.Exited
		}
		return s
	}
}

func parseUsage(s *guestSample, raw string) {
	for line := range strings.SplitSeq(raw, "\n") {
		key, value, _ := strings.Cut(strings.TrimSpace(line), "=")
		n, _ := strconv.ParseFloat(value, 64)
		switch key {
		case "cpu":
			s.busy, s.available = n/100, true
		case "cores":
			s.cores = int(n)
		case "ram":
			s.ram = uint64(n)
		case "ramtotal":
			s.ramTotal = uint64(n)
		case "disk":
			s.disk = uint64(n)
		case "uptime":
			s.uptime = time.Duration(n) * time.Second
		case "fresh":
			s.fresh = value == "yes"
		}
	}
}

// usageLine is the sample in one row: CPU cores busy of the total, RAM used of the total, disk, uptime.
func (s guestSample) usageLine() string {
	if !s.available {
		return ""
	}
	parts := []string{fmt.Sprintf("CPU %.1f/%d cores", s.busy, s.cores)}
	if s.ramTotal > 0 {
		parts = append(parts, "RAM "+usedOf(s.ram, s.ramTotal))
	}
	if s.disk > 0 {
		parts = append(parts, "Disk "+formatGiB(s.disk))
	}
	if s.uptime > 0 {
		parts = append(parts, "up "+compactAge(s.uptime))
	}
	return strings.Join(parts, " · ")
}

func compactAge(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
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

// parseSeedRefused is the reason the guest's seed printed when it refused to release the agent.
func parseSeedRefused(raw string) string {
	for line := range strings.SplitSeq(raw, "\n") {
		if why, ok := strings.CutPrefix(line, "seed\trefused\t"); ok {
			return safeText(why)
		}
	}
	return ""
}
