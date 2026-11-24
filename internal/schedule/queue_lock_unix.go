//go:build !windows

// SPEC: _spec/_experiments/dashboard/resource-queue.puml
package schedule

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func lockQueueFile(f *os.File) error {
	err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) {
		return ErrQueueBusy
	}
	return err
}
