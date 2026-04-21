package install

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// DetectCCCOnePath / IsMappedDrive
// ---------------------------------------------------------------------------

func TestIsMappedDrive(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{`C:\CCC\EMS_Export`, false},  // C: is local
		{`D:\CCC\EMS_Export`, true},   // D: treated as potentially mapped
		{`Z:\Share\CCC`, true},        // Z: classic mapped letter
		{`\\server\share\CCC`, false}, // UNC — not a drive letter
		{`//server/share/CCC`, false}, // UNC forward-slash variant
		{`C:\`, false},                // root of C:
		{``, false},                   // empty
		{`relative\path`, false},      // no drive letter
		{`A:\`, true},                 // A: non-C letter
	}

	for _, tc := range tests {
		got := IsMappedDrive(tc.path)
		if got != tc.want {
			t.Errorf("IsMappedDrive(%q) = %v; want %v", tc.path, got, tc.want)
		}
	}
}

func TestDetectCCCOnePath_noCandidatesExist(t *testing.T) {
	// On Linux (CI) none of the Windows paths exist — should return ("", false).
	path, found := DetectCCCOnePath()
	if found {
		t.Errorf("expected not found on non-Windows, got path=%q found=%v", path, found)
	}
}

// ---------------------------------------------------------------------------
// wizard — promptYN
// ---------------------------------------------------------------------------

func TestWizardPromptYN_defaultYes(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"", true},      // empty → default yes
		{"y", true},
		{"Y", true},
		{"yes", true},
		{"n", false},
		{"N", false},
		{"no", false},
		{"maybe", true}, // unrecognised → default yes
	}

	for _, tc := range tests {
		r := strings.NewReader(tc.input + "\n")
		w := &bytes.Buffer{}
		wz := newWizard(r, w)
		got, _ := wz.promptYN("Test?", true)
		if got != tc.want {
			t.Errorf("promptYN(%q, defaultYes=true) = %v; want %v", tc.input, got, tc.want)
		}
	}
}

func TestWizardPromptYN_defaultNo(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"", false},      // empty → default no
		{"y", true},
		{"n", false},
		{"maybe", false}, // unrecognised → default no
	}

	for _, tc := range tests {
		r := strings.NewReader(tc.input + "\n")
		w := &bytes.Buffer{}
		wz := newWizard(r, w)
		got, _ := wz.promptYN("Test?", false)
		if got != tc.want {
			t.Errorf("promptYN(%q, defaultYes=false) = %v; want %v", tc.input, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// wizard — promptFolder
// ---------------------------------------------------------------------------

func TestWizardPromptFolder_usesDefault(t *testing.T) {
	r := strings.NewReader("\n")
	w := &bytes.Buffer{}
	wz := newWizard(r, w)
	got, _ := wz.promptFolder("Folder?", `C:\CCC\EMS_Export`)
	if got != `C:\CCC\EMS_Export` {
		t.Errorf("expected default path, got %q", got)
	}
}

func TestWizardPromptFolder_userOverride(t *testing.T) {
	userPath := `D:\Custom\Path`
	r := strings.NewReader(userPath + "\n")
	w := &bytes.Buffer{}
	wz := newWizard(r, w)
	got, _ := wz.promptFolder("Folder?", `C:\CCC\EMS_Export`)
	if got != userPath {
		t.Errorf("expected user path %q, got %q", userPath, got)
	}
}

// ---------------------------------------------------------------------------
// wizard — promptChoice
// ---------------------------------------------------------------------------

func TestWizardPromptChoice_defaultKey(t *testing.T) {
	r := strings.NewReader("\n")
	w := &bytes.Buffer{}
	wz := newWizard(r, w)
	choices := map[string]string{
		"U": "Use UNC path",
		"F": "User-mode fallback",
		"C": "Cancel",
	}
	got, _ := wz.promptChoice("Choose:", choices, "U")
	if got != "u" {
		t.Errorf("expected 'u', got %q", got)
	}
}

func TestWizardPromptChoice_explicitChoice(t *testing.T) {
	r := strings.NewReader("F\n")
	w := &bytes.Buffer{}
	wz := newWizard(r, w)
	choices := map[string]string{
		"U": "Use UNC path",
		"F": "User-mode fallback",
		"C": "Cancel",
	}
	got, _ := wz.promptChoice("Choose:", choices, "U")
	if got != "f" {
		t.Errorf("expected 'f', got %q", got)
	}
}

// ---------------------------------------------------------------------------
// wizard — output content
// ---------------------------------------------------------------------------

func TestWizardWelcomeBanner(t *testing.T) {
	w := &bytes.Buffer{}
	wz := newWizard(strings.NewReader(""), w)
	wz.welcomeBanner()
	out := w.String()
	if !strings.Contains(out, "Earl Scheib EMS Watcher") {
		t.Errorf("welcome banner missing expected text, got: %q", out)
	}
}

func TestWizardSuccessSummary(t *testing.T) {
	w := &bytes.Buffer{}
	wz := newWizard(strings.NewReader(""), w)
	wz.successSummary(`C:\CCC\EMS_Export`, `C:\EarlScheibWatcher\ems_watcher.log`)
	out := w.String()
	if !strings.Contains(out, "Installation complete") {
		t.Errorf("success summary missing expected text, got: %q", out)
	}
	if !strings.Contains(out, `C:\CCC\EMS_Export`) {
		t.Errorf("success summary missing folder path, got: %q", out)
	}
}

// ---------------------------------------------------------------------------
// writeConfigIfAbsent — behaviour tests (using temp dir)
// ---------------------------------------------------------------------------

func TestWriteConfigIfAbsent_createsWhenMissing(t *testing.T) {
	tmp := t.TempDir()
	configPath := tmp + "/config.ini"
	err := writeConfigIfAbsent(configPath, `C:\CCC\EMS_Export`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("file not created: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "[watcher]") {
		t.Errorf("config.ini missing [watcher] section, got:\n%s", content)
	}
	if !strings.Contains(content, `C:\CCC\EMS_Export`) {
		t.Errorf("config.ini missing watch_folder, got:\n%s", content)
	}
}

func TestWriteConfigIfAbsent_preservesExisting(t *testing.T) {
	tmp := t.TempDir()
	configPath := tmp + "/config.ini"
	original := "[watcher]\nwatch_folder = Z:\\Custom\\Path\n"
	if err := os.WriteFile(configPath, []byte(original), 0o644); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	// Should NOT overwrite existing file
	err := writeConfigIfAbsent(configPath, `C:\CCC\EMS_Export`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, _ := os.ReadFile(configPath)
	if !strings.Contains(string(data), `Z:\Custom\Path`) {
		t.Errorf("existing config was overwritten, got:\n%s", string(data))
	}
}

func TestWriteConfigFolder_createsWhenMissing(t *testing.T) {
	tmp := t.TempDir()
	configPath := tmp + "/config.ini"
	err := writeConfigFolder(configPath, `C:\CCC\NEW_PATH`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("file not created: %v", err)
	}
	if !strings.Contains(string(data), `C:\CCC\NEW_PATH`) {
		t.Errorf("config missing new path, got:\n%s", string(data))
	}
}
