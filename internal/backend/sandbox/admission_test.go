// SPEC: _spec/internal/schedule/resource-queue.puml
package sandbox

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/proveo-ca/proveo/internal/sbx"
)

func TestDiskAdmissionSerializesAndReconcilesLeases(t *testing.T) {
	oldDisk, oldList, oldDir := diskSample, listSandboxes, receiptDir
	t.Cleanup(func() { diskSample, listSandboxes, receiptDir = oldDisk, oldList, oldDir })
	root := t.TempDir()
	receiptDir = func() string { return root }
	known := true
	listSandboxes = func() ([]sbx.Listed, bool) { return []sbx.Listed{{Name: "outside-test"}}, known }
	diskSample = func(string) (sbx.Capacity, error) {
		return sbx.Capacity{DiskFree: 2 << 30, ArtifactFree: 1 << 30, ReservationBytes: 1 << 30, Reservation: "1g"}, nil
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, name := range []string{"codex-first", "codex-second"} {
		wg.Go(func() { results <- reserveDisk(root, name, name) })
	}
	wg.Wait()
	close(results)
	granted := 0
	for err := range results {
		if err == nil {
			granted++
		} else if !errors.Is(err, ErrDiskWait) {
			t.Fatalf("reservation: %v", err)
		}
	}
	if granted != 1 {
		t.Fatalf("granted %d reservations for one available slot", granted)
	}
	path := filepath.Join(root, "admission", "leases.json")
	dead := []byte(`[{"Name":"codex-gone","Attempt":"old","Bytes":1073741824,"PID":0}]`)
	if err := os.WriteFile(path, dead, 0o600); err != nil {
		t.Fatal(err)
	}
	known = false
	if err := reserveDisk(root, "codex-next", "next"); !errors.Is(err, ErrDiskWait) {
		t.Fatalf("unknown inventory accepted a reservation: %v", err)
	}
	known = true
	if err := reserveDisk(root, "codex-next", "next"); err != nil {
		t.Fatalf("confirmed missing VM held a disk lease: %v", err)
	}
}
