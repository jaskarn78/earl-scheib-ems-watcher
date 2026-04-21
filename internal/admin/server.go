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
// HeartbeatTimeout / ShutdownGrace default to 30s and 5s when zero.
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
		cfg.HeartbeatTimeout = 30 * time.Second
	}
	if cfg.ShutdownGrace == 0 {
		cfg.ShutdownGrace = 5 * time.Second
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("admin.Run listen: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	url := fmt.Sprintf("http://127.0.0.1:%d", port)

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
	// Static UI at root
	mux.Handle("/", http.FileServer(http.FS(uiFS())))
	// API
	mux.HandleFunc("/api/queue", s.handleQueue)
	mux.HandleFunc("/api/cancel", s.handleCancel)
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

// remoteQueueURL returns the remote webhook base URL with
// /earlscheibconcord/queue appended (no trailing slash).
func (s *server) remoteQueueURL() string {
	return strings.TrimRight(s.cfg.WebhookURL, "/") + "/earlscheibconcord/queue"
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
