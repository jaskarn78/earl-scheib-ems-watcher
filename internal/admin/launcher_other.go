//go:build !windows

package admin

import (
	"os/exec"
	"runtime"
)

// openBrowser on non-Windows is a dev-convenience only — Marco runs Windows.
// On Linux we use xdg-open; on macOS we use `open`; anywhere else is a no-op.
func openBrowser(url string) error {
	switch runtime.GOOS {
	case "linux":
		return exec.Command("xdg-open", url).Start()
	case "darwin":
		return exec.Command("open", url).Start()
	default:
		return nil
	}
}
