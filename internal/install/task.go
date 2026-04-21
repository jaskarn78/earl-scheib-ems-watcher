package install

import (
	"fmt"
	"os/exec"
)

const taskName = "EarlScheibEMSWatcher"

// RegisterTask creates a Windows Scheduled Task from the given XML file.
//
// It first tries to register using the SYSTEM account (so the task runs
// without a logged-on user). If SYSTEM registration fails (e.g., the
// folder is on a mapped drive) it falls back to the interactive user
// account, matching the Inno Setup Pascal RegisterScheduledTask() behaviour.
//
// useFallback=true skips the SYSTEM attempt and goes straight to user-mode.
func RegisterTask(xmlPath string, useFallback bool) error {
	// Delete any existing task first (ignore failure — task may not exist yet)
	_ = exec.Command("schtasks", "/Delete", "/TN", taskName, "/F").Run()

	if !useFallback {
		// Attempt SYSTEM account registration
		cmd := exec.Command(
			"schtasks", "/Create",
			"/XML", xmlPath,
			"/TN", taskName,
			"/F",
			"/RU", "SYSTEM",
		)
		if out, err := cmd.CombinedOutput(); err == nil {
			return nil
		} else {
			// Log the failure and fall through to user-mode
			_ = out // consumed below via fallback
		}
	}

	// User-mode task — requires user to be logged on, but can see mapped drives.
	// /IT = only run when user is logged in (InteractiveToken).
	cmd := exec.Command(
		"schtasks", "/Create",
		"/XML", xmlPath,
		"/TN", taskName,
		"/F",
		"/IT",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("schtasks create failed: %w\noutput: %s", err, string(out))
	}
	return nil
}

// UnregisterTask removes the Scheduled Task by name.
func UnregisterTask() error {
	cmd := exec.Command("schtasks", "/Delete", "/TN", taskName, "/F")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("schtasks delete failed: %w\noutput: %s", err, string(out))
	}
	return nil
}
