//go:build windows

package browser

import (
	"path/filepath"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// FreeBytes returns the free disk space (bytes) on the volume containing dir.
//
//nolint:gosec // G103: unsafe.Pointer is required for the GetDiskFreeSpaceExW syscall.
func FreeBytes(dir string) (uint64, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return 0, err
	}
	vol := filepath.VolumeName(abs) + `\`
	volPtr, err := windows.UTF16PtrFromString(vol)
	if err != nil {
		return 0, err
	}
	var freeAvail, total, totalFree uint64
	r1, _, callErr := syscall.NewLazyDLL("kernel32.dll").
		NewProc("GetDiskFreeSpaceExW").
		Call(
			uintptr(unsafe.Pointer(volPtr)),
			uintptr(unsafe.Pointer(&freeAvail)),
			uintptr(unsafe.Pointer(&total)),
			uintptr(unsafe.Pointer(&totalFree)),
		)
	if r1 == 0 {
		return 0, callErr
	}
	return freeAvail, nil
}
