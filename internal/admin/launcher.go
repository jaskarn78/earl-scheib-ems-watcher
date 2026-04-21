package admin

// Open attempts to open url in the user's default browser.
// Failures are non-fatal — the caller prints the URL to stdout so Marco can
// navigate manually if the shell-out fails.
//
// Platform-specific implementations live in launcher_windows.go and launcher_other.go.
func Open(url string) error {
	return openBrowser(url)
}
