//go:build unix

package sbx

import "golang.org/x/sys/unix"

// diskFree is the space an unprivileged user may still write under path.
func diskFree(path string) (uint64, bool) {
	_, free, ok := diskSpace(path)
	return free, ok
}

func diskSpace(path string) (uint64, uint64, bool) {
	var st unix.Statfs_t
	if unix.Statfs(path, &st) != nil {
		return 0, 0, false
	}
	return uint64(st.Blocks) * uint64(st.Bsize), uint64(st.Bavail) * uint64(st.Bsize), true
}
