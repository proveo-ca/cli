// SPEC: _spec/internal/schedule/resource-queue.puml
package sbx

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const DiskVolumeSetting = "sandbox.disk.dockerVolume"

type Capacity struct {
	Cores, BusyCores                             int
	CPUUsage, RAMUsage                           bool
	RAM, RAMFree, VRAM                           uint64
	Disk, DiskFree, DiskPolicy, ReservationBytes uint64
	ArtifactFree                                 uint64
	Reservation, Storage                         string
}

func DiskReservation() (string, uint64, error) {
	out, err := boundedWith(4*time.Second, Binary, "settings", "get", "--json", DiskVolumeSetting)
	if err != nil {
		return "", 0, fmt.Errorf("read %s: %w", DiskVolumeSetting, err)
	}
	var setting struct{ Value json.RawMessage }
	if err := json.Unmarshal(out, &setting); err != nil {
		return "", 0, err
	}
	var shown string
	if json.Unmarshal(setting.Value, &shown) != nil {
		shown = string(setting.Value)
	}
	value, err := parseDiskSize(shown)
	if err != nil || value == 0 {
		return "", 0, fmt.Errorf("unreadable %s value %q", DiskVolumeSetting, shown)
	}
	return shown, value, nil
}

func parseDiskSize(s string) (uint64, error) {
	v := strings.TrimSuffix(strings.TrimSuffix(strings.ToLower(strings.TrimSpace(s)), "ib"), "b")
	if v == "" {
		return 0, errors.New("empty size")
	}
	scale := uint64(1)
	switch v[len(v)-1] {
	case 'k':
		scale = 1 << 10
	case 'm':
		scale = 1 << 20
	case 'g':
		scale = 1 << 30
	case 't':
		scale = 1 << 40
	}
	if scale > 1 {
		v = strings.TrimSpace(v[:len(v)-1])
	}
	n, err := strconv.ParseUint(v, 10, 64)
	if err != nil || n > math.MaxUint64/scale {
		return 0, fmt.Errorf("invalid disk size %q", s)
	}
	return n * scale, nil
}

func storagePath() (string, error) {
	if path := os.Getenv("PROVEO_SBX_STORAGE"); path != "" {
		if !filepath.IsAbs(path) {
			return "", errors.New("PROVEO_SBX_STORAGE must name an absolute sbx storage directory")
		}
		return path, nil
	}
	out, err := boundedWith(4*time.Second, Binary, "df", "--json")
	if err == nil {
		var data struct{ DataRoot, StoragePath string }
		if json.Unmarshal(out, &data) == nil {
			for _, path := range []string{data.DataRoot, data.StoragePath} {
				if filepath.IsAbs(path) {
					return path, nil
				}
			}
		}
	}
	out, err = boundedWith(4*time.Second, Binary, "daemon", "status", "--json")
	if err == nil {
		var daemon struct{ Status, Socket string }
		if json.Unmarshal(out, &daemon) == nil && daemon.Status == "running" &&
			filepath.IsAbs(daemon.Socket) && filepath.Base(daemon.Socket) == "sandboxd.sock" {
			root := filepath.Dir(daemon.Socket)
			if fi, err := os.Stat(filepath.Join(root, "runtimes")); err == nil && fi.IsDir() {
				return filepath.Join(root, "runtimes"), nil
			}
		}
	}
	return "", errors.New("sbx storage path unavailable; set PROVEO_SBX_STORAGE to the daemon's storage directory")
}

func DiskCapacity(artifacts string) (Capacity, error) {
	var c Capacity
	shown, bytes, err := DiskReservation()
	if err != nil {
		return c, err
	}
	c.Reservation, c.ReservationBytes = shown, bytes
	path, err := storagePath()
	if err != nil {
		return c, err
	}
	total, free, ok := diskSpace(path)
	if !ok || total == 0 {
		return c, fmt.Errorf("cannot read disk capacity at %s", path)
	}
	c.Disk, c.DiskFree, c.Storage = total, free, path
	c.ArtifactFree = free
	if artifacts != "" {
		artifactFree, ok := diskFree(artifacts)
		if !ok {
			return c, fmt.Errorf("cannot read retained-state disk capacity at %s", artifacts)
		}
		c.ArtifactFree = artifactFree
	}
	if s := os.Getenv("PROVEO_SBX_DISK_POOL"); s != "" {
		c.DiskPolicy, err = parseDiskSize(s)
		if err != nil {
			return c, err
		}
	}
	return c, nil
}

func HostTelemetry(c Capacity) Capacity {
	c.Cores = runtime.NumCPU()
	switch runtime.GOOS {
	case "linux":
		c.RAM, _ = readMemTotal()
		c.RAMFree, c.RAMUsage = readMemAvailable()
		if total, ok := readNvidiaMemory("total"); ok && !nvidiaIntegrated() {
			c.VRAM = total
		} else if total, _, ok := amdVRAM(drmRoot); ok {
			c.VRAM = total
		}
	case "darwin":
		if out, err := boundedWith(2*time.Second, "sysctl", "-n", "hw.memsize"); err == nil {
			c.RAM, _ = strconv.ParseUint(strings.TrimSpace(string(out)), 10, 64)
		}
		if out, err := boundedWith(2*time.Second, "vm_stat"); err == nil {
			c.RAMFree, c.RAMUsage = parseVMStat(string(out))
		}
	}
	if out, err := boundedWith(2*time.Second, "ps", "-A", "-o", "pcpu="); err == nil {
		var busy float64
		for _, value := range strings.Fields(string(out)) {
			if n, err := strconv.ParseFloat(value, 64); err == nil && !math.IsNaN(n) && n >= 0 {
				busy += n
				c.CPUUsage = true
			}
		}
		c.BusyCores = int(math.Round(min(float64(c.Cores), busy/100)))
	}
	return c
}
