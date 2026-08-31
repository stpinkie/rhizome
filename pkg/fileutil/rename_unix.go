//go:build !windows

package fileutil

import "os"

// replaceFile renames src to dst, replacing dst if it already exists.
// On POSIX systems, os.Rename atomically replaces the destination.
func replaceFile(src, dst string) error {
	return os.Rename(src, dst)
}
