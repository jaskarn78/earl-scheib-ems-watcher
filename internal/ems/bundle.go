// Package ems parses CCC ONE EMS 2.01 dBase bundles (AD1 + VEH [+ ENV/LIN/...])
// and synthesizes BMS 2.6 XML payloads that the server-side /estimate handler
// (app.py parse_bms) already accepts.
//
// A "bundle" is the set of sibling files sharing a single GUID basename that
// CCC ONE writes into the EMS export folder. This package does NOT detect
// bundles on disk — that is the scanner's job (see internal/scanner/ems_bundle.go).
// This package converts an already-grouped set of file paths into a *Bundle
// value via ParseBundle, then renders it as bytes via RenderBMS.
package ems

// Bundle is a single CCC ONE EMS 2.01 dBase bundle keyed by the GUID basename.
//
// AD1/VEH/ENV are per-file field maps (field-name → trimmed string value).
// Missing files/fields yield empty strings (never panic) — ParseBundle returns
// an error ONLY when AD1 or VEH is entirely absent, since those two files
// carry the owner and vehicle data the /estimate handler requires.
//
// SourceFiles preserves the absolute paths of every component file in the
// bundle, sorted ascending by lowercase(filepath.Base(p)). This ordering is
// shared with bundleSHA256 in the scanner package so dedup hashing is
// deterministic across scans.
type Bundle struct {
	Basename string // GUID basename, e.g. "AB12CDEF-1234-...-0000"
	// AD1: OWNR_FN / OWNR_LN / OWNR_PH1 / OWNR_EA (email) / OWNR_ADDR1,
	//      with INSD_* fallback keys for shops that populate insured only.
	AD1 map[string]string
	// VEH: V_VIN, V_MODEL_YR (year), V_MAKEDESC (make), V_MODEL, V_COLOR —
	//      matches the field names parse.go extracts via go-dbase and the
	//      VehicleInfo emission in bms.go.
	VEH map[string]string
	// ENV: UNQFILE_ID / ESTFILE_ID / RO_ID / SUPP_NO / TRANS_TYPE —
	//      DocumentVerCode priority chain + RO tag for the admin UI.
	ENV         map[string]string
	SourceFiles []string // sorted ascending by lowercase(filepath.Base)
}
