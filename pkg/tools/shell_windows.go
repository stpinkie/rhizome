//go:build windows

package tools

import (
	"path/filepath"
	"syscall"
)

// resolveLongPath converts a Windows 8.3 short path (e.g. RUNNER~1)
// to its full long-path equivalent. On failure it returns the original path.
func resolveLongPath(p string) string {
	long, err := syscall.GetLongPathName(p)
	if err != nil {
		return p
	}
	return filepath.Clean(long)
}
