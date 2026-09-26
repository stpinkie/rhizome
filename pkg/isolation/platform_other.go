//go:build !linux && !windows && !darwin

package isolation

import (
	"os/exec"
)

func applyPlatformIsolation(cmd *exec.Cmd, opts Options, root string) error {
	// Unsupported platforms currently keep the command unchanged. Callers rely on
	// Preflight and higher-level checks to surface unsupported isolation modes.
	return nil
}

func postStartPlatformIsolation(cmd *exec.Cmd, opts Options, root string) error {
	return nil
}

func cleanupPendingPlatformResources(cmd *exec.Cmd) {
}
