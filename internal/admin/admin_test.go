// Test startup strategy: admin.Config.URLCh (test-only hook added in task 2)
// lets tests read the bound URL immediately after net.Listen succeeds, with
// no reliance on stdout capture or port scanning. All tests in this file use
// startAdminWithURLCh as the single entrypoint for spinning up admin.Run.
//
// Background for future maintainers: an earlier draft of this file tried a
// brute-force port-scan approach (waitForAdmin) and a stdout-pipe approach.
// Both were brittle. The URLCh channel is the canonical, deterministic
// mechanism — do not reintroduce the scan/pipe approaches.
package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jjagpal/earl-scheib-watcher/internal/admin"
	"github.com/jjagpal/earl-scheib-watcher/internal/webhook"
)

// ---- Helpers ----

type capturedRequest struct {
	method string
	path   string
	body   []byte
	sig    string
}

func newFakeRemote(t *testing.T, respStatus int, respBody []byte, captured *atomic.Pointer[capturedRequest]) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		captured.Store(&capturedRequest{
			method: r.Method,
			path:   r.URL.Path,
			body:   append([]byte(nil), body...),
			sig:    r.Header.Get("X-EMS-Signature"),
		})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(respStatus)
		_, _ = w.Write(respBody)
	}))
}

// startAdminWithURLCh is the single entrypoint for starting admin.Run in tests.
// It uses admin.Config.URLCh to receive the bound URL deterministically.
// Appends "/earlscheibconcord" to remoteURL so the WebhookURL matches production
// shape ("https://host/earlscheibconcord") — ensures upstream path assertions
// like "/earlscheibconcord/queue" exercise the real URL construction logic.
func startAdminWithURLCh(t *testing.T, remoteURL, secret string, heartbeat time.Duration) (string, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	urlCh := make(chan string, 1)
	cfg := admin.Config{
		WebhookURL:       remoteURL + "/earlscheibconcord",
		Secret:           secret,
		AppVersion:       "test",
		HeartbeatTimeout: heartbeat,
		ShutdownGrace:    200 * time.Millisecond,
		OpenBrowser:      nil,
		URLCh:            urlCh,
	}
	done := make(chan error, 1)
	go func() { done <- admin.Run(ctx, cfg) }()

	select {
	case u := <-urlCh:
		return u, func() {
			cancel()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Log("admin.Run did not return within 2s after cancel")
			}
		}
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("admin did not bind within 3s")
		return "", func() {}
	}
}

func postJSON(t *testing.T, urlStr, body string) (*http.Response, []byte) {
	t.Helper()
	resp, err := http.Post(urlStr, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", urlStr, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func getJSON(t *testing.T, urlStr string) (*http.Response, []byte) {
	t.Helper()
	resp, err := http.Get(urlStr)
	if err != nil {
		t.Fatalf("GET %s: %v", urlStr, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

// ---- Tests ----

func TestProxyQueue_SignsEmptyBodyAndForwardsGET(t *testing.T) {
	secret := "test-secret-ADMIN-02"
	captured := &atomic.Pointer[capturedRequest]{}
	remote := newFakeRemote(t, 200, []byte(`[{"id":1,"name":"Alice"}]`), captured)
	defer remote.Close()

	adminURL, stop := startAdminWithURLCh(t, remote.URL, secret, 5*time.Second)
	defer stop()

	resp, body := getJSON(t, adminURL+"/api/queue")
	if resp.StatusCode != 200 {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}
	if string(body) != `[{"id":1,"name":"Alice"}]` {
		t.Errorf("body not forwarded verbatim: %s", body)
	}

	cr := captured.Load()
	if cr == nil {
		t.Fatal("fake remote never captured a request")
	}
	if cr.method != http.MethodGet {
		t.Errorf("upstream method: got %s, want GET", cr.method)
	}
	wantSig := webhook.Sign(secret, []byte(""))
	if cr.sig != wantSig {
		t.Errorf("upstream X-EMS-Signature: got %q, want %q", cr.sig, wantSig)
	}
	if !strings.HasSuffix(cr.path, "/earlscheibconcord/queue") {
		t.Errorf("upstream path: got %q, want suffix /earlscheibconcord/queue", cr.path)
	}
}

func TestProxyCancel_SignsJSONBodyAndForwardsAsDELETE(t *testing.T) {
	secret := "test-secret-cancel"
	captured := &atomic.Pointer[capturedRequest]{}
	remote := newFakeRemote(t, 200, []byte(`{"deleted":1}`), captured)
	defer remote.Close()

	adminURL, stop := startAdminWithURLCh(t, remote.URL, secret, 5*time.Second)
	defer stop()

	resp, body := postJSON(t, adminURL+"/api/cancel", `{"id": 42}`)
	if resp.StatusCode != 200 {
		t.Fatalf("status: got %d, want 200; body=%s", resp.StatusCode, body)
	}

	cr := captured.Load()
	if cr == nil {
		t.Fatal("fake remote never captured a request")
	}
	if cr.method != http.MethodDelete {
		t.Errorf("upstream method: got %s, want DELETE", cr.method)
	}
	// Canonical JSON: compact, no whitespace
	wantBody := `{"id":42}`
	if string(cr.body) != wantBody {
		t.Errorf("upstream body: got %q, want %q", cr.body, wantBody)
	}
	wantSig := webhook.Sign(secret, []byte(wantBody))
	if cr.sig != wantSig {
		t.Errorf("upstream X-EMS-Signature: got %q, want %q", cr.sig, wantSig)
	}
}

func TestProxyCancel_Propagates404(t *testing.T) {
	captured := &atomic.Pointer[capturedRequest]{}
	notFoundBody := []byte(`{"error":"not found or already sent"}`)
	remote := newFakeRemote(t, 404, notFoundBody, captured)
	defer remote.Close()

	adminURL, stop := startAdminWithURLCh(t, remote.URL, "secret", 5*time.Second)
	defer stop()

	resp, body := postJSON(t, adminURL+"/api/cancel", `{"id": 999}`)
	if resp.StatusCode != 404 {
		t.Errorf("status: got %d, want 404", resp.StatusCode)
	}
	if !bytes.Equal(body, notFoundBody) {
		t.Errorf("body forwarding: got %q, want %q", body, notFoundBody)
	}
}

func TestProxyQueue_BadMethod(t *testing.T) {
	captured := &atomic.Pointer[capturedRequest]{}
	remote := newFakeRemote(t, 200, []byte(`[]`), captured)
	defer remote.Close()

	adminURL, stop := startAdminWithURLCh(t, remote.URL, "secret", 5*time.Second)
	defer stop()

	req, _ := http.NewRequest(http.MethodPut, adminURL+"/api/queue", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status: got %d, want 405", resp.StatusCode)
	}
}

func TestProxyCancel_BadJSON(t *testing.T) {
	captured := &atomic.Pointer[capturedRequest]{}
	remote := newFakeRemote(t, 200, []byte(`{}`), captured)
	defer remote.Close()

	adminURL, stop := startAdminWithURLCh(t, remote.URL, "secret", 5*time.Second)
	defer stop()

	resp, _ := postJSON(t, adminURL+"/api/cancel", "not-json")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", resp.StatusCode)
	}
	if captured.Load() != nil {
		t.Error("upstream must not be called on malformed body")
	}
}

func TestAlive_ResetsHeartbeatAndShutdownOnIdle(t *testing.T) {
	captured := &atomic.Pointer[capturedRequest]{}
	remote := newFakeRemote(t, 200, []byte(`[]`), captured)
	defer remote.Close()

	// Short heartbeat window for fast test.
	hb := 150 * time.Millisecond
	adminURL, stop := startAdminWithURLCh(t, remote.URL, "secret", hb)
	t.Cleanup(stop)

	// Post /alive every 40ms for 400ms -> must stay alive.
	deadline := time.Now().Add(400 * time.Millisecond)
	for time.Now().Before(deadline) {
		resp, err := http.Post(adminURL+"/alive", "text/plain", strings.NewReader(""))
		if err != nil {
			t.Fatalf("heartbeat POST failed mid-stream: %v", err)
		}
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("/alive status: got %d, want 204", resp.StatusCode)
		}
		resp.Body.Close()
		time.Sleep(40 * time.Millisecond)
	}
	// Server must still be alive — one more GET succeeds.
	resp, err := http.Get(adminURL + "/api/queue")
	if err != nil {
		t.Fatalf("admin server died during heartbeat cycle: %v", err)
	}
	resp.Body.Close()
}

func TestHeartbeatTimeout_TriggersShutdown(t *testing.T) {
	captured := &atomic.Pointer[capturedRequest]{}
	remote := newFakeRemote(t, 200, []byte(`[]`), captured)
	defer remote.Close()

	// Run Run directly so we can observe the return.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	urlCh := make(chan string, 1)
	done := make(chan error, 1)
	cfg := admin.Config{
		WebhookURL:       remote.URL,
		Secret:           "s",
		AppVersion:       "t",
		HeartbeatTimeout: 100 * time.Millisecond,
		ShutdownGrace:    100 * time.Millisecond,
		URLCh:            urlCh,
	}
	go func() { done <- admin.Run(ctx, cfg) }()

	select {
	case <-urlCh:
	case <-time.After(2 * time.Second):
		t.Fatal("admin did not bind")
	}

	// Do NOT post /alive. Expect Run to return within HeartbeatTimeout + ShutdownGrace + slack.
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Run did not shutdown on heartbeat timeout within 1s")
	}
}

func TestPortBind_ReturnsLocalhostURL(t *testing.T) {
	captured := &atomic.Pointer[capturedRequest]{}
	remote := newFakeRemote(t, 200, []byte(`[]`), captured)
	defer remote.Close()

	adminURL, stop := startAdminWithURLCh(t, remote.URL, "secret", 5*time.Second)
	defer stop()

	u, err := url.Parse(adminURL)
	if err != nil {
		t.Fatalf("parse admin URL: %v", err)
	}
	if u.Hostname() != "127.0.0.1" {
		t.Errorf("hostname: got %q, want 127.0.0.1", u.Hostname())
	}
	if u.Port() == "" || u.Port() == "0" {
		t.Errorf("port must be non-zero ephemeral, got %q", u.Port())
	}
}

func TestProxySendNow_SignsJSONBodyAndForwardsAsPOST(t *testing.T) {
	secret := "test-secret-send-now"
	captured := &atomic.Pointer[capturedRequest]{}
	remote := newFakeRemote(t, 200, []byte(`{"sent":true}`), captured)
	defer remote.Close()

	adminURL, stop := startAdminWithURLCh(t, remote.URL, secret, 5*time.Second)
	defer stop()

	resp, body := postJSON(t, adminURL+"/api/send-now", `{"id": 7}`)
	if resp.StatusCode != 200 {
		t.Fatalf("status: got %d, want 200; body=%s", resp.StatusCode, body)
	}

	cr := captured.Load()
	if cr == nil {
		t.Fatal("fake remote never captured a request")
	}
	if cr.method != http.MethodPost {
		t.Errorf("upstream method: got %s, want POST", cr.method)
	}
	if !strings.HasSuffix(cr.path, "/earlscheibconcord/queue/send-now") {
		t.Errorf("upstream path: got %q, want suffix /earlscheibconcord/queue/send-now", cr.path)
	}
	wantBody := `{"id":7}`
	if string(cr.body) != wantBody {
		t.Errorf("upstream body: got %q, want %q", cr.body, wantBody)
	}
	wantSig := webhook.Sign(secret, []byte(wantBody))
	if cr.sig != wantSig {
		t.Errorf("upstream X-EMS-Signature: got %q, want %q", cr.sig, wantSig)
	}
}

func TestProxySendNow_Propagates404(t *testing.T) {
	captured := &atomic.Pointer[capturedRequest]{}
	notFoundBody := []byte(`{"error":"not_found_or_already_sent"}`)
	remote := newFakeRemote(t, 404, notFoundBody, captured)
	defer remote.Close()

	adminURL, stop := startAdminWithURLCh(t, remote.URL, "secret", 5*time.Second)
	defer stop()

	resp, body := postJSON(t, adminURL+"/api/send-now", `{"id": 999}`)
	if resp.StatusCode != 404 {
		t.Errorf("status: got %d, want 404", resp.StatusCode)
	}
	if !bytes.Equal(body, notFoundBody) {
		t.Errorf("body forwarding: got %q, want %q", body, notFoundBody)
	}
}

func TestProxySendNow_BadJSON(t *testing.T) {
	captured := &atomic.Pointer[capturedRequest]{}
	remote := newFakeRemote(t, 200, []byte(`{}`), captured)
	defer remote.Close()

	adminURL, stop := startAdminWithURLCh(t, remote.URL, "secret", 5*time.Second)
	defer stop()

	resp, _ := postJSON(t, adminURL+"/api/send-now", "not-json")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", resp.StatusCode)
	}
	if captured.Load() != nil {
		t.Error("upstream must not be called on malformed body")
	}
}

func TestProxyQueue_PropagatesUpstream401(t *testing.T) {
	captured := &atomic.Pointer[capturedRequest]{}
	remote := newFakeRemote(t, 401, []byte(`{"error":"invalid signature"}`), captured)
	defer remote.Close()

	adminURL, stop := startAdminWithURLCh(t, remote.URL, "wrong-secret-server-will-reject", 5*time.Second)
	defer stop()

	resp, body := getJSON(t, adminURL+"/api/queue")
	if resp.StatusCode != 401 {
		t.Errorf("status: got %d, want 401", resp.StatusCode)
	}
	var parsed map[string]string
	_ = json.Unmarshal(body, &parsed)
	if parsed["error"] != "invalid signature" {
		t.Errorf("body: got %s", body)
	}
}
