package install

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

// wizard holds the I/O plumbing for console prompts.
// Using explicit reader/writer makes the wizard fully testable.
type wizard struct {
	in  *bufio.Reader
	out io.Writer
}

// newWizard creates a wizard that reads from r and writes to w.
// Pass os.Stdin and os.Stdout for production use.
func newWizard(r io.Reader, w io.Writer) *wizard {
	return &wizard{in: bufio.NewReader(r), out: w}
}

// println writes a line to the wizard's output.
func (wz *wizard) println(args ...interface{}) {
	fmt.Fprintln(wz.out, args...)
}

// printf writes a formatted string to the wizard's output.
func (wz *wizard) printf(format string, args ...interface{}) {
	fmt.Fprintf(wz.out, format, args...)
}

// readLine reads a single line (trimmed) from wizard input.
func (wz *wizard) readLine() (string, error) {
	line, err := wz.in.ReadString('\n')
	return strings.TrimSpace(line), err
}

// promptYN asks a yes/no question and returns true for Y/y/yes/empty-default-yes.
// If defaultYes is true, empty input returns true.
func (wz *wizard) promptYN(prompt string, defaultYes bool) (bool, error) {
	hint := "[Y/n]"
	if !defaultYes {
		hint = "[y/N]"
	}
	wz.printf("%s %s: ", prompt, hint)

	line, err := wz.readLine()
	if err != nil && line == "" {
		return defaultYes, err
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "", " ":
		return defaultYes, nil
	case "y", "yes":
		return true, nil
	case "n", "no":
		return false, nil
	default:
		return defaultYes, nil
	}
}

// promptFolder asks for a folder path, showing a default. Returns the chosen path.
// The loop retries if the entered path is empty (default is used in that case).
func (wz *wizard) promptFolder(prompt, defaultPath string) (string, error) {
	if defaultPath != "" {
		wz.printf("%s\n  [default: %s]: ", prompt, defaultPath)
	} else {
		wz.printf("%s: ", prompt)
	}

	line, err := wz.readLine()
	if err != nil && line == "" {
		return defaultPath, err
	}
	if line == "" {
		return defaultPath, nil
	}
	return line, nil
}

// promptChoice asks the user to enter one of the provided option keys (case-insensitive).
// Returns the lowercased choice. choices is a map of key -> description.
func (wz *wizard) promptChoice(prompt string, choices map[string]string, defaultKey string) (string, error) {
	wz.println(prompt)
	for k, desc := range choices {
		marker := ""
		if strings.EqualFold(k, defaultKey) {
			marker = " (default)"
		}
		wz.printf("  [%s] %s%s\n", strings.ToUpper(k), desc, marker)
	}
	wz.printf("Choice [%s]: ", strings.ToUpper(defaultKey))

	for {
		line, err := wz.readLine()
		if err != nil && line == "" {
			return strings.ToLower(defaultKey), err
		}
		line = strings.TrimSpace(strings.ToLower(line))
		if line == "" {
			return strings.ToLower(defaultKey), nil
		}
		if _, ok := choices[line]; ok {
			return line, nil
		}
		if _, ok := choices[strings.ToUpper(line)]; ok {
			return strings.ToLower(line), nil
		}
		wz.printf("Please enter one of: %s: ", validChoicesStr(choices))
	}
}

func validChoicesStr(choices map[string]string) string {
	keys := make([]string, 0, len(choices))
	for k := range choices {
		keys = append(keys, strings.ToUpper(k))
	}
	return strings.Join(keys, "/")
}

// welcomeBanner prints the install welcome banner.
func (wz *wizard) welcomeBanner() {
	wz.println()
	wz.println("=================================================================")
	wz.println("  Earl Scheib EMS Watcher — Setup")
	wz.println("=================================================================")
	wz.println()
	wz.println("This wizard will:")
	wz.println("  1. Detect (or confirm) your CCC ONE export folder")
	wz.println("  2. Test the connection to the follow-up service")
	wz.println("  3. Install the watcher to C:\\EarlScheibWatcher\\")
	wz.println("  4. Register a Windows Scheduled Task to run every 5 minutes")
	wz.println()
}

// configureCCCONEInstructions prints the CCC ONE configuration step.
func (wz *wizard) configureCCCONEInstructions(folder string) {
	wz.println()
	wz.println("-----------------------------------------------------------------")
	wz.println("  Configure CCC ONE")
	wz.println("-----------------------------------------------------------------")
	wz.println()
	wz.println("In CCC ONE, open: Tools > Extract > EMS Extract Preferences")
	wz.println()
	wz.println("Check BOTH of these boxes:")
	wz.println("   [x] Lock Estimate")
	wz.println("   [x] Save Workfile")
	wz.println()
	wz.printf("Set the \"Output Folder\" to: %s\n", folder)
	wz.println()
	wz.println("Click Save and close the preferences window.")
	wz.println()
}

// successSummary prints the post-install summary.
func (wz *wizard) successSummary(folder, logFile string) {
	wz.println()
	wz.println("=================================================================")
	wz.println("  Installation complete!")
	wz.println("=================================================================")
	wz.println()
	wz.printf("  Watch folder:  %s\n", folder)
	wz.println("  Program:       C:\\EarlScheibWatcher\\earlscheib.exe")
	wz.println("  Config:        C:\\EarlScheibWatcher\\config.ini")
	wz.printf("  Log file:      %s\n", logFile)
	wz.println("  Task:          Task Scheduler > EarlScheibEMSWatcher")
	wz.println()
	wz.println("The watcher will run every 5 minutes automatically.")
	wz.println()
	wz.println("To verify: open Task Scheduler, find EarlScheibEMSWatcher,")
	wz.println("check that Last Run Result shows 0x0 after the next run.")
	wz.println()
}

// defaultWizard creates a wizard using os.Stdin / os.Stdout.
func defaultWizard() *wizard {
	return newWizard(os.Stdin, os.Stdout)
}
