//go:build windows

package admin

import "os/exec"

// openBrowser on Windows uses rundll32 url.dll,FileProtocolHandler which
// does not flash a console window (unlike `cmd /c start`) and handles URLs
// with query strings / fragments correctly. Works on Windows 10+.
func openBrowser(url string) error {
	return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}
