package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jjagpal/earl-scheib-watcher/internal/webhook"
)

// handleQueue proxies GET /api/queue -> GET {webhookURL}/earlscheibconcord/queue.
// The browser sends no body; the outbound HMAC signs []byte("") matching the
// remote-config precedent in internal/remoteconfig/remoteconfig.go.
func (s *server) handleQueue(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	sig := webhook.Sign(s.cfg.Secret, []byte(""))

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.remoteQueueURL(), nil)
	if err != nil {
		s.jsonError(w, http.StatusInternalServerError, "build request: "+err.Error())
		return
	}
	req.Header.Set("X-EMS-Signature", sig)
	req.Header.Set("X-EMS-Source", "EarlScheibWatcher-Admin")

	resp, err := s.client.Do(req)
	if err != nil {
		s.jsonError(w, http.StatusBadGateway, "upstream unreachable: "+err.Error())
		return
	}
	defer resp.Body.Close()

	// Forward status and body verbatim. Content-Type is always JSON here.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		s.jsonError(w, http.StatusBadGateway, "read upstream body: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(body)
}

// handleCancel proxies POST /api/cancel -> DELETE {webhookURL}/earlscheibconcord/queue.
// The browser posts JSON {"id": N}; we re-encode to a canonical compact form
// and HMAC-sign the exact bytes we forward upstream (matches the telemetry
// precedent: sign the raw outbound body).
func (s *server) handleCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	raw, err := io.ReadAll(io.LimitReader(r.Body, 1024))
	if err != nil {
		s.jsonError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var parsed struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil || parsed.ID == 0 {
		s.jsonError(w, http.StatusBadRequest, "body must be {\"id\": N} with non-zero integer N")
		return
	}

	// Re-marshal to a canonical body: compact, no whitespace, stable field order.
	// The HMAC must cover these exact bytes (both outbound and server-side validation).
	outBody, err := json.Marshal(struct {
		ID int64 `json:"id"`
	}{ID: parsed.ID})
	if err != nil {
		s.jsonError(w, http.StatusInternalServerError, "marshal: "+err.Error())
		return
	}

	sig := webhook.Sign(s.cfg.Secret, outBody)

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, s.remoteQueueURL(), bytes.NewReader(outBody))
	if err != nil {
		s.jsonError(w, http.StatusInternalServerError, "build request: "+err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Length", fmt.Sprintf("%d", len(outBody)))
	req.Header.Set("X-EMS-Signature", sig)
	req.Header.Set("X-EMS-Source", "EarlScheibWatcher-Admin")

	resp, err := s.client.Do(req)
	if err != nil {
		s.jsonError(w, http.StatusBadGateway, "upstream unreachable: "+err.Error())
		return
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		s.jsonError(w, http.StatusBadGateway, "read upstream body: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(respBody)
}

// handleSendNow proxies POST /api/send-now -> POST {webhookURL}/queue/send-now.
// Mirrors handleCancel byte-for-byte (the only differences are the upstream
// method=POST and URL=/queue/send-now — everything else about canonical
// re-marshal + HMAC sign + body/status relay is the established pattern).
func (s *server) handleSendNow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	raw, err := io.ReadAll(io.LimitReader(r.Body, 1024))
	if err != nil {
		s.jsonError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var parsed struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil || parsed.ID == 0 {
		s.jsonError(w, http.StatusBadRequest, "body must be {\"id\": N} with non-zero integer N")
		return
	}

	// Re-marshal to canonical compact JSON — the HMAC covers these exact bytes.
	outBody, err := json.Marshal(struct {
		ID int64 `json:"id"`
	}{ID: parsed.ID})
	if err != nil {
		s.jsonError(w, http.StatusInternalServerError, "marshal: "+err.Error())
		return
	}

	sig := webhook.Sign(s.cfg.Secret, outBody)

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.remoteSendNowURL(), bytes.NewReader(outBody))
	if err != nil {
		s.jsonError(w, http.StatusInternalServerError, "build request: "+err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Length", fmt.Sprintf("%d", len(outBody)))
	req.Header.Set("X-EMS-Signature", sig)
	req.Header.Set("X-EMS-Source", "EarlScheibWatcher-Admin")

	resp, err := s.client.Do(req)
	if err != nil {
		s.jsonError(w, http.StatusBadGateway, "upstream unreachable: "+err.Error())
		return
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		s.jsonError(w, http.StatusBadGateway, "read upstream body: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(respBody)
}

func (s *server) jsonError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// WMH-03: known job_types — used as a whitelist on PUT so the proxy isn't
// a free-range pass-through to arbitrary upstream paths. Must stay in lock-
// step with app.py JOB_TYPE_META.
var validTemplateJobTypes = map[string]bool{
	"24h":    true,
	"3day":   true,
	"review": true,
}

// handleTemplatesList proxies GET /api/templates ->
//
//	GET {webhookURL}/earlscheibconcord/templates.
//
// Mirrors handleQueue exactly (HMAC-sign []byte(""), 1 MiB body limit).
func (s *server) handleTemplatesList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	sig := webhook.Sign(s.cfg.Secret, []byte(""))

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.remoteTemplatesURL(), nil)
	if err != nil {
		s.jsonError(w, http.StatusInternalServerError, "build request: "+err.Error())
		return
	}
	req.Header.Set("X-EMS-Signature", sig)
	req.Header.Set("X-EMS-Source", "EarlScheibWatcher-Admin")

	resp, err := s.client.Do(req)
	if err != nil {
		s.jsonError(w, http.StatusBadGateway, "upstream unreachable: "+err.Error())
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		s.jsonError(w, http.StatusBadGateway, "read upstream body: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(body)
}

// handleTemplateUpsert proxies PUT /api/templates/{job_type} ->
//
//	PUT {webhookURL}/earlscheibconcord/templates/{job_type}.
//
// Canonical-rewrites the body to {"body":"..."} before signing + forwarding
// (matches handleSendNow / handleCancel). job_type is whitelisted so the
// proxy never fans out to arbitrary upstream paths.
func (s *server) handleTemplateUpsert(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	jobType := strings.TrimPrefix(r.URL.Path, "/api/templates/")
	if jobType == "" || strings.Contains(jobType, "/") {
		s.jsonError(w, http.StatusBadRequest, "missing or malformed job_type")
		return
	}
	if !validTemplateJobTypes[jobType] {
		s.jsonError(w, http.StatusBadRequest, "unknown job_type: "+jobType)
		return
	}

	// Max body 4 KiB — templates are capped at 2000 chars server-side but
	// allow some JSON slack (field name + whitespace + escaping).
	raw, err := io.ReadAll(io.LimitReader(r.Body, 4096))
	if err != nil {
		s.jsonError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var parsed struct {
		Body string `json:"body"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		s.jsonError(w, http.StatusBadRequest, "body must be {\"body\": \"...\"}")
		return
	}

	// Re-marshal to canonical compact JSON — the HMAC covers these exact bytes.
	outBody, err := json.Marshal(struct {
		Body string `json:"body"`
	}{Body: parsed.Body})
	if err != nil {
		s.jsonError(w, http.StatusInternalServerError, "marshal: "+err.Error())
		return
	}

	sig := webhook.Sign(s.cfg.Secret, outBody)

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, s.remoteTemplateURL(jobType), bytes.NewReader(outBody))
	if err != nil {
		s.jsonError(w, http.StatusInternalServerError, "build request: "+err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Length", fmt.Sprintf("%d", len(outBody)))
	req.Header.Set("X-EMS-Signature", sig)
	req.Header.Set("X-EMS-Source", "EarlScheibWatcher-Admin")

	resp, err := s.client.Do(req)
	if err != nil {
		s.jsonError(w, http.StatusBadGateway, "upstream unreachable: "+err.Error())
		return
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		s.jsonError(w, http.StatusBadGateway, "read upstream body: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(respBody)
}
