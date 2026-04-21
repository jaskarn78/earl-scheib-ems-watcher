//go:build windows

package install

import (
	"golang.org/x/sys/windows"
)

// isElevated returns true if the current process is running with administrator
// privileges (elevated token). Uses the Windows API via golang.org/x/sys/windows.
func isElevated() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}
