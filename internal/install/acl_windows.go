//go:build windows

package install

import (
	"fmt"
	"os/exec"
)

// SetDataDirACL sets ACLs on dir so SYSTEM has Full Control and Users have
// Modify access (matching the Inno Setup [Dirs] Permissions directive).
//
// Equivalent icacls command:
//
//	icacls "<dir>" /grant "SYSTEM:(OI)(CI)F" /grant "Users:(OI)(CI)M" /T /Q
//
// (OI) = object inherit, (CI) = container inherit, F = Full, M = Modify.
func SetDataDirACL(dir string) error {
	cmd := exec.Command(
		"icacls", dir,
		"/grant", "SYSTEM:(OI)(CI)F",
		"/grant", "Users:(OI)(CI)M",
		"/T",
		"/Q",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("icacls failed: %w\noutput: %s", err, string(out))
	}
	return nil
}
