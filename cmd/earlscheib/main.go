package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jjagpal/earl-scheib-watcher/internal/admin"
	"github.com/jjagpal/earl-scheib-watcher/internal/commands"
	"github.com/jjagpal/earl-scheib-watcher/internal/config"
	"github.com/jjagpal/earl-scheib-watcher/internal/db"
	"github.com/jjagpal/earl-scheib-watcher/internal/heartbeat"
	"github.com/jjagpal/earl-scheib-watcher/internal/install"
	"github.com/jjagpal/earl-scheib-watcher/internal/logging"
	"github.com/jjagpal/earl-scheib-watcher/internal/remoteconfig"
	"github.com/jjagpal/earl-scheib-watcher/internal/scanner"
	"github.com/jjagpal/earl-scheib-watcher/internal/status"
	"github.com/jjagpal/earl-scheib-watcher/internal/telemetry"
	"github.com/jjagpal/earl-scheib-watcher/internal/webhook"
)

// secretKey is injected at build time via:
//
//	-ldflags "-X main.secretKey=<value>"
//
// Never set a real secret here — this default is for dev/test builds only.
var secretKey = "dev-test-secret-do-not-use-in-production"

// appVersion is injected at build time via:
//
//	-ldflags "-X main.appVersion=<value>"
//
// Defaults to "dev" for local/test builds. Production Makefile sets the real version.
var appVersion = "dev"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(0)
	}

	// Initialize telemetry before routing to subcommand.
	// Reads webhookURL from config (best-effort; ignores error).
	// Init is lightweight — just stores config, no network calls.
	// Logger is nil here; each command re-inits tel with a real logger once one is set up.
	dataDir := config.DataDir()
	cfg, _ := config.LoadConfig(filepath.Join(dataDir, "config.ini"))
	tel := telemetry.Init(cfg.WebhookURL, secretKey, appVersion, nil)

	switch os.Args[1] {
	case "--tray":
		runStub("tray")
	case "--scan":
		runScan(tel)
	case "--wizard":
		runStub("wizard")
	case "--test":
		runTest(tel)
	case "--status":
		runStatus(tel)
	case "--install":
		runInstall()
	case "--uninstall":
		runUninstall()
	case "--configure":
		runConfigure()
	case "--admin":
		runAdmin(tel)
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

// runScan loads config, opens the DB, sends heartbeat, and runs the scanner.
// The entire scan body is wrapped in telemetry.Wrap so any panic is captured
// and POSTed before the process exits.
// Exits 0 on success, 1 if any errors occurred during the scan run.
func runScan(tel *telemetry.Telemetry) {
	dataDir := config.DataDir()
	cfgPath := filepath.Join(dataDir, "config.ini")

	// --- remote-config: best-effort, runs BEFORE loading effective config ---
	// Load defaults to get the webhook URL for the remote-config fetch.
	// A 5-second timeout is built into remoteconfig.Fetch; failures are logged
	// to stderr and the scan continues with the local config (OPS-05).
	rcCfg, _ := config.LoadConfig(cfgPath)
	remote, rcErr := remoteconfig.Fetch(context.Background(), rcCfg.WebhookURL, secretKey, nil)
	if rcErr != nil {
		// Best-effort: log to stderr only, do not fail scan.
		fmt.Fprintf(os.Stderr, "remote-config fetch: %v\n", rcErr)
	} else if len(remote) > 0 {
		if _, applyErr := remoteconfig.Apply(cfgPath, remote, nil); applyErr != nil {
			fmt.Fprintf(os.Stderr, "remote-config apply: %v\n", applyErr)
		}
	}
	// --- now load effective config (may have been updated by remote-config above) ---

	cfg, _ := config.LoadConfig(cfgPath)
	logger := logging.SetupLogging(dataDir, cfg.LogLevel)

	// Re-init telemetry with the real logger and (possibly updated) webhookURL.
	// This ensures any crash inside Wrap has a functioning logger for debug output.
	tel = telemetry.Init(cfg.WebhookURL, secretKey, appVersion, logger)

	_ = tel.Wrap(func() error {
		sqlDB, err := db.Open(filepath.Join(dataDir, "ems_watcher.db"))
		if err != nil {
			logger.Error("db open failed", "err", err)
			os.Exit(1)
		}
		defer sqlDB.Close()

		if initErr := db.InitSchema(sqlDB); initErr != nil {
			logger.Error("db schema init failed", "err", initErr)
			os.Exit(1)
		}

		heartbeat.Send(cfg.WebhookURL, secretKey, logger)

		// Operator commands: best-effort poll, act on any pending command
		// (today just "upload_log"). Failures here never block the scan.
		if cmds := commands.Poll(context.Background(), cfg.WebhookURL, secretKey, logger); cmds != nil {
			hostName, _ := os.Hostname()
			commands.Handle(context.Background(), cmds, cfg.WebhookURL, secretKey, dataDir, hostName, logger)
		}

		sendFn := func(filePath string, body []byte) bool {
			return webhook.Send(webhook.SendConfig{
				WebhookURL: cfg.WebhookURL,
				SecretKey:  secretKey,
				Timeout:    30 * time.Second,
			}, filePath, body, logger)
		}

		processed, errors := scanner.Run(scanner.RunConfig{
			WatchFolder: cfg.WatchFolder,
			WebhookURL:  cfg.WebhookURL,
			AppVersion:  appVersion,
			DB:          sqlDB,
			Logger:      logger,
			Sender:      sendFn,
		})

		logger.Info("Run complete", "processed", processed, "errors", errors)
		if errors > 0 {
			os.Exit(1)
		}
		return nil
	})
}

// runTest sends a canned BMS test payload to the configured webhook.
// Uses the exact TEST_BMS_XML bytes from the Python reference (ems_watcher.py lines 426–444).
// Wrapped with telemetry.Wrap so any panic is captured before exit.
// Exits 0 on HTTP 2xx, 1 on failure.
func runTest(tel *telemetry.Telemetry) {
	// TEST_BMS_XML — exact bytes from Python (leading newline after triple-quote included).
	testPayload := []byte(`
<?xml version="1.0" encoding="UTF-8"?>
<VehicleDamageEstimateAddRq xmlns="http://www.cieca.com/BMS">
  <DocumentInfo>
    <DocumentID>TEST-EMS-WATCHER</DocumentID>
    <DocumentVerCode>TEST-EMS-WATCHER-V1</DocumentVerCode>
    <DocumentStatus>E</DocumentStatus>
    <CreateDateTime>2026-01-01T00:00:00Z</CreateDateTime>
  </DocumentInfo>
  <EventInfo>
    <RepairEvent>
      <CloseDateTime>2026-01-01T00:00:00Z</CloseDateTime>
    </RepairEvent>
  </EventInfo>
  <Owner>
    <GivenName>Test</GivenName>
    <OtherOrSurName>Watcher</OtherOrSurName>
    <CommPhone>5555550123</CommPhone>
  </Owner>
</VehicleDamageEstimateAddRq>`)

	dataDir := config.DataDir()
	cfg, _ := config.LoadConfig(filepath.Join(dataDir, "config.ini"))
	logger := logging.SetupLogging(dataDir, cfg.LogLevel)

	tel = telemetry.Init(cfg.WebhookURL, secretKey, appVersion, logger)

	_ = tel.Wrap(func() error {
		logger.Info("Sending BMS test POST", "url", cfg.WebhookURL)
		ok := webhook.Send(webhook.SendConfig{
			WebhookURL: cfg.WebhookURL,
			SecretKey:  secretKey,
			Timeout:    30 * time.Second,
		}, "test_payload.xml", testPayload, logger)

		if ok {
			fmt.Println("Test POST succeeded (HTTP 2xx).")
			os.Exit(0)
		}
		fmt.Println("Test POST FAILED. See ems_watcher.log for details.")
		os.Exit(1)
		return nil
	})
}

// runStatus prints folder reachability, run counts, recent files, and recent log
// errors to stdout. Delegates to status.Print.
// Wrapped with telemetry.Wrap so any panic is captured before exit.
// Exits 0.
func runStatus(tel *telemetry.Telemetry) {
	dataDir := config.DataDir()
	cfg, _ := config.LoadConfig(filepath.Join(dataDir, "config.ini"))
	logger := logging.SetupLogging(dataDir, cfg.LogLevel)

	tel = telemetry.Init(cfg.WebhookURL, secretKey, appVersion, logger)

	_ = tel.Wrap(func() error {
		dbPath := filepath.Join(dataDir, "ems_watcher.db")
		sqlDB, err := db.Open(dbPath)
		if err == nil {
			defer sqlDB.Close()
		} else {
			// DB not yet created — pass nil so status.Print shows "No database yet"
			sqlDB = nil
		}

		status.Print(cfg, dataDir, sqlDB, logger, os.Stdout)
		os.Exit(0)
		return nil
	})
}

// runInstall runs the console-based install wizard (portable-zip distribution).
// Requires administrator privileges. Prints a clear error and exits 1 if not elevated.
func runInstall() {
	if err := install.Run(install.Options{}); err != nil {
		fmt.Fprintf(os.Stderr, "install failed: %v\n", err)
		os.Exit(1)
	}
}

// runUninstall removes the Scheduled Task and optionally the data directory.
// Requires administrator privileges.
func runUninstall() {
	if err := install.Uninstall(install.UninstallOptions{}); err != nil {
		fmt.Fprintf(os.Stderr, "uninstall failed: %v\n", err)
		os.Exit(1)
	}
}

// runConfigure re-runs folder selection and connection test without reinstalling
// the Scheduled Task. Useful after changing the CCC ONE export folder location.
func runConfigure() {
	if err := install.Configure(install.Options{}); err != nil {
		fmt.Fprintf(os.Stderr, "configure failed: %v\n", err)
		os.Exit(1)
	}
}

// runAdmin launches the local browser-based queue admin UI.
//
// Starts an HTTP server on 127.0.0.1:EPHEMERAL, opens Marco's default
// browser to it, and proxies /api/queue + /api/cancel to the remote
// webhook server with HMAC-signed requests. Blocks until the browser tab
// is closed (30 s heartbeat timeout) or Ctrl+C is pressed.
//
// Wrapped in tel.Wrap so any panic in the admin server is captured and
// POSTed to {webhookURL}/telemetry before exit.
func runAdmin(tel *telemetry.Telemetry) {
	dataDir := config.DataDir()
	cfg, _ := config.LoadConfig(filepath.Join(dataDir, "config.ini"))
	logger := logging.SetupLogging(dataDir, cfg.LogLevel)

	// Re-init telemetry with the real logger now that logging is up.
	tel = telemetry.Init(cfg.WebhookURL, secretKey, appVersion, logger)

	_ = tel.Wrap(func() error {
		if cfg.WebhookURL == "" {
			logger.Error("admin: webhook_url is empty — edit config.ini or re-run installer")
			fmt.Fprintln(os.Stderr, "admin: webhook_url is empty — cannot proxy to remote queue.")
			os.Exit(1)
		}

		logger.Info("admin: starting", "webhook_url", cfg.WebhookURL)

		err := admin.Run(context.Background(), admin.Config{
			WebhookURL:       cfg.WebhookURL,
			Secret:           secretKey,
			AppVersion:       appVersion,
			Logger:           logger,
			HeartbeatTimeout: 30 * time.Second,
			ShutdownGrace:    5 * time.Second,
			OpenBrowser:      admin.Open,
		})
		if err != nil {
			logger.Error("admin: server exited with error", "err", err)
			fmt.Fprintf(os.Stderr, "admin: %v\n", err)
			os.Exit(1)
		}
		logger.Info("admin: exited cleanly")
		return nil
	})
}

func runStub(name string) {
	fmt.Printf("earlscheib %s: not yet implemented\n", name)
	os.Exit(0)
}

func printUsage() {
	fmt.Println("usage: earlscheib [--tray|--scan|--wizard|--test|--status|--install|--uninstall|--configure|--admin]")
}
