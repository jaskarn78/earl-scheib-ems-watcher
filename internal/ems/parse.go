package ems

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Valentin-Kaiser/go-dbase/dbase"
)

// Fields we extract per CCC ONE EMS 2.01 file kind. Confirmed empirically
// from a live Earl Scheib bundle (AD1=117 cols, VEH=28 cols, ENV=25 cols).
// OWNR_* is the vehicle owner (our SMS target); INSD_* is the insured party
// and serves as a fallback when OWNR is blank (some shops duplicate, some
// populate only INSD). Missing fields are stored as "" without error —
// only missing files trigger a ParseBundle error.
var (
	ad1Fields = []string{
		// Owner (primary target for follow-up SMS)
		"OWNR_FN", "OWNR_LN", "OWNR_PH1", "OWNR_PH2", "OWNR_EA",
		"OWNR_ADDR1", "OWNR_ADDR2", "OWNR_CITY", "OWNR_ST", "OWNR_ZIP",
		// Insured (fallback when OWNR is blank)
		"INSD_FN", "INSD_LN", "INSD_PH1", "INSD_PH2", "INSD_EA",
	}
	vehFields = []string{"V_VIN", "V_MODEL_YR", "V_MAKEDESC", "V_MAKECODE", "V_MODEL", "V_COLOR"}
	envFields = []string{"UNQFILE_ID", "ESTFILE_ID", "RO_ID", "SUPP_NO", "TRANS_TYPE"}
)

// ParseBundle reads the dBase files in the bundle and returns a populated
// *Bundle. files keys are lowercased extension without dot ("ad1", "veh",
// "env", "lin", ...); values are absolute paths.
//
// Returns an error if files["ad1"] or files["veh"] is missing — those are the
// only two required files per the /estimate contract. Missing optional files
// (ENV, LIN, etc.) are tolerated: the corresponding map on Bundle is nil.
//
// Missing fields WITHIN a present file never cause an error; they are stored
// as "" in the returned map. This matches the plan's missing-field policy
// (known_dbase_fields block in PLAN.md) — field names like V_OWNER_E may not
// exist in Marco's export and their absence is not actionable.
func ParseBundle(basename string, files map[string]string) (*Bundle, error) {
	if _, ok := files["ad1"]; !ok {
		return nil, fmt.Errorf("ems: bundle %s missing required file: AD1", basename)
	}
	if _, ok := files["veh"]; !ok {
		return nil, fmt.Errorf("ems: bundle %s missing required file: VEH", basename)
	}

	b := &Bundle{Basename: basename}

	ad1, err := readFields(files["ad1"], ad1Fields)
	if err != nil {
		return nil, fmt.Errorf("ems: read AD1 for %s: %w", basename, err)
	}
	b.AD1 = ad1

	veh, err := readFields(files["veh"], vehFields)
	if err != nil {
		return nil, fmt.Errorf("ems: read VEH for %s: %w", basename, err)
	}
	b.VEH = veh

	if envPath, ok := files["env"]; ok {
		// ENV is optional; don't fail the whole bundle if it's unreadable,
		// just log-via-error-silence and continue with an empty ENV map. The
		// RenderBMS DocumentVerCode fallback to basename handles this cleanly.
		if env, err := readFields(envPath, envFields); err == nil {
			b.ENV = env
		}
	}

	b.SourceFiles = sortedBundlePaths(files)
	return b, nil
}

// readFields opens a dBase file read-only, reads row 0, and returns the named
// fields as a trimmed-string map. Unknown field names map to "".
//
// CCC ONE EMS 2.01 bundles are single-row dBase tables — we only need row 0.
// If the table is empty, every requested field returns "".
//
// Memo (type M) columns are SKIPPED during interpretation — CCC ONE writes
// the companion memo sidecar with a .DBT extension (dBase III/IV format)
// but go-dbase opens only .FPT (FoxPro). We don't need memo content for SMS
// follow-up (customer name, phone, VIN are all non-memo), so bypassing Row()
// and interpreting each non-memo column manually lets us read the row even
// when the memo sidecar file is missing or in the wrong format.
func readFields(path string, fields []string) (map[string]string, error) {
	tbl, err := dbase.OpenTable(&dbase.Config{
		Filename:   path,
		TrimSpaces: true,
		ReadOnly:   true,
		Untested:   true, // CCC ONE writes FoxBasePlus (0x03) which is "not tested" per go-dbase taxonomy
	})
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", filepath.Base(path), err)
	}
	defer tbl.Close()

	out := make(map[string]string, len(fields))
	for _, f := range fields {
		out[f] = "" // default — every requested field is present in the result
	}
	wanted := make(map[string]bool, len(fields))
	for _, f := range fields {
		wanted[f] = true
	}

	// Short-circuit on an empty table — every field already defaults to "".
	if tbl.Header().RecordsCount() == 0 {
		return out, nil
	}

	// Read raw row bytes directly, bypassing Row() → BytesToRow() which
	// eagerly resolves memo fields via the FPT sidecar (not present in the
	// CCC ONE bundle — they ship .DBT memo sidecars we don't need).
	raw, err := tbl.ReadRow(0)
	if err != nil {
		return nil, fmt.Errorf("read row 0 of %s: %w", filepath.Base(path), err)
	}
	if len(raw) < 1 {
		return out, nil
	}

	// Walk columns at their fixed offsets. First byte of the row is the
	// delete marker (0x20 active, 0x2A deleted); column data starts at 1.
	offset := uint16(1)
	for _, col := range tbl.Columns() {
		length := uint16(col.Length)
		// Bail early if the row is truncated — defensive against malformed files.
		if int(offset)+int(length) > len(raw) {
			break
		}

		// Skip memo columns — their raw bytes are an FPT address we'd have
		// to resolve via the (missing or wrong-format) memo sidecar. We
		// don't need memo content for the /estimate contract.
		if dbase.DataType(col.DataType) == dbase.Memo {
			offset += length
			continue
		}

		name := col.Name()
		if !wanted[name] {
			offset += length
			continue
		}

		cellBytes := raw[offset : offset+length]
		offset += length

		val, ierr := tbl.Interpret(cellBytes, col)
		if ierr != nil || val == nil {
			continue
		}
		out[name] = strings.TrimSpace(fmt.Sprint(val))
	}
	return out, nil
}

// sortedBundlePaths returns the absolute paths from files sorted ascending by
// lowercase(filepath.Base(path)). Shared ordering with bundleSHA256 in the
// scanner package guarantees that the file bytes hashed for dedup match the
// file order recorded in Bundle.SourceFiles — a single source of truth.
func sortedBundlePaths(files map[string]string) []string {
	out := make([]string, 0, len(files))
	for _, p := range files {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(filepath.Base(out[i])) < strings.ToLower(filepath.Base(out[j]))
	})
	return out
}
