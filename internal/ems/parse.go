package ems

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Valentin-Kaiser/go-dbase/dbase"
)

// Fields we extract per CCC ONE EMS 2.01 file kind. Missing fields are stored
// as "" without error — only missing files trigger a ParseBundle error.
var (
	ad1Fields = []string{"V_OWNER_F", "V_OWNER_L", "V_OWNER_PH", "V_OWNER_AD", "V_OWNER_E"}
	vehFields = []string{"V_VIN", "V_YR", "V_MAKE", "V_MODEL"}
	envFields = []string{"E_DOC_NUM", "E_RO", "E_EST_NUM", "E_DOC_ID", "E_REF"}
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

	// Short-circuit on an empty table — every field already defaults to "".
	if tbl.Header().RecordsCount() == 0 {
		return out, nil
	}

	row, err := tbl.Row()
	if err != nil {
		return nil, fmt.Errorf("read row 0 of %s: %w", filepath.Base(path), err)
	}

	for _, name := range fields {
		field := row.FieldByName(name)
		if field == nil {
			continue
		}
		v := field.GetValue()
		if v == nil {
			continue
		}
		out[name] = strings.TrimSpace(fmt.Sprint(v))
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
