package heartbeat_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/jjagpal/earl-scheib-watcher/internal/heartbeat"
)

// testLogger returns a discard logger for tests.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestHeartbeatBody(t *testing.T) {
	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	heartbeat.Send(srv.URL, "test-secret", testLogger())

	body := string(capturedBody)
	if len(body) == 0 {
		t.Fatal("server received empty body")
	}
	if !containsAll(body, "<Heartbeat>", "<Host>", "</Host>", "</Heartbeat>") {
		t.Errorf("heartbeat body %q missing required XML tags", body)
	}
}

func TestHeartbeatHeaders(t *testing.T) {
	var capturedReq *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedReq = r
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	heartbeat.Send(srv.URL, "test-secret", testLogger())

	if capturedReq == nil {
		t.Fatal("server received no request")
	}

	// Content-Type must be application/xml
	ct := capturedReq.Header.Get("Content-Type")
	if ct != "application/xml" {
		t.Errorf("Content-Type = %q, want application/xml", ct)
	}

	// X-EMS-Heartbeat must be 1
	hb := capturedReq.Header.Get("X-Ems-Heartbeat")
	if hb != "1" {
		t.Errorf("X-EMS-Heartbeat = %q, want 1", hb)
	}

	// X-EMS-Signature must be present (non-empty secret provided)
	sig := capturedReq.Header.Get("X-Ems-Signature")
	if sig == "" {
		t.Error("X-EMS-Signature is absent, want HMAC hex")
	}
}

func TestHeartbeatEmptyURL(t *testing.T) {
	var callCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Should return immediately without making any HTTP request
	heartbeat.Send("", "test-secret", testLogger())

	if n := atomic.LoadInt32(&callCount); n != 0 {
		t.Errorf("expected 0 HTTP calls for empty URL, got %d", n)
	}
}

func TestHeartbeatNonFatal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError) // 500
	}))
	defer srv.Close()

	// Must not panic or block — non-fatal, just debug log
	heartbeat.Send(srv.URL, "test-secret", testLogger())
}

func TestHeartbeatEmptySecretSetsEmptySignature(t *testing.T) {
	var capturedReq *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedReq = r
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// With empty secret, Python sets sig = "" and still sends the header as empty string
	heartbeat.Send(srv.URL, "", testLogger())

	if capturedReq == nil {
		t.Fatal("server received no request")
	}

	// X-EMS-Signature should be empty string (Python: sig = "" if not secret_key)
	sig := capturedReq.Header.Get("X-Ems-Signature")
	if sig != "" {
		t.Errorf("X-EMS-Signature = %q with empty secret, want empty string", sig)
	}
}

// containsAll returns true if s contains all the given substrings.
func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		found := false
		for i := 0; i <= len(s)-len(sub); i++ {
			if s[i:i+len(sub)] == sub {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
