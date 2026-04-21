package admin

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestEmbeddedUI_ServesAllThreeFiles verifies the embed FS wiring created
// in plan 05-02 (uiFS in embed.go) combined with the UI asset files written
// by plan 05-03 (index.html, main.css, main.js) serves correctly through
// http.FileServer. The test lives in package admin (not admin_test) so it
// can call the unexported uiFS() directly.
func TestEmbeddedUI_ServesAllThreeFiles(t *testing.T) {
	handler := http.FileServer(http.FS(uiFS()))
	server := httptest.NewServer(handler)
	defer server.Close()

	cases := []struct {
		path           string
		wantStatus     int
		wantBodySubstr string
		wantCTPrefix   string
	}{
		{"/", 200, "Earl Scheib Concord", "text/html"},
		{"/index.html", 200, "Earl Scheib Concord", "text/html"},
		{"/main.css", 200, "--oxblood", "text/css"},
		{"/main.js", 200, "fetch('/api/queue'", ""}, // CT is js/js; don't pin
	}

	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			resp, err := http.Get(server.URL + tc.path)
			if err != nil {
				t.Fatalf("GET %s: %v", tc.path, err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("status: got %d, want %d", resp.StatusCode, tc.wantStatus)
			}
			body, _ := io.ReadAll(resp.Body)
			// min() is a Go 1.22+ builtin — no local shim needed.
			if !strings.Contains(string(body), tc.wantBodySubstr) {
				t.Errorf("body missing %q (got first 200 bytes: %q)", tc.wantBodySubstr, body[:min(200, len(body))])
			}
			if tc.wantCTPrefix != "" {
				ct := resp.Header.Get("Content-Type")
				if !strings.HasPrefix(ct, tc.wantCTPrefix) {
					t.Errorf("Content-Type: got %q, want prefix %q", ct, tc.wantCTPrefix)
				}
			}
		})
	}
}
