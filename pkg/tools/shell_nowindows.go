//go:build !windows

package tools

// resolveLongPath is a no-op on non-Windows platforms.
func resolveLongPath(p string) string {
	return p
}
