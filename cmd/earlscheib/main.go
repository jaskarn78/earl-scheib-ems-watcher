package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jjagpal/earl-scheib-watcher/internal/config"
	"github.com/jjagpal/earl-scheib-watcher/internal/db"
	"github.com/jjagpal/earl-scheib-watcher/internal/heartbeat"
	"github.com/jjagpal/earl-scheib-watcher/internal/logging"
	"github.com/jjagpal/earl-scheib-watcher/internal/scanner"
	"github.com/jjagpal/earl-scheib-watcher/internal/status"
	"github.com/jjagpal/earl-scheib-watcher/internal/webhook"
)

// secretKey is injected at build time via:
//
//	-ldflags "-X main.secretKey=<value>"
//
// Never set a real secret here — this default is for dev/test builds only.
var secretKey = "dev-test-secret-do-not-use-in-production"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(0)
	}

	switch os.Args[1] {
	case "--tray":
		runStub("tray")
	case "--scan":
		runScan()
	case "--wizard":
		runStub("wizard")
	case "--test":
		runTest()
	case "--status":
		runStatus()
	case "--install":
		runStub("install")
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

// runScan loads config, opens the DB, sends heartbeat, and runs the scanner.
// Exits 0 on success, 1 if any errors occurred during the scan run.
func runScan() {
	dataDir := config.DataDir()
	cfg, _ := config.LoadConfig(filepath.Join(dataDir, "config.ini"))
	logger := logging.SetupLogging(dataDir, cfg.LogLevel)

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

	sendFn := func(filePath string, body []byte) bool {
		return webhook.Send(webhook.SendConfig{
			WebhookURL: cfg.WebhookURL,
			SecretKey:  secretKey,
			Timeout:    30 * time.Second,
		}, filePath, body, logger)
	}

	processed, errors := scanner.Run(scanner.RunConfig{
		WatchFolder: cfg.WatchFolder,
		DB:          sqlDB,
		Logger:      logger,
		Sender:      sendFn,
	})

	logger.Info("Run complete", "processed", processed, "errors", errors)
	if errors > 0 {
		os.Exit(1)
	}
}

// runTest sends a canned BMS test payload to the configured webhook.
// Uses the exact TEST_BMS_XML bytes from the Python reference (ems_watcher.py lines 426–444).
// Exits 0 on HTTP 2xx, 1 on failure.
func runTest() {
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
}

// runStatus prints folder reachability, run counts, recent files, and recent log
// errors to stdout. Delegates to status.Print. Exits 0.
func runStatus() {
	dataDir := config.DataDir()
	cfg, _ := config.LoadConfig(filepath.Join(dataDir, "config.ini"))
	logger := logging.SetupLogging(dataDir, cfg.LogLevel)

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
}

func runStub(name string) {
	fmt.Printf("earlscheib %s: not yet implemented\n", name)
	os.Exit(0)
}

func printUsage() {
	fmt.Println("usage: earlscheib [--tray|--scan|--wizard|--test|--status|--install]")
}
