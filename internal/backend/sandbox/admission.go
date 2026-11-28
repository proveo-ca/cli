// SPEC: _spec/internal/schedule/resource-queue.puml
package sandbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"syscall"

	"github.com/proveo-ca/proveo/internal/sbx"
)

const DiskWaitExit = 75

var ErrDiskWait = errors.New("waiting for sandbox disk")

type diskLease struct {
	Name, Attempt string
	Bytes         uint64
	PID           int
}

var diskSample = sbx.DiskCapacity

func CheckAdmission(artifacts string) error {
	return admitDisk(artifacts, diskLease{})
}

func CanReuseInstance(state, target, work, id string) bool {
	abs, err := filepath.Abs(work)
	if err != nil {
		return false
	}
	work = canonicalInstancePath(abs)
	entries, err := instanceEntries(state)
	if err != nil {
		return false
	}
	var candidate *Instance
	for _, entry := range entries {
		if entry.Target != target || entry.Workspace != work || (id != "" && entry.ID != id) {
			continue
		}
		if candidate != nil {
			return false
		}
		candidate = entry
	}
	if candidate == nil || candidate.VMID == "" {
		return false
	}
	row, err := inventoryInstance(candidate.Name)
	return err == nil && row != nil && candidate.verify(row) == nil && idleInstance(row) == nil
}

func reserveDisk(artifacts, sandbox, attempt string) error {
	return admitDisk(artifacts, diskLease{Name: sandbox, Attempt: attempt, PID: os.Getpid()})
}

func admitDisk(artifacts string, want diskLease) error {
	if receiptDir() == "" {
		return fmt.Errorf("%w: disk ledger is unavailable", ErrDiskWait)
	}
	dir := filepath.Join(receiptDir(), "admission")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("%w: %v", ErrDiskWait, err)
	}
	gate, err := directGate(filepath.Join(dir, "ledger.lock"))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDiskWait, err)
	}
	defer gate.Close()
	rows, ok := listSandboxes()
	if !ok {
		return fmt.Errorf("%w: sbx inventory unavailable", ErrDiskWait)
	}
	path := filepath.Join(dir, "leases.json")
	var held []diskLease
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, &held); err != nil {
			return fmt.Errorf("%w: unreadable disk ledger: %v", ErrDiskWait, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: %v", ErrDiskWait, err)
	}
	live := make(map[string]bool, len(rows))
	for _, row := range rows {
		live[row.Name] = true
	}
	remaining := slices.DeleteFunc(held, func(r diskLease) bool {
		if r.Name == want.Name && !live[r.Name] {
			return !sbx.Exists(r.Name)
		}
		if live[r.Name] || leaseProcessAlive(r.PID) {
			return false
		}
		return len(rows) != 0 || !sbx.Exists(r.Name)
	})
	c, err := diskSample(artifacts)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDiskWait, err)
	}
	var reserved uint64
	for _, r := range remaining {
		if r.Name == want.Name {
			return fmt.Errorf("%w: %s already holds a disk reservation", ErrDiskWait, r.Name)
		}
		if r.Bytes > math.MaxUint64-reserved {
			return fmt.Errorf("%w: invalid disk reservation total", ErrDiskWait)
		}
		reserved += r.Bytes
	}
	limit := min(c.DiskFree, c.ArtifactFree)
	if c.DiskPolicy > 0 {
		limit = min(limit, c.DiskPolicy)
	}
	if reserved > limit || c.ReservationBytes > limit-reserved {
		return fmt.Errorf("%w: %s per sandbox, %s free, %s already reserved", ErrDiskWait, c.Reservation, sbx.GiB(limit), sbx.GiB(reserved))
	}
	if want.Name != "" {
		want.Bytes = c.ReservationBytes
		remaining = append(remaining, want)
	}
	b, err := json.Marshal(remaining)
	if err != nil {
		return err
	}
	return writePrivateRecord(path, b)
}

func leaseProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	defer func() { _ = p.Release() }()
	err = p.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, os.ErrPermission)
}
