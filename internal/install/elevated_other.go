//go:build !windows

package install

import "os"

// isElevated returns true if the process is running as root on non-Windows systems.
// On Linux/macOS, root UID=0 is the equivalent of "administrator".
func isElevated() bool {
	return os.Getuid() == 0
}
