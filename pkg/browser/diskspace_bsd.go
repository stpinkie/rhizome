//go:build freebsd || dragonfly

package browser

import "golang.org/x/sys/unix"

// FreeBytes returns the free disk space (bytes) on the volume containing dir.
func FreeBytes(dir string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, err
	}
	if st.Bavail < 0 {
		return 0, nil
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}
