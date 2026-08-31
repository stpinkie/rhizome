//go:build windows

package fileutil

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// replaceFile renames src to dst, replacing dst if it already exists.
// Windows os.Rename does not replace existing files, so we use MoveFileEx
// with MOVEFILE_REPLACE_EXISTING.
func replaceFile(src, dst string) error {
	from, err := windows.UTF16PtrFromString(src)
	if err != nil {
		return fmt.Errorf("convert src to UTF-16: %w", err)
	}
	to, err := windows.UTF16PtrFromString(dst)
	if err != nil {
		return fmt.Errorf("convert dst to UTF-16: %w", err)
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING)
}
