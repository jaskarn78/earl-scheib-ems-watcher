package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jjagpal/earl-scheib-watcher/internal/admin"
	"github.com/jjagpal/earl-scheib-watcher/internal/commands"
	"github.com/jjagpal/earl-scheib-watcher/internal/config"
	"github.com/jjagpal/earl-scheib-watcher/internal/db"
	"github.com/jjagpal/earl-scheib-watcher/internal/ems"
	"github.com/jjagpal/earl-scheib-watcher/internal/heartbeat"
	"github.com/jjagpal/earl-scheib-watcher/internal/install"
	"github.com/jjagpal/earl-scheib-watcher/internal/logging"
	"github.com/jjagpal/earl-scheib-watcher/internal/remoteconfig"
	"github.com/jjagpal/earl-scheib-watcher/internal/scanner"
	"github.com/jjagpal/earl-scheib-watcher/internal/status"
	"github.com/jjagpal/earl-scheib-watcher/internal/telemetry"
	"github.com/jjagpal/earl-scheib-watcher/internal/update"
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
	case "--dump-bundle":
		// Diagnostic-only: dump every column of every dBase file in a bundle.
		// Used to identify which CCC ONE field carries close/lock state since
		// every TRANS_TYPE we see is "E" — the close indicator must live in
		// some other field/file we don't currently parse. Marco runs this on
		// (a) a known-still-open RO and (b) a known-closed RO; the diff tells
		// us where to look.
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: earlscheib --dump-bundle <basename-or-path>")
			os.Exit(1)
		}
		runDumpBundle(os.Args[2])
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
			commands.Handle(context.Background(), cmds, cfg.WebhookURL, secretKey, dataDir, hostName, appVersion, update.DefaultLauncher, logger)
		}

		// Self-update: best-effort poll for a newer installer. Any error is logged
		// and swallowed — must never block the scan cycle. If an update is applied,
		// update.Check calls os.Exit(0) internally after launching the installer so
		// the installer can replace earlscheib.exe; the next Scheduled Task tick
		// (<=5 min) will re-launch with the new binary.
		if err := update.Check(context.Background(), cfg.WebhookURL, secretKey, dataDir, appVersion, logger, update.DefaultLauncher); err != nil {
			logger.Warn("update check failed (non-fatal)", "err", err)
		}

		sendFn := func(filePath string, body []byte) bool {
			// EMS dBase bundles are sent with ?trigger=ems_bundle so the
			// server-side /estimate handler can distinguish them from plain
			// CCC ONE BMS XML files (and, if it wants, route/tag accordingly).
			// Scanner flags bundles by using a virtual path ending in .bundle.
			url := cfg.WebhookURL
			if strings.HasSuffix(filePath, ".bundle") {
				if strings.Contains(url, "?") {
					url += "&trigger=ems_bundle"
				} else {
					url += "?trigger=ems_bundle"
				}
			}
			return webhook.Send(webhook.SendConfig{
				WebhookURL: url,
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
// By default: HTTP server on 127.0.0.1:EPHEMERAL, browser auto-opens, proxies
// /api/queue + /api/cancel + /api/send-now to the remote webhook with HMAC.
//
// Flags (after "--admin"):
//
//	--bind HOST:PORT    override the listener (e.g. 0.0.0.0:8080 for Tailscale)
//	--webhook URL       override config.ini webhook_url (useful when running
//	                    this binary on a non-Marco machine without a full
//	                    watcher install). Implicitly disables browser auto-open.
//
// Wrapped in tel.Wrap so any panic in the admin server is captured and
// POSTed to {webhookURL}/telemetry before exit.
func runAdmin(tel *telemetry.Telemetry) {
	// Parse post-"--admin" flags. Simple manual parse keeps the binary free of
	// flag-package ordering quirks with the dispatcher above.
	var bindOverride, webhookOverride string
	for i := 2; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "--bind":
			if i+1 < len(os.Args) {
				bindOverride = os.Args[i+1]
				i++
			}
		case "--webhook":
			if i+1 < len(os.Args) {
				webhookOverride = os.Args[i+1]
				i++
			}
		}
	}

	dataDir := config.DataDir()
	cfg, _ := config.LoadConfig(filepath.Join(dataDir, "config.ini"))
	if webhookOverride != "" {
		cfg.WebhookURL = webhookOverride
	}
	logger := logging.SetupLogging(dataDir, cfg.LogLevel)

	// Re-init telemetry with the real logger now that logging is up.
	tel = telemetry.Init(cfg.WebhookURL, secretKey, appVersion, logger)

	_ = tel.Wrap(func() error {
		if cfg.WebhookURL == "" {
			logger.Error("admin: webhook_url is empty — edit config.ini or re-run installer")
			fmt.Fprintln(os.Stderr, "admin: webhook_url is empty — cannot proxy to remote queue.")
			os.Exit(1)
		}

		// Remote-bind runs (e.g. Tailscale dev viewer) skip the auto-open —
		// the browser on this machine should not swallow a URL pointing at
		// an unspecified-interface host. User opens the Tailscale URL by hand.
		openBrowser := admin.Open
		if bindOverride != "" {
			openBrowser = nil
		}

		logger.Info("admin: starting", "webhook_url", cfg.WebhookURL, "bind", bindOverride)

		err := admin.Run(context.Background(), admin.Config{
			WebhookURL:       cfg.WebhookURL,
			Secret:           secretKey,
			AppVersion:       appVersion,
			Logger:           logger,
			HeartbeatTimeout: 24 * time.Hour,
			ShutdownGrace:    5 * time.Second,
			OpenBrowser:      openBrowser,
			BindAddr:         bindOverride,
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

// runDumpBundle prints every column of every dBase file in the bundle whose
// basename (or absolute path containing) matches the argument. Output goes to
// stdout in a paste-friendly format so the operator can hand it back over
// chat. Diagnostic-only — never POSTs anything, never touches the DB.
//
// Resolution rules for the argument:
//   - If absolute path: scan that directory directly, filter to files whose
//     basename (no extension) matches the path's last segment minus extension.
//     Example: `--dump-bundle "C:\EarlScheibWatcher\7ffa697a.veh"` dumps every
//     sibling of 7ffa697a.veh sharing the 7ffa697a basename.
//   - If bare basename (no path separators): look in config.WatchFolder for
//     files starting with that basename (case-insensitive).
//
// Exits 0 on success, 1 on missing folder / no matching bundle / any
// per-file dump error (errors are still printed; the non-zero exit code lets
// CI / scripts detect partial failures).
func runDumpBundle(arg string) {
	dataDir := config.DataDir()
	cfg, _ := config.LoadConfig(filepath.Join(dataDir, "config.ini"))

	// Pick the directory to scan and the basename to match.
	var dir, basename string
	if filepath.IsAbs(arg) || strings.ContainsAny(arg, `\/`) {
		dir = filepath.Dir(arg)
		stem := filepath.Base(arg)
		if ext := filepath.Ext(stem); ext != "" {
			stem = strings.TrimSuffix(stem, ext)
		}
		basename = stem
	} else {
		dir = cfg.WatchFolder
		basename = arg
	}

	if dir == "" {
		fmt.Fprintln(os.Stderr, "dump-bundle: watch_folder not configured and arg is not an absolute path")
		os.Exit(1)
	}

	// Use the existing detector so file-grouping rules stay consistent with
	// what the scanner does in production. A discardLogger keeps the dump
	// output clean of "Cannot read watch folder" lines.
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	bundles := scanner.DetectBundles(dir, logger)

	// Find the bundle whose basename matches (case-insensitive, prefix or
	// exact). Falls back to a plain prefix scan of the directory if the bundle
	// is missing required AD1+VEH (DetectBundles filters those out, but a
	// closed RO might be the very thing missing one of them).
	var match *scanner.BundleCandidate
	want := strings.ToLower(basename)
	for i := range bundles {
		if strings.EqualFold(bundles[i].Basename, basename) ||
			strings.HasPrefix(strings.ToLower(bundles[i].Basename), want) {
			match = &bundles[i]
			break
		}
	}

	files, err := bundleFilesByPrefix(dir, basename)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dump-bundle: cannot read %s: %v\n", dir, err)
		os.Exit(1)
	}
	if match != nil {
		// Prefer DetectBundles' own file map (correct case + dedup), then
		// merge in any extras from the prefix scan that DetectBundles dropped
		// (e.g. memo sidecars, files with duplicate extensions).
		for ext, p := range match.Files {
			if _, seen := files[ext]; !seen {
				files[ext] = p
			}
		}
	}

	if len(files) == 0 {
		fmt.Fprintf(os.Stderr, "dump-bundle: no files found for basename %q in %s\n", basename, dir)
		os.Exit(1)
	}

	// Sort files by extension so output is deterministic and grouped (.ad1
	// first, then .env, .lin, .veh, ...).
	exts := make([]string, 0, len(files))
	for e := range files {
		exts = append(exts, e)
	}
	sort.Strings(exts)

	fmt.Printf("# Bundle dump: basename=%s dir=%s files=%d\n",
		basename, dir, len(files))
	fmt.Println("# Paste this entire block back so we can spot the close/lock field.")
	fmt.Println()

	exit := 0
	for _, ext := range exts {
		path := files[ext]
		// Skip non-dBase sidecars (memo .DBT/.FPT, index .CDX/.MDX) — they
		// are companion files, not standalone tables, and dbase.OpenTable on
		// them errors. Listed explicitly so unknown extensions DO get tried.
		switch strings.ToLower(ext) {
		case "dbt", "fpt", "cdx", "mdx", "ndx":
			fmt.Printf("=== %s — companion sidecar, skipping table read ===\n\n",
				filepath.Base(path))
			continue
		}
		if err := ems.DumpAllFields(path, os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "dump-bundle: %v\n", err)
			exit = 1
		}
	}
	os.Exit(exit)
}

// bundleFilesByPrefix returns a map of lowercased extension → full path for
// every file in dir whose basename (minus last extension) matches stem
// (case-insensitive). Catches files DetectBundles would skip — required for
// closed ROs that may lack .AD1 or .VEH but still have other dBase files we
// need to inspect.
func bundleFilesByPrefix(dir, stem string) (map[string]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	want := strings.ToLower(stem)
	out := make(map[string]string)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		ext := filepath.Ext(name)
		if ext == "" {
			continue
		}
		base := strings.TrimSuffix(name, ext)
		if strings.ToLower(base) != want {
			continue
		}
		lowerExt := strings.ToLower(strings.TrimPrefix(ext, "."))
		// First-wins on duplicate extensions matches DetectBundles policy.
		if _, seen := out[lowerExt]; !seen {
			out[lowerExt] = filepath.Join(dir, name)
		}
	}
	return out, nil
}

func printUsage() {
	fmt.Println("usage: earlscheib [--tray|--scan|--wizard|--test|--status|--install|--uninstall|--configure|--admin|--dump-bundle <basename>]")
}
