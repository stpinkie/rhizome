//go:build !windows

package browser

import "golang.org/x/sys/unix"

// FreeBytes returns the free disk space (bytes) on the volume containing dir.
func FreeBytes(dir string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return st.Bavail * uint64(st.Bsize), nil
}
