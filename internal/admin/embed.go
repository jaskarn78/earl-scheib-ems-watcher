// Package admin implements `earlscheib.exe --admin`: a local HTTP server on
// 127.0.0.1:0 that serves an embedded single-page queue-inspection UI and
// proxies /api/queue (GET) and /api/cancel (POST) to the remote webhook
// server, HMAC-signing every outbound request with the binary-baked secret.
//
// The browser never sees the secret. Closing the browser tab or pressing
// Ctrl+C terminates the Go process cleanly via http.Server.Shutdown.
package admin

import (
	"embed"
	"io/fs"
)

//go:embed ui
var uiRoot embed.FS

// uiFS returns the embedded ui/ subtree rooted at "ui" so http.FileServer
// serves index.html at / rather than /ui/index.html.
func uiFS() fs.FS {
	sub, err := fs.Sub(uiRoot, "ui")
	if err != nil {
		// Sub only errors on a bogus path; this is a compile-time constant.
		panic("admin: embed fs.Sub(\"ui\") failed: " + err.Error())
	}
	return sub
}
