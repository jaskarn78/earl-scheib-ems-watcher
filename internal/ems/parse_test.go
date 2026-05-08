package ems

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Valentin-Kaiser/go-dbase/dbase"
	"golang.org/x/text/encoding/charmap"
)

// columnSpec is a lightweight recipe for a test dBase column. Defaults to
// Character; pass a non-zero DataType (e.g. dbase.Date) for other column
// kinds. The DataType field is opt-in so existing fixture call sites that
// only build Character columns continue to compile unchanged.
type columnSpec struct {
	Name     string
	Length   uint8
	DataType dbase.DataType
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
// makeTestDBFTyped is the typed-value variant of makeTestDBF. It accepts a
// map[string]interface{} so Date columns can carry time.Time values directly
// (a zero time.Time{} round-trips as 8 spaces on disk — the dBase empty-date
// representation CCC ONE writes for unfilled date fields).
func makeTestDBFTyped(t *testing.T, dir, filename string, cols []columnSpec, row map[string]interface{}) string {
	t.Helper()
	return makeTestDBFInner(t, dir, filename, cols, row)
}

func makeTestDBF(t *testing.T, dir, filename string, cols []columnSpec, row map[string]string) string {
	t.Helper()
	r := make(map[string]interface{}, len(row))
	for k, v := range row {
		r[k] = v
	}
	return makeTestDBFInner(t, dir, filename, cols, r)
}

func makeTestDBFInner(t *testing.T, dir, filename string, cols []columnSpec, row map[string]interface{}) string {
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
	colTypes := make(map[string]dbase.DataType, len(cols))
	for _, c := range cols {
		dt := c.DataType
		if dt == 0 {
			dt = dbase.Character
		}
		col, err := dbase.NewColumn(c.Name, dt, c.Length, 0, false)
		if err != nil {
			t.Fatalf("NewColumn(%q): %v", c.Name, err)
		}
		built = append(built, col)
		colTypes[c.Name] = dt
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

	_ = colTypes // colTypes available for future per-column conversion if needed
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
		[]columnSpec{{Name: "OWNR_FN", Length: 20}, {Name: "OWNR_LN", Length: 20}, {Name: "OWNR_PH1", Length: 20}},
		map[string]string{"OWNR_FN": "Marco", "OWNR_LN": "Rossi", "OWNR_PH1": "555"},
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
		[]columnSpec{{Name: "OWNR_FN", Length: 20}, {Name: "OWNR_LN", Length: 20}, {Name: "OWNR_PH1", Length: 20}},
		map[string]string{"OWNR_FN": "Marco", "OWNR_LN": "Rossi", "OWNR_PH1": "(925) 555-0199"},
	)
	veh := makeTestDBF(t, dir, "G-123.VEH",
		[]columnSpec{{Name: "V_VIN", Length: 20}, {Name: "V_MODEL_YR", Length: 4}, {Name: "V_MAKEDESC", Length: 20}, {Name: "V_MODEL", Length: 20}},
		map[string]string{"V_VIN": "1HGCM82633A123456", "V_MODEL_YR": "2020", "V_MAKEDESC": "HONDA", "V_MODEL": "ACCORD"},
	)

	b, err := ParseBundle("G-123", map[string]string{"ad1": ad1, "veh": veh})
	if err != nil {
		t.Fatalf("ParseBundle: %v", err)
	}
	if b.Basename != "G-123" {
		t.Errorf("Basename=%q want %q", b.Basename, "G-123")
	}
	if b.AD1["OWNR_FN"] != "Marco" {
		t.Errorf("AD1[OWNR_FN]=%q want %q", b.AD1["OWNR_FN"], "Marco")
	}
	if b.AD1["OWNR_LN"] != "Rossi" {
		t.Errorf("AD1[OWNR_LN]=%q want %q", b.AD1["OWNR_LN"], "Rossi")
	}
	if b.AD1["OWNR_PH1"] != "(925) 555-0199" {
		t.Errorf("AD1[OWNR_PH1]=%q want %q", b.AD1["OWNR_PH1"], "(925) 555-0199")
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
		[]columnSpec{{Name: "OWNR_FN", Length: 20}, {Name: "OWNR_LN", Length: 20}, {Name: "OWNR_PH1", Length: 20}},
		map[string]string{"OWNR_FN": "Marco", "OWNR_LN": "", "OWNR_PH1": ""},
	)
	veh := makeTestDBF(t, dir, "G-1.VEH",
		[]columnSpec{{Name: "V_VIN", Length: 20}},
		map[string]string{"V_VIN": "VIN-001"},
	)
	b, err := ParseBundle("G-1", map[string]string{"ad1": ad1, "veh": veh})
	if err != nil {
		t.Fatalf("ParseBundle: %v", err)
	}
	if b.AD1["OWNR_FN"] != "Marco" {
		t.Errorf("trimmed OWNR_FN=%q want %q (trailing spaces should be stripped)",
			b.AD1["OWNR_FN"], "Marco")
	}
}

func Test_ParseBundle_MissingOptionalField_EmptyString(t *testing.T) {
	dir := t.TempDir()
	// AD1 intentionally does NOT include OWNR_EA — lookup must return "" without error.
	ad1 := makeTestDBF(t, dir, "G-2.AD1",
		[]columnSpec{{Name: "OWNR_FN", Length: 20}},
		map[string]string{"OWNR_FN": "Solo"},
	)
	veh := makeTestDBF(t, dir, "G-2.VEH",
		[]columnSpec{{Name: "V_VIN", Length: 20}},
		map[string]string{"V_VIN": "VIN-002"},
	)
	b, err := ParseBundle("G-2", map[string]string{"ad1": ad1, "veh": veh})
	if err != nil {
		t.Fatalf("ParseBundle: %v", err)
	}
	if b.AD1["OWNR_EA"] != "" {
		t.Errorf("missing field OWNR_EA=%q want empty string", b.AD1["OWNR_EA"])
	}
	if b.AD1["OWNR_LN"] != "" {
		t.Errorf("missing field OWNR_LN=%q want empty string", b.AD1["OWNR_LN"])
	}
}

func Test_ParseBundle_WithENV_DocNumExtracted(t *testing.T) {
	dir := t.TempDir()
	ad1 := makeTestDBF(t, dir, "G-3.AD1",
		[]columnSpec{{Name: "OWNR_FN", Length: 20}, {Name: "OWNR_LN", Length: 20}},
		map[string]string{"OWNR_FN": "Al", "OWNR_LN": "Pha"},
	)
	veh := makeTestDBF(t, dir, "G-3.VEH",
		[]columnSpec{{Name: "V_VIN", Length: 20}},
		map[string]string{"V_VIN": "VIN-003"},
	)
	env := makeTestDBF(t, dir, "G-3.ENV",
		[]columnSpec{{Name: "UNQFILE_ID", Length: 20}},
		map[string]string{"UNQFILE_ID": "EST-00042"},
	)
	b, err := ParseBundle("G-3", map[string]string{"ad1": ad1, "veh": veh, "env": env})
	if err != nil {
		t.Fatalf("ParseBundle: %v", err)
	}
	if b.ENV["UNQFILE_ID"] != "EST-00042" {
		t.Errorf("ENV[UNQFILE_ID]=%q want %q", b.ENV["UNQFILE_ID"], "EST-00042")
	}
}

func Test_ParseBundle_SourceFilesSorted(t *testing.T) {
	dir := t.TempDir()
	ad1 := makeTestDBF(t, dir, "g-9.AD1",
		[]columnSpec{{Name: "OWNR_FN", Length: 20}},
		map[string]string{"OWNR_FN": "z"},
	)
	veh := makeTestDBF(t, dir, "G-9.VEH",
		[]columnSpec{{Name: "V_VIN", Length: 20}},
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
		[]columnSpec{{Name: "V_VIN", Length: 20}},
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

// Test_ParseBundle_AD2_TTL_Optional confirms that when the files map has no
// "ad2" or "ttl" keys, ParseBundle returns a valid Bundle with nil AD2 and
// nil TTL and no error. This covers the "optional file" branch added in
// 260505-q2t without requiring real .ad2/.ttl dBase fixtures.
func Test_ParseBundle_AD2_TTL_Optional(t *testing.T) {
	dir := t.TempDir()
	ad1 := makeTestDBF(t, dir, "G-OPT.AD1",
		[]columnSpec{{Name: "OWNR_FN", Length: 20}, {Name: "OWNR_LN", Length: 20}},
		map[string]string{"OWNR_FN": "Opt", "OWNR_LN": "Test"},
	)
	veh := makeTestDBF(t, dir, "G-OPT.VEH",
		[]columnSpec{{Name: "V_VIN", Length: 20}},
		map[string]string{"V_VIN": "VIN-OPT"},
	)
	// Deliberately omit "ad2" and "ttl" keys — the optional-file branch must
	// tolerate their absence and leave AD2/TTL nil.
	b, err := ParseBundle("G-OPT", map[string]string{"ad1": ad1, "veh": veh})
	if err != nil {
		t.Fatalf("ParseBundle unexpectedly returned error: %v", err)
	}
	if b.AD2 != nil {
		t.Errorf("expected b.AD2 == nil when ad2 file absent, got %v", b.AD2)
	}
	if b.TTL != nil {
		t.Errorf("expected b.TTL == nil when ttl file absent, got %v", b.TTL)
	}
}

// Test_ParseBundle_EmptyDateColumn_StoresEmptyString is the parse-level
// regression for 260508-q9c. CCC ONE writes empty dBase Date columns as 8
// spaces. go-dbase's Interpret normalises that to time.Time{} (the zero
// value). Before the fix, fmt.Sprint(time.Time{}) yielded the 35-character
// string "0001-01-01 00:00:00 +0000 UTC" — non-empty — so downstream
// emptiness checks (pickDocumentStatus's `lookup(b.AD2,"DATE_OUT") != ""`)
// misclassified every fresh estimate as a closed RO and emitted "C".
//
// Contract pinned by this test: an empty Date column round-trips through
// ParseBundle as "" in the AD2 map, and pickDocumentStatus consequently
// returns "E" (not "C") even when TTL.G_TTL_AMT > 0.
func Test_ParseBundle_EmptyDateColumn_StoresEmptyString(t *testing.T) {
	dir := t.TempDir()
	ad1 := makeTestDBF(t, dir, "G-Q9C.AD1",
		[]columnSpec{{Name: "OWNR_FN", Length: 20}, {Name: "OWNR_LN", Length: 20}},
		map[string]string{"OWNR_FN": "Russell", "OWNR_LN": "Rosete"},
	)
	veh := makeTestDBF(t, dir, "G-Q9C.VEH",
		[]columnSpec{{Name: "V_VIN", Length: 20}},
		map[string]string{"V_VIN": "VIN-Q9C"},
	)
	// AD2 with a Date column whose row value is the zero time.Time{}. On disk,
	// go-dbase serialises that as 8 spaces — exactly what CCC ONE writes for
	// an unfilled DATE_OUT field on a fresh estimate.
	ad2 := makeTestDBFTyped(t, dir, "G-Q9C.AD2",
		[]columnSpec{{Name: "DATE_OUT", Length: 8, DataType: dbase.Date}},
		map[string]interface{}{"DATE_OUT": time.Time{}},
	)
	ttl := makeTestDBF(t, dir, "G-Q9C.TTL",
		[]columnSpec{{Name: "G_TTL_AMT", Length: 16}},
		map[string]string{"G_TTL_AMT": "1500.00"},
	)

	b, err := ParseBundle("G-Q9C", map[string]string{
		"ad1": ad1, "veh": veh, "ad2": ad2, "ttl": ttl,
	})
	if err != nil {
		t.Fatalf("ParseBundle: %v", err)
	}
	if got := b.AD2["DATE_OUT"]; got != "" {
		t.Errorf("AD2[DATE_OUT]=%q want \"\" (empty Date column must NOT serialize as zero-time string)", got)
	}
	// Belt-and-suspenders: prove the fix closes the loop with bms.go. With
	// DATE_OUT empty, pickDocumentStatus must return "E" regardless of TTL.
	if got := pickDocumentStatus(b); got != "E" {
		t.Errorf("pickDocumentStatus=%q want \"E\" (empty DATE_OUT must NOT trigger closed-RO override)", got)
	}
}

// Test_ParseBundle_PopulatedDateColumn_NonEmpty is the companion positive-path
// case: a populated date round-trips as a non-empty string. We don't pin a
// specific format here — downstream code only checks emptiness — but verify
// the value is non-empty so a future regression that swallows ALL date columns
// would fail loudly.
func Test_ParseBundle_PopulatedDateColumn_NonEmpty(t *testing.T) {
	dir := t.TempDir()
	ad1 := makeTestDBF(t, dir, "G-Q9D.AD1",
		[]columnSpec{{Name: "OWNR_FN", Length: 20}},
		map[string]string{"OWNR_FN": "Pop"},
	)
	veh := makeTestDBF(t, dir, "G-Q9D.VEH",
		[]columnSpec{{Name: "V_VIN", Length: 20}},
		map[string]string{"V_VIN": "VIN-Q9D"},
	)
	ad2 := makeTestDBFTyped(t, dir, "G-Q9D.AD2",
		[]columnSpec{{Name: "DATE_OUT", Length: 8, DataType: dbase.Date}},
		map[string]interface{}{"DATE_OUT": time.Date(2026, 5, 8, 0, 0, 0, 0, time.UTC)},
	)
	b, err := ParseBundle("G-Q9D", map[string]string{
		"ad1": ad1, "veh": veh, "ad2": ad2,
	})
	if err != nil {
		t.Fatalf("ParseBundle: %v", err)
	}
	if got := b.AD2["DATE_OUT"]; got == "" {
		t.Errorf("AD2[DATE_OUT] for populated 2026-05-08 = \"\"; expected non-empty")
	}
}
