//go:build unix

package sbx

import "golang.org/x/sys/unix"

// diskFree is the space an unprivileged user may still write under path.
func diskFree(path string) (uint64, bool) {
	var st unix.Statfs_t
	if unix.Statfs(path, &st) != nil {
		return 0, false
	}
	return uint64(st.Bavail) * uint64(st.Bsize), true
}
