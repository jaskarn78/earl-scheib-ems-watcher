package admin

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// Config bundles the values admin.Run needs. Logger may be nil.
// HeartbeatTimeout / ShutdownGrace default to 30s and 5s when zero.
// OpenBrowser is nil in tests; nil means "do not attempt to open a browser".
type Config struct {
	WebhookURL       string
	Secret           string
	AppVersion       string
	Logger           *slog.Logger
	HeartbeatTimeout time.Duration
	ShutdownGrace    time.Duration
	OpenBrowser      func(url string) error
}

// Run is implemented fully in task 2 of this plan. This stub exists only so
// task 1 leaves the package in a buildable state.
func Run(ctx context.Context, cfg Config) error {
	_ = ctx
	_ = cfg
	return errors.New("admin.Run: stub — task 2 of plan 05-02 implements")
}
