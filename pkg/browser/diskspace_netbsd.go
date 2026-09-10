//go:build netbsd

package browser

import "golang.org/x/sys/unix"

// FreeBytes returns the free disk space (bytes) on the volume containing dir.
func FreeBytes(dir string) (uint64, error) {
	var st unix.Statvfs_t
	if err := unix.Statvfs(dir, &st); err != nil {
		return 0, err
	}
	return st.Bavail * st.Bsize, nil
}
