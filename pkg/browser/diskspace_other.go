//go:build !windows && !linux && !darwin && !freebsd && !dragonfly && !openbsd && !netbsd

package browser

import (
	"fmt"
	"runtime"
)

// FreeBytes is not implemented for the current OS/GOARCH combination.
func FreeBytes(dir string) (uint64, error) {
	return 0, fmt.Errorf("FreeBytes not supported on %s/%s", runtime.GOOS, runtime.GOARCH)
}
