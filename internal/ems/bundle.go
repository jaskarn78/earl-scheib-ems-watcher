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
	Basename    string            // GUID basename, e.g. "AB12CDEF-1234-...-0000"
	AD1         map[string]string // V_OWNER_F, V_OWNER_L, V_OWNER_PH, V_OWNER_AD, V_OWNER_E
	VEH         map[string]string // V_VIN, V_YR, V_MAKE, V_MODEL
	ENV         map[string]string // E_DOC_NUM / E_RO / E_EST_NUM / E_DOC_ID / E_REF
	SourceFiles []string          // sorted ascending by lowercase(filepath.Base)
}
