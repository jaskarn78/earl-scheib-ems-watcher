//go:build !windows

package install

import "errors"

// SetDataDirACL is a no-op stub on non-Windows platforms.
// On Windows, this function calls icacls to grant SYSTEM=Full and Users=Modify.
func SetDataDirACL(dir string) error {
	return errors.New("SetDataDirACL is only supported on Windows; run this binary on a Windows host to install")
}
