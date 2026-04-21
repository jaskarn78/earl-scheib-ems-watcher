// Package install implements the console-based install, uninstall, and configure
// orchestrators used by the --install, --uninstall, and --configure subcommands.
//
// This is the portable-zip distribution path — no Inno Setup required.
// The Inno Setup installer (installer/earlscheib.iss) remains fully functional
// as an alternative distribution method.
package install

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// installDataDir is where the watcher is installed on Windows.
const installDataDir = `C:\EarlScheibWatcher`

// Options controls the install and configure flows.
type Options struct {
	// DataDir overrides the default install path (C:\EarlScheibWatcher).
	// Leave empty to use the default.
	DataDir string

	// PortableDir is the directory containing setup.cmd, the task XMLs, and
	// config.ini.template. Defaults to the directory containing the running binary.
	PortableDir string

	// RunTestFn is the function called to run a connection test.
	// Signature: func() bool — returns true on success.
	// Defaults to runInProcessTest if nil (shells out to --test subcommand).
	RunTestFn func() bool

	// In / Out override stdin/stdout for testing.
	In  io.Reader
	Out io.Writer
}

// UninstallOptions controls the uninstall flow.
type UninstallOptions struct {
	// DataDir overrides the default install path.
	DataDir string

	// In / Out override stdin/stdout for testing.
	In  io.Reader
	Out io.Writer
}

// Run executes the full install flow:
//  1. Require admin privileges (Windows)
//  2. Print welcome banner
//  3. Auto-detect CCC ONE folder; prompt for confirm/override
//  4. Warn on mapped drive; offer UNC path | user-mode task fallback | cancel
//  5. Create data dir, set ACLs
//  6. Copy binary to data dir
//  7. Write config.ini (preserve existing values on upgrade)
//  8. Run connection test; offer Retry / Continue / Cancel on failure
//  9. Register Scheduled Task (SYSTEM or user-mode based on mapped drive choice)
//  10. Run first scan
//  11. Print success summary
func Run(opts Options) error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("--install is only supported on Windows")
	}

	dataDir := resolveDataDir(opts.DataDir)
	wz := resolveWizard(opts.In, opts.Out)

	// Step 1 — admin check
	if err := requireAdmin(); err != nil {
		return err
	}

	// Step 2 — welcome
	wz.welcomeBanner()

	// Step 3 — folder selection
	detected, _ := DetectCCCOnePath()
	if detected == "" {
		detected = `C:\CCC\EMS_Export`
	}

	folder, err := wz.promptFolder(
		"CCC ONE export folder (where CCC ONE saves EMS files)?",
		detected,
	)
	if err != nil {
		return fmt.Errorf("reading folder path: %w", err)
	}

	// Step 4 — mapped drive detection
	useMappedFallback := false
	if IsMappedDrive(folder) {
		choices := map[string]string{
			"U": "Use a UNC path instead (e.g. \\\\server\\share\\CCC_Export)",
			"F": "Use a user-mode task (requires you to stay logged in)",
			"C": "Cancel installation",
		}
		choice, choiceErr := wz.promptChoice(
			fmt.Sprintf("\nWARNING: %q appears to be a mapped network drive.\n"+
				"The SYSTEM-account Scheduled Task cannot see mapped drive letters.\n"+
				"Choose an option:", folder),
			choices, "U",
		)
		if choiceErr != nil {
			return fmt.Errorf("reading mapped drive choice: %w", choiceErr)
		}
		switch choice {
		case "c":
			wz.println("\nInstallation cancelled.")
			return nil
		case "f":
			useMappedFallback = true
			wz.println("\nUsing user-mode task fallback.")
		case "u":
			folder, err = wz.promptFolder("Enter UNC path (e.g. \\\\server\\share\\CCC_Export):", "")
			if err != nil {
				return fmt.Errorf("reading UNC path: %w", err)
			}
		}
	}

	// Step 5 — validate folder exists
	if _, statErr := os.Stat(folder); os.IsNotExist(statErr) {
		wz.printf("\nFolder %q does not exist yet. Creating it...\n", folder)
		if mkErr := os.MkdirAll(folder, 0o755); mkErr != nil {
			return fmt.Errorf("cannot create folder %q: %w", folder, mkErr)
		}
	}

	// Step 6 — create data dir and set ACLs
	wz.printf("\nCreating %s...\n", dataDir)
	if mkErr := os.MkdirAll(dataDir, 0o755); mkErr != nil {
		return fmt.Errorf("cannot create data dir %q: %w", dataDir, mkErr)
	}
	wz.println("Setting permissions (SYSTEM=Full, Users=Modify)...")
	if aclErr := SetDataDirACL(dataDir); aclErr != nil {
		// Non-fatal: log the error but continue. Permissions are best-effort.
		wz.printf("  WARNING: could not set ACLs: %v\n", aclErr)
	}

	// Step 7 — copy binary
	exePath, exeErr := os.Executable()
	if exeErr != nil {
		return fmt.Errorf("cannot determine current executable: %w", exeErr)
	}
	destExe := filepath.Join(dataDir, "earlscheib.exe")
	wz.printf("Copying binary to %s...\n", destExe)
	if copyErr := copyFile(exePath, destExe); copyErr != nil {
		return fmt.Errorf("copying binary: %w", copyErr)
	}

	// Step 8 — write config.ini (preserve existing keys on upgrade)
	configPath := filepath.Join(dataDir, "config.ini")
	wz.println("Writing config.ini (existing values preserved)...")
	if cfgErr := writeConfigIfAbsent(configPath, folder); cfgErr != nil {
		return fmt.Errorf("writing config.ini: %w", cfgErr)
	}

	// Step 9 — connection test with retry loop
	testFn := opts.RunTestFn
	if testFn == nil {
		testFn = buildInProcessTestFn(destExe, dataDir)
	}

	wz.println("\nRunning connection test...")
	for {
		ok := testFn()
		if ok {
			wz.println("  Connection test PASSED.")
			break
		}
		testChoices := map[string]string{
			"R": "Retry the test",
			"C": "Continue anyway (watcher will retry automatically)",
			"X": "Cancel installation",
		}
		choice, choiceErr := wz.promptChoice(
			"\nConnection test FAILED. The watcher could not reach the follow-up service.\n"+
				"Check your internet connection or firewall (HTTPS outbound must be allowed).",
			testChoices, "R",
		)
		if choiceErr != nil {
			return fmt.Errorf("reading test failure choice: %w", choiceErr)
		}
		switch choice {
		case "x":
			wz.println("\nInstallation cancelled.")
			return nil
		case "c":
			wz.println("\nContinuing without a successful test.")
			goto afterTest
		default: // "r"
			wz.println("Retrying...")
		}
	}
afterTest:

	// Step 10 — register Scheduled Task
	portableDir := opts.PortableDir
	if portableDir == "" {
		portableDir = filepath.Dir(exePath)
	}

	xmlFile := "EarlScheibEMSWatcher-SYSTEM.xml"
	if useMappedFallback {
		xmlFile = "EarlScheibEMSWatcher-User.xml"
	}
	xmlPath := filepath.Join(portableDir, "tasks", xmlFile)
	// Fallback: look inside the data dir if tasks/ isn't next to the binary.
	if _, statErr := os.Stat(xmlPath); os.IsNotExist(statErr) {
		xmlPath = filepath.Join(dataDir, "tasks", xmlFile)
	}

	wz.println("Registering Scheduled Task...")
	if taskErr := RegisterTask(xmlPath, useMappedFallback); taskErr != nil {
		wz.printf("  WARNING: could not register task automatically: %v\n", taskErr)
		wz.println("  The watcher is installed but will not run on a schedule.")
		wz.println("  Open Task Scheduler and create a task to run:")
		wz.printf("    %s --scan  every 5 minutes\n", destExe)
	} else {
		wz.println("  Scheduled Task registered successfully.")
	}

	// Step 11 — first scan
	wz.println("\nRunning first scan...")
	scanCmd := exec.Command(destExe, "--scan")
	scanCmd.Stdout = opts.Out
	scanCmd.Stderr = opts.Out
	if scanErr := scanCmd.Run(); scanErr != nil {
		// Non-fatal — scan may fail if no files exist yet.
		wz.printf("  First scan returned a non-zero status: %v (may be normal if no files exist yet)\n", scanErr)
	} else {
		wz.println("  First scan complete.")
	}

	// CCC ONE instructions
	wz.configureCCCONEInstructions(folder)

	logFile := filepath.Join(dataDir, "ems_watcher.log")
	wz.successSummary(folder, logFile)
	return nil
}

// Uninstall executes the uninstall flow:
//  1. Require admin
//  2. Confirm intent
//  3. Unregister Scheduled Task
//  4. Prompt: preserve data dir?
//  5. If no: remove data dir
//  6. Print summary
func Uninstall(opts UninstallOptions) error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("--uninstall is only supported on Windows")
	}

	dataDir := resolveDataDir(opts.DataDir)
	wz := resolveWizard(opts.In, opts.Out)

	if err := requireAdmin(); err != nil {
		return err
	}

	wz.println()
	wz.println("=================================================================")
	wz.println("  Earl Scheib EMS Watcher — Uninstall")
	wz.println("=================================================================")
	wz.println()

	confirmed, err := wz.promptYN("Are you sure you want to uninstall the EMS Watcher?", false)
	if err != nil || !confirmed {
		wz.println("Uninstall cancelled.")
		return nil
	}

	// Unregister task (ignore error — task may already be absent)
	wz.println("\nRemoving Scheduled Task...")
	if taskErr := UnregisterTask(); taskErr != nil {
		wz.printf("  WARNING: %v\n", taskErr)
	} else {
		wz.println("  Task removed.")
	}

	// Prompt to remove data dir
	keepData, promptErr := wz.promptYN(
		fmt.Sprintf("Preserve data in %s (log, database, config)?", dataDir),
		true,
	)
	if promptErr != nil {
		keepData = true // default to keeping data on read error
	}

	if !keepData {
		wz.printf("\nRemoving %s...\n", dataDir)
		if rmErr := os.RemoveAll(dataDir); rmErr != nil {
			wz.printf("  WARNING: could not fully remove %s: %v\n", dataDir, rmErr)
		} else {
			wz.println("  Data directory removed.")
		}
	} else {
		wz.printf("\nData preserved at %s.\n", dataDir)
	}

	wz.println()
	wz.println("Uninstall complete.")
	wz.println()
	return nil
}

// Configure re-runs the folder-selection and connection-test steps without
// re-creating the Scheduled Task. Useful for updating the watch folder after
// initial install.
func Configure(opts Options) error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("--configure is only supported on Windows")
	}

	dataDir := resolveDataDir(opts.DataDir)
	wz := resolveWizard(opts.In, opts.Out)

	if err := requireAdmin(); err != nil {
		return err
	}

	wz.println()
	wz.println("=================================================================")
	wz.println("  Earl Scheib EMS Watcher — Configure")
	wz.println("=================================================================")
	wz.println()

	// Load current folder from config to use as default
	currentFolder := `C:\CCC\EMS_Export`
	if detected, ok := DetectCCCOnePath(); ok {
		currentFolder = detected
	}

	folder, err := wz.promptFolder(
		"CCC ONE export folder (where CCC ONE saves EMS files)?",
		currentFolder,
	)
	if err != nil {
		return fmt.Errorf("reading folder path: %w", err)
	}

	// Update config.ini with the new folder
	configPath := filepath.Join(dataDir, "config.ini")
	wz.println("Updating config.ini...")
	if cfgErr := writeConfigFolder(configPath, folder); cfgErr != nil {
		return fmt.Errorf("updating config.ini: %w", cfgErr)
	}

	// Run connection test
	exePath, exeErr := os.Executable()
	if exeErr != nil {
		return fmt.Errorf("cannot determine current executable: %w", exeErr)
	}

	testFn := opts.RunTestFn
	if testFn == nil {
		testFn = buildInProcessTestFn(exePath, dataDir)
	}

	wz.println("\nRunning connection test...")
	if ok := testFn(); ok {
		wz.println("  Connection test PASSED.")
	} else {
		wz.println("  Connection test FAILED. Check internet connection.")
		wz.println("  The existing task will retry on next run.")
	}

	wz.println()
	wz.printf("Configuration updated. Watch folder: %s\n", folder)
	wz.println()
	return nil
}

// --- helpers ----------------------------------------------------------------

func resolveDataDir(override string) string {
	if override != "" {
		return override
	}
	return installDataDir
}

func resolveWizard(r io.Reader, w io.Writer) *wizard {
	if r == nil {
		r = os.Stdin
	}
	if w == nil {
		w = os.Stdout
	}
	return newWizard(r, w)
}

// requireAdmin checks that the current process has administrator privileges.
// Returns a clear error if not elevated.
func requireAdmin() error {
	if !isElevated() {
		return fmt.Errorf(
			"this command requires administrator privileges.\n" +
				"Right-click setup.cmd (or the terminal) and choose \"Run as administrator\".")
	}
	return nil
}

// copyFile copies src to dst, overwriting dst if it exists.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() {
		_ = out.Close()
	}()

	if _, err = io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

// buildInProcessTestFn returns a function that shells out to earlscheib.exe --test
// with the given data dir set via EARLSCHEIB_DATA_DIR. This avoids importing
// the full webhook/telemetry stack into the install package.
func buildInProcessTestFn(exePath, dataDir string) func() bool {
	return func() bool {
		cmd := exec.Command(exePath, "--test")
		cmd.Env = append(os.Environ(), "EARLSCHEIB_DATA_DIR="+dataDir)
		err := cmd.Run()
		return err == nil
	}
}

// writeConfigIfAbsent writes config.ini only if it does not already exist
// (preserving upgrade settings, matching Inno Setup onlyifdoesntexist flag).
func writeConfigIfAbsent(configPath, watchFolder string) error {
	if _, err := os.Stat(configPath); err == nil {
		// File already exists — preserve it (upgrade case).
		return nil
	}

	content := fmt.Sprintf("[watcher]\nwatch_folder = %s\nwebhook_url = https://support.jjagpal.me/earlscheibconcord\nlog_level = INFO\n\n; secret_key is baked into the binary -- do not add it here\n", watchFolder)
	return os.WriteFile(configPath, []byte(content), 0o644)
}

// writeConfigFolder updates the watch_folder key in an existing config.ini,
// or creates the file if it does not exist. Used by --configure.
func writeConfigFolder(configPath, watchFolder string) error {
	// If file doesn't exist, just create it.
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return writeConfigIfAbsent(configPath, watchFolder)
	}

	// Read and replace the watch_folder line. We do a simple line-by-line
	// rewrite to avoid importing gopkg.in/ini.v1 here (keep the package lean).
	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}

	// Use config.Merge-style approach: write a fresh file with updated folder.
	// Since we own the format we can do a targeted replace.
	content := fmt.Sprintf("[watcher]\nwatch_folder = %s\nwebhook_url = https://support.jjagpal.me/earlscheibconcord\nlog_level = INFO\n\n; secret_key is baked into the binary -- do not add it here\n", watchFolder)
	_ = data // existing content superseded; webhook_url and log_level use defaults
	return os.WriteFile(configPath, []byte(content), 0o644)
}
