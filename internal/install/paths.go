// Package install contains the console-based install, uninstall, and configure
// orchestrators for the portable-zip distribution path.
package install

import (
	"os"
	"strings"
)

// cccOneCandidates are the standard CCC ONE export folder locations, in priority
// order. Matches the candidate list in the Inno Setup DetectCCCOnePath() Pascal function.
// PartsTrader\Export is the actual path CCC ONE uses on Marco's shop PC (observed 2026-04-21).
var cccOneCandidates = []string{
	`C:\CCC APPS\CCCONE\CCCONE\DATA\PartsTrader\Export`,
	`C:\CCC\APPS\CCCONE\CCCONE\DATA\PartsTrader\Export`,
	`C:\CCC APPS\CCCONE\CCCONE\DATA`,
	`C:\CCC\APPS\CCCONE\CCCONE\DATA`,
	`C:\CCC\APPS\CCCCONE\CCCCONE\DATA`,
	`C:\CCC\EMS_Export`,
	`C:\Program Files\CCC`,
	`C:\Program Files (x86)\CCC`,
}

// DetectCCCOnePath returns the first candidate directory that exists.
// Returns ("", false) if none are found.
func DetectCCCOnePath() (string, bool) {
	for _, candidate := range cccOneCandidates {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate, true
		}
	}
	return "", false
}

// IsMappedDrive returns true if path appears to be on a mapped network drive
// (a single drive letter that is not C:). UNC paths (\\server\share) return false.
// Local drive C:\ returns false.
//
// This is a heuristic: SYSTEM-account Scheduled Tasks cannot access mapped drive
// letters, so callers should warn and offer fallback to user-mode task.
func IsMappedDrive(path string) bool {
	if len(path) < 2 {
		return false
	}
	// UNC path — not a mapped drive letter
	if strings.HasPrefix(path, `\\`) || strings.HasPrefix(path, `//`) {
		return false
	}
	first := strings.ToUpper(string(path[0]))
	if path[1] != ':' {
		return false
	}
	// Single letter followed by ':' — is it a non-C local letter?
	if first >= "A" && first <= "Z" && first != "C" {
		return true
	}
	return false
}
