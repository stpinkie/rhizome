//go:build windows

package tools

import (
	"path/filepath"
	"syscall"
)

// resolveLongPath converts a Windows 8.3 short path (e.g. RUNNER~1)
// to its full long-path equivalent. On failure it returns the original path.
func resolveLongPath(p string) string {
	u16, err := syscall.UTF16PtrFromString(p)
	if err != nil {
		return p
	}
	// First call to get required buffer size.
	n, err := syscall.GetLongPathName(u16, nil, 0)
	if err != nil || n == 0 {
		return p
	}
	buf := make([]uint16, n)
	n, err = syscall.GetLongPathName(u16, &buf[0], n)
	if err != nil || n == 0 {
		return p
	}
	return filepath.Clean(syscall.UTF16ToString(buf[:n]))
}
