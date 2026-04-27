package admin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Config bundles the values admin.Run needs. Logger may be nil.
// HeartbeatTimeout / ShutdownGrace default to 30m and 5s when zero.
// 30 minutes gives Marco enough runway to leave the queue viewer open on a
// second monitor between jobs without it silently shutting down; the
// front-end also shows a friendly "Queue Viewer is resting" sleep panel
// (internal/admin/ui/main.js) after a failed poll so the reason is clear
// when it eventually times out.
// OpenBrowser is nil in tests; nil means "do not attempt to open a browser".
// URLCh, when non-nil, receives the bound URL exactly once immediately
// after net.Listen succeeds. Used by tests; production callers leave it nil.
type Config struct {
	WebhookURL       string
	Secret           string
	AppVersion       string
	Logger           *slog.Logger
	HeartbeatTimeout time.Duration
	ShutdownGrace    time.Duration
	OpenBrowser      func(url string) error
	URLCh            chan<- string
	// BindAddr optionally overrides the default "127.0.0.1:0" listener address.
	// Pass e.g. "0.0.0.0:8080" to expose the admin UI over a LAN / Tailscale so
	// operators can view Marco's queue remotely without installing a separate
	// binary. When unset, localhost-ephemeral binding is preserved (the default
	// Marco experience).
	BindAddr string
}

// server bundles the state the proxy handlers need at request time.
type server struct {
	cfg       Config
	client    *http.Client
	logger    *slog.Logger
	lastAlive *atomicTime
}

// Run starts the admin HTTP server on 127.0.0.1:EPHEMERAL, opens the browser,
// and blocks until one of: ctx is cancelled, SIGINT/SIGTERM is received, or
// the browser heartbeat has been silent for HeartbeatTimeout.
//
// Returns nil on clean shutdown; error only on listen/bind failure or
// non-http.ErrServerClosed serve errors.
func Run(ctx context.Context, cfg Config) error {
	if cfg.HeartbeatTimeout == 0 {
		// QAJ-03 TESTING BUMP: extended to 24h so the Queue Viewer stays
		// alive overnight during the multi-day field test. The existing
		// sleep-panel UI still triggers when the timer elapses; users
		// just reach it far less often.
		// TODO(qaj): revert to 30*time.Minute before prod re-release so
		// the watchdog auto-shutdown returns to its original cadence.
		cfg.HeartbeatTimeout = 24 * time.Hour
	}
	if cfg.ShutdownGrace == 0 {
		cfg.ShutdownGrace = 5 * time.Second
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	bindAddr := cfg.BindAddr
	if bindAddr == "" {
		bindAddr = "127.0.0.1:0"
	}
	listener, err := net.Listen("tcp", bindAddr)
	if err != nil {
		return fmt.Errorf("admin.Run listen: %w", err)
	}
	tcpAddr := listener.Addr().(*net.TCPAddr)
	port := tcpAddr.Port
	// URL host: if bound to 0.0.0.0 / :: / unspecified, advertise 127.0.0.1 for
	// the local browser-open hook. Remote viewers substitute their own host.
	host := tcpAddr.IP.String()
	if tcpAddr.IP == nil || tcpAddr.IP.IsUnspecified() {
		host = "127.0.0.1"
	}
	url := fmt.Sprintf("http://%s:%d", host, port)

	// Test hook: emit the bound URL to the caller non-blockingly.
	if cfg.URLCh != nil {
		select {
		case cfg.URLCh <- url:
		default:
		}
	}

	s := &server{
		cfg:       cfg,
		client:    &http.Client{Timeout: 30 * time.Second},
		logger:    logger,
		lastAlive: newAtomicTime(time.Now()),
	}

	mux := http.NewServeMux()
	// Static UI at root.
	// Wrap with no-store so binary upgrades reach Marco on a regular refresh.
	// embed.FS gives every file a zero (epoch) mtime, so without this header
	// browsers happily serve last week's index.html / main.js after the exe
	// is replaced — the symptom that broke the queue tabs after the Apr 27
	// upgrade. no-store forbids caching outright; revalidation can't be
	// content-aware here since embed.FS doesn't expose stable ETags.
	mux.Handle("/", noStoreMiddleware(http.FileServer(http.FS(uiFS()))))
	// API
	mux.HandleFunc("/api/queue", s.handleQueue)
	mux.HandleFunc("/api/cancel", s.handleCancel)
	mux.HandleFunc("/api/send-now", s.handleSendNow)
	mux.HandleFunc("/api/diagnostic", s.handleDiagnostic)
	// WMH-03: Marco-editable SMS templates. List (GET) at /api/templates;
	// upsert/revert (PUT) at /api/templates/{job_type}. Trailing slash on
	// the second route makes it a subtree so ServeMux routes the path parts
	// to the single handler.
	mux.HandleFunc("/api/templates", s.handleTemplatesList)
	mux.HandleFunc("/api/templates/", s.handleTemplateUpsert)
	mux.HandleFunc("/alive", s.handleAlive)

	httpServer := &http.Server{
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
	}

	// Signal handling: SIGINT / SIGTERM -> context cancel
	sigCtx, stopSignals := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	// Heartbeat watchdog: if no /alive for HeartbeatTimeout, trigger shutdown.
	watchdogCtx, cancelWatchdog := context.WithCancel(sigCtx)
	defer cancelWatchdog()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		// Tick often enough to notice a timeout within one HeartbeatTimeout/3 window.
		interval := cfg.HeartbeatTimeout / 3
		if interval < 10*time.Millisecond {
			interval = 10 * time.Millisecond
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-watchdogCtx.Done():
				return
			case now := <-ticker.C:
				if now.Sub(s.lastAlive.Load()) > cfg.HeartbeatTimeout {
					logger.Info("admin: heartbeat timeout — shutting down",
						"silent_for", now.Sub(s.lastAlive.Load()))
					cancelWatchdog()
					return
				}
			}
		}
	}()

	// Serve in a goroutine so we can wait on sigCtx + watchdogCtx
	serveErrCh := make(chan error, 1)
	go func() {
		fmt.Fprintf(os.Stdout, "admin UI: %s\n", url)
		serveErrCh <- httpServer.Serve(listener)
	}()

	// Best-effort browser open (skipped in tests when OpenBrowser is nil)
	if cfg.OpenBrowser != nil {
		if err := cfg.OpenBrowser(url); err != nil {
			logger.Warn("admin: failed to open browser", "err", err, "url", url)
		}
	}

	select {
	case err := <-serveErrCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("admin.Run serve: %w", err)
		}
		return nil
	case <-watchdogCtx.Done():
		// Graceful shutdown
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownGrace)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
		wg.Wait()
		// Drain serve goroutine
		<-serveErrCh
		return nil
	}
}

// noStoreMiddleware sets Cache-Control: no-store on every static UI response
// so browsers always pull a fresh copy after a binary upgrade.
func noStoreMiddleware(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		h.ServeHTTP(w, r)
	})
}

// remoteQueueURL returns the remote webhook base URL with /queue appended.
// WebhookURL already ends in /earlscheibconcord (same convention as every other
// package — telemetry appends /telemetry, remoteconfig appends /remote-config,
// heartbeat appends /heartbeat).
func (s *server) remoteQueueURL() string {
	return strings.TrimRight(s.cfg.WebhookURL, "/") + "/queue"
}

// remoteSendNowURL returns the remote /queue/send-now endpoint.
// Shares the same /earlscheibconcord prefix convention as remoteQueueURL.
func (s *server) remoteSendNowURL() string {
	return strings.TrimRight(s.cfg.WebhookURL, "/") + "/queue/send-now"
}

// remoteTemplatesURL returns the remote /templates endpoint (GET listing).
// WMH-03: Same prefix convention as remoteQueueURL / remoteSendNowURL.
func (s *server) remoteTemplatesURL() string {
	return strings.TrimRight(s.cfg.WebhookURL, "/") + "/templates"
}

// remoteTemplateURL returns the remote /templates/{job_type} endpoint (PUT).
func (s *server) remoteTemplateURL(jobType string) string {
	return s.remoteTemplatesURL() + "/" + jobType
}

// handleAlive is the browser heartbeat endpoint. Resets the last-alive timer.
func (s *server) handleAlive(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	s.lastAlive.Store(time.Now())
	w.WriteHeader(http.StatusNoContent)
}

// --- atomicTime ---------------------------------------------------------

// atomicTime is a mutex-guarded time.Time. sync/atomic does not support
// time.Time directly without typed-load gymnastics; a tiny mutex is simpler
// and fast enough for once-per-10s updates.
type atomicTime struct {
	mu sync.RWMutex
	t  time.Time
}

func newAtomicTime(t time.Time) *atomicTime { return &atomicTime{t: t} }

func (a *atomicTime) Load() time.Time {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.t
}

func (a *atomicTime) Store(t time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.t = t
}
