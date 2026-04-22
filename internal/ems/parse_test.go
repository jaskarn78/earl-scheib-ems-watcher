package ems

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Valentin-Kaiser/go-dbase/dbase"
	"golang.org/x/text/encoding/charmap"
)

// columnSpec is a lightweight recipe for a test dBase column. Only Character
// columns are used by the EMS 2.01 fixtures we need, so we hard-code the type
// and let the caller pick the length.
type columnSpec struct {
	Name   string
	Length uint8
}

// makeTestDBF writes a single-row dBase table and returns the final absolute
// path (dir/filename). Uses FoxBasePlus (0x03 — plain dBase III) with
// Untested=true, matching the on-wire format CCC ONE EMS 2.01 writes.
//
// go-dbase v1.12.10 NewTable has two quirks we work around:
//  1. It uppercases the ENTIRE filename path in Create (io_unix.go line 150, 165),
//     including directories. So we write into a working tempdir, then move.
//  2. It requires the extension to be ".DBF" (case-insensitive after uppercase).
//
// Strategy: create the table as a side-tempdir ".DBF", then os.Rename to the
// final dir/filename the caller asked for. OpenTable does NOT re-validate ext.
func makeTestDBF(t *testing.T, dir, filename string, cols []columnSpec, row map[string]string) string {
	t.Helper()
	finalPath := filepath.Join(dir, filename)

	// The library uppercases the FULL filename path on Create. On Linux
	// /tmp becomes /TMP which doesn't exist. Workaround: create the file
	// with a bare (no-path) uppercase filename under a chdir into an
	// uppercase-safe workdir. We create the workdir explicitly with an
	// all-uppercase name under t.TempDir() — t.TempDir's parent is /tmp,
	// so the path when uppercased becomes /TMP/.../WORK which still fails.
	// Therefore we chdir into the workdir and pass Filename="FIXTURE.DBF".
	baseWork := filepath.Join(t.TempDir(), "WORK")
	if err := os.MkdirAll(baseWork, 0o755); err != nil {
		t.Fatalf("mkdir workdir: %v", err)
	}
	// t.Chdir (Go 1.24+) restores the original cwd after the test; our go.mod
	// requires 1.25.0 so this is available.
	t.Chdir(baseWork)
	workName := "FIXTURE.DBF"
	workPath := filepath.Join(baseWork, workName)

	built := make([]*dbase.Column, 0, len(cols))
	for _, c := range cols {
		col, err := dbase.NewColumn(c.Name, dbase.Character, c.Length, 0, false)
		if err != nil {
			t.Fatalf("NewColumn(%q): %v", c.Name, err)
		}
		built = append(built, col)
	}

	tbl, err := dbase.NewTable(
		dbase.FoxBasePlus,
		&dbase.Config{
			// Use bare uppercase filename (no path prefix) so Create's
			// ToUpper pass doesn't corrupt an absolute path. After close
			// we resolve the actual file via readdir on baseWork.
			Filename:   workName,
			Converter:  dbase.NewDefaultConverter(charmap.Windows1252),
			TrimSpaces: true,
			Untested:   true,
		},
		built,
		0,
		nil,
	)
	if err != nil {
		t.Fatalf("NewTable: %v", err)
	}

	m := make(map[string]interface{}, len(row))
	for k, v := range row {
		m[k] = v
	}
	r, err := tbl.RowFromMap(m)
	if err != nil {
		t.Fatalf("RowFromMap: %v", err)
	}
	if err := r.Add(); err != nil {
		t.Fatalf("row.Add: %v", err)
	}
	if err := tbl.Close(); err != nil {
		t.Fatalf("tbl.Close: %v", err)
	}

	// The library wrote to baseWork/FIXTURE.DBF (relative path honors cwd).
	actualPath := workPath
	if _, err := os.Stat(actualPath); err != nil {
		// Try any file that appeared in baseWork.
		entries, _ := os.ReadDir(baseWork)
		found := ""
		for _, e := range entries {
			if !e.IsDir() {
				found = filepath.Join(baseWork, e.Name())
				break
			}
		}
		if found == "" {
			t.Fatalf("fixture not found in %s: %v", baseWork, err)
		}
		actualPath = found
	}

	// Read bytes and write to final destination (avoid cross-device rename).
	data, err := os.ReadFile(actualPath)
	if err != nil {
		t.Fatalf("read fixture %s: %v", actualPath, err)
	}
	if err := os.WriteFile(finalPath, data, 0o644); err != nil {
		t.Fatalf("write fixture to %s: %v", finalPath, err)
	}
	return finalPath
}

func Test_ParseBundle_AD1_Only_Errors(t *testing.T) {
	dir := t.TempDir()
	ad1 := makeTestDBF(t, dir, "G-123.AD1",
		[]columnSpec{{"V_OWNER_F", 20}, {"V_OWNER_L", 20}, {"V_OWNER_PH", 20}},
		map[string]string{"V_OWNER_F": "Marco", "V_OWNER_L": "Rossi", "V_OWNER_PH": "555"},
	)
	files := map[string]string{"ad1": ad1}
	_, err := ParseBundle("G-123", files)
	if err == nil {
		t.Fatal("expected error on missing VEH, got nil")
	}
	if !strings.Contains(err.Error(), "VEH") {
		t.Fatalf("expected error message to mention VEH; got %q", err.Error())
	}
}

func Test_ParseBundle_AD1_Plus_VEH_OK(t *testing.T) {
	dir := t.TempDir()
	ad1 := makeTestDBF(t, dir, "G-123.AD1",
		[]columnSpec{{"V_OWNER_F", 20}, {"V_OWNER_L", 20}, {"V_OWNER_PH", 20}},
		map[string]string{"V_OWNER_F": "Marco", "V_OWNER_L": "Rossi", "V_OWNER_PH": "(925) 555-0199"},
	)
	veh := makeTestDBF(t, dir, "G-123.VEH",
		[]columnSpec{{"V_VIN", 20}, {"V_YR", 4}, {"V_MAKE", 20}, {"V_MODEL", 20}},
		map[string]string{"V_VIN": "1HGCM82633A123456", "V_YR": "2020", "V_MAKE": "HONDA", "V_MODEL": "ACCORD"},
	)

	b, err := ParseBundle("G-123", map[string]string{"ad1": ad1, "veh": veh})
	if err != nil {
		t.Fatalf("ParseBundle: %v", err)
	}
	if b.Basename != "G-123" {
		t.Errorf("Basename=%q want %q", b.Basename, "G-123")
	}
	if b.AD1["V_OWNER_F"] != "Marco" {
		t.Errorf("AD1[V_OWNER_F]=%q want %q", b.AD1["V_OWNER_F"], "Marco")
	}
	if b.AD1["V_OWNER_L"] != "Rossi" {
		t.Errorf("AD1[V_OWNER_L]=%q want %q", b.AD1["V_OWNER_L"], "Rossi")
	}
	if b.AD1["V_OWNER_PH"] != "(925) 555-0199" {
		t.Errorf("AD1[V_OWNER_PH]=%q want %q", b.AD1["V_OWNER_PH"], "(925) 555-0199")
	}
	if b.VEH["V_VIN"] != "1HGCM82633A123456" {
		t.Errorf("VEH[V_VIN]=%q want %q", b.VEH["V_VIN"], "1HGCM82633A123456")
	}
}

func Test_ParseBundle_TrailingSpaces_Stripped(t *testing.T) {
	dir := t.TempDir()
	// Write value "Marco" (5 chars) into a 20-char field — dBase pads to 20 with spaces.
	// With TrimSpaces=true in the parser's OpenTable config, we should get "Marco".
	ad1 := makeTestDBF(t, dir, "G-1.AD1",
		[]columnSpec{{"V_OWNER_F", 20}, {"V_OWNER_L", 20}, {"V_OWNER_PH", 20}},
		map[string]string{"V_OWNER_F": "Marco", "V_OWNER_L": "", "V_OWNER_PH": ""},
	)
	veh := makeTestDBF(t, dir, "G-1.VEH",
		[]columnSpec{{"V_VIN", 20}},
		map[string]string{"V_VIN": "VIN-001"},
	)
	b, err := ParseBundle("G-1", map[string]string{"ad1": ad1, "veh": veh})
	if err != nil {
		t.Fatalf("ParseBundle: %v", err)
	}
	if b.AD1["V_OWNER_F"] != "Marco" {
		t.Errorf("trimmed V_OWNER_F=%q want %q (trailing spaces should be stripped)",
			b.AD1["V_OWNER_F"], "Marco")
	}
}

func Test_ParseBundle_MissingOptionalField_EmptyString(t *testing.T) {
	dir := t.TempDir()
	// AD1 intentionally does NOT include V_OWNER_E — lookup must return "" without error.
	ad1 := makeTestDBF(t, dir, "G-2.AD1",
		[]columnSpec{{"V_OWNER_F", 20}},
		map[string]string{"V_OWNER_F": "Solo"},
	)
	veh := makeTestDBF(t, dir, "G-2.VEH",
		[]columnSpec{{"V_VIN", 20}},
		map[string]string{"V_VIN": "VIN-002"},
	)
	b, err := ParseBundle("G-2", map[string]string{"ad1": ad1, "veh": veh})
	if err != nil {
		t.Fatalf("ParseBundle: %v", err)
	}
	if b.AD1["V_OWNER_E"] != "" {
		t.Errorf("missing field V_OWNER_E=%q want empty string", b.AD1["V_OWNER_E"])
	}
	if b.AD1["V_OWNER_L"] != "" {
		t.Errorf("missing field V_OWNER_L=%q want empty string", b.AD1["V_OWNER_L"])
	}
}

func Test_ParseBundle_WithENV_DocNumExtracted(t *testing.T) {
	dir := t.TempDir()
	ad1 := makeTestDBF(t, dir, "G-3.AD1",
		[]columnSpec{{"V_OWNER_F", 20}, {"V_OWNER_L", 20}},
		map[string]string{"V_OWNER_F": "Al", "V_OWNER_L": "Pha"},
	)
	veh := makeTestDBF(t, dir, "G-3.VEH",
		[]columnSpec{{"V_VIN", 20}},
		map[string]string{"V_VIN": "VIN-003"},
	)
	env := makeTestDBF(t, dir, "G-3.ENV",
		[]columnSpec{{"E_DOC_NUM", 20}},
		map[string]string{"E_DOC_NUM": "EST-00042"},
	)
	b, err := ParseBundle("G-3", map[string]string{"ad1": ad1, "veh": veh, "env": env})
	if err != nil {
		t.Fatalf("ParseBundle: %v", err)
	}
	if b.ENV["E_DOC_NUM"] != "EST-00042" {
		t.Errorf("ENV[E_DOC_NUM]=%q want %q", b.ENV["E_DOC_NUM"], "EST-00042")
	}
}

func Test_ParseBundle_SourceFilesSorted(t *testing.T) {
	dir := t.TempDir()
	ad1 := makeTestDBF(t, dir, "g-9.AD1",
		[]columnSpec{{"V_OWNER_F", 20}},
		map[string]string{"V_OWNER_F": "z"},
	)
	veh := makeTestDBF(t, dir, "G-9.VEH",
		[]columnSpec{{"V_VIN", 20}},
		map[string]string{"V_VIN": "v"},
	)
	b, err := ParseBundle("G-9", map[string]string{"veh": veh, "ad1": ad1})
	if err != nil {
		t.Fatalf("ParseBundle: %v", err)
	}
	if len(b.SourceFiles) != 2 {
		t.Fatalf("SourceFiles len=%d want 2", len(b.SourceFiles))
	}
	// Sorted ascending by lowercase basename: "g-9.ad1" < "g-9.veh".
	if strings.ToLower(filepath.Base(b.SourceFiles[0])) != "g-9.ad1" {
		t.Errorf("first SourceFile=%q want g-9.ad1", filepath.Base(b.SourceFiles[0]))
	}
	if strings.ToLower(filepath.Base(b.SourceFiles[1])) != "g-9.veh" {
		t.Errorf("second SourceFile=%q want g-9.veh", filepath.Base(b.SourceFiles[1]))
	}
}

func Test_ParseBundle_MissingAD1_Errors(t *testing.T) {
	dir := t.TempDir()
	veh := makeTestDBF(t, dir, "G-X.VEH",
		[]columnSpec{{"V_VIN", 20}},
		map[string]string{"V_VIN": "v"},
	)
	_, err := ParseBundle("G-X", map[string]string{"veh": veh})
	if err == nil {
		t.Fatal("expected error on missing AD1, got nil")
	}
	if !strings.Contains(err.Error(), "AD1") {
		t.Fatalf("expected error to mention AD1; got %q", err.Error())
	}
}
