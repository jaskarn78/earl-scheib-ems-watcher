package ems

import (
	"bytes"
	"encoding/xml"
)

// BMS namespace. Must match app.py's BMS_NS byte-for-byte — ElementTree does
// namespace-aware path lookups, so a mismatched URI silently yields empty
// results from parse_bms (server 400 "invalid BMS payload").
const bmsNamespace = "http://www.cieca.com/BMS"

// Default document status when the ENV.TRANS_TYPE field is missing or empty.
// app.py branches on this: ESTIMATE_STATUSES={E,EM,EL,EP} triggers 24h+3day
// jobs, CLOSED_STATUSES={I,C,F,FI,FC,WC} triggers review. "E" is the CIECA
// code for a plain estimate transmission — the default for any non-empty
// EMS bundle. When ENV.TRANS_TYPE carries a different code we forward it
// verbatim (e.g. "EM" supplement, "I" invoiced) so jobs route correctly.
const documentStatusEstimateDefault = "E"

// bmsDoc is the tree we marshal. Element names are chosen to match the XPath
// queries in app.py parse_bms() — specifically the `.//bms:TAG` lookups which
// do a full-tree descendant search, so nesting depth doesn't matter as long
// as each tag appears exactly once with the right local name.
type bmsDoc struct {
	XMLName xml.Name `xml:"BMSEnvelope"`
	Xmlns   string   `xml:"xmlns,attr"`
	Group   bmsGroup `xml:"TransactionGroup"`
}

type bmsGroup struct {
	Trans bmsTrans `xml:"BMSTrans"`
}

type bmsTrans struct {
	DocumentInfo  bmsDocumentInfo  `xml:"DocumentInfo"`
	EventInfo     bmsEventInfo     `xml:"EventInfo"`
	// VehicleInfo (OH4-01) carries VIN / Year / Make / Model / ROId so the
	// parallel Python parser in app.py can synthesize vehicle_desc for the
	// admin UI. omitempty on each field keeps blanks out of the wire payload.
	VehicleInfo   bmsVehicleInfo   `xml:"VehicleInfo"`
	EstimateAddRq bmsEstimateAddRq `xml:"VehicleDamageEstimateAddRq"`
}

type bmsDocumentInfo struct {
	DocumentVerCode string `xml:"DocumentVerCode"`
	DocumentStatus  string `xml:"DocumentStatus"`
}

type bmsEventInfo struct {
	RepairEvent bmsRepairEvent `xml:"RepairEvent"`
}

type bmsRepairEvent struct {
	// CloseDateTime is always emitted (even if empty) so the XPath
	// `.//bms:EventInfo/bms:RepairEvent/bms:CloseDateTime` lookup always finds
	// a node. Empty string is parseable and yields empty close_dt on the
	// Python side — acceptable per app.py.
	CloseDateTime string `xml:"CloseDateTime"`
}

// bmsVehicleInfo groups the vehicle attributes Python's parse_bms pulls via
// .//bms:VIN, .//bms:Year, .//bms:Make, .//bms:Model, .//bms:ROId. All fields
// are optional — ElementTree tolerates missing elements by returning "".
type bmsVehicleInfo struct {
	VIN   string `xml:"VIN,omitempty"`
	Year  string `xml:"Year,omitempty"`
	Make  string `xml:"Make,omitempty"`
	Model string `xml:"Model,omitempty"`
	ROId  string `xml:"ROId,omitempty"`
}

type bmsEstimateAddRq struct {
	Owner                bmsOwner `xml:"Owner"`
	ActualPickupDateTime string   `xml:"ActualPickupDateTime"`
}

type bmsOwner struct {
	GivenName      string `xml:"GivenName"`
	OtherOrSurName string `xml:"OtherOrSurName"`
	CommPhone      string `xml:"CommPhone,omitempty"`
	// CommEmail (OH4-01) — surfaced in the admin UI under the phone number
	// so Marco can reach customers who prefer email. Optional; omitempty
	// keeps the element absent when the OWNR/INSD block has no address.
	CommEmail string `xml:"CommEmail,omitempty"`
	CommAddr  string `xml:"CommAddr,omitempty"`
}

// RenderBMS returns an XML byte slice representing b as a BMS 2.6 envelope
// the server-side parse_bms() function will accept. Always prefixed with the
// standard XML prolog. Indented for log-readability; indentation is not
// semantically required.
//
// Field mapping (b → BMS):
//   - DocumentVerCode: first non-empty of ENV[UNQFILE_ID, ESTFILE_ID, RO_ID],
//     falling back to b.Basename. Guaranteed non-empty so app.py never needs
//     its DocumentID fallback branch.
//   - DocumentStatus: ENV.TRANS_TYPE verbatim (E, EM, EL, EP, I, C, ...) —
//     defaults to "E" when blank so plain estimate POSTs schedule jobs.
//   - Owner.GivenName / OtherOrSurName / CommPhone / CommEmail / CommAddr:
//     AD1 OWNR_* fields (FN/LN/PH1/EA/ADDR1), with INSD_* fallback (some
//     shops populate only the insured) via pickOwnerField.
//   - VehicleInfo (OH4-01): VIN=V_VIN, Year=V_MODEL_YR, Make=V_MAKEDESC,
//     Model=V_MODEL, ROId=ENV.RO_ID. All optional; omitempty on blanks.
//   - ActualPickupDateTime: ""  (no pickup yet at estimate time)
//   - CloseDateTime: ""         (no close yet at estimate time)
//
// Parallel Python contract (app.py parse_bms):
//
//	<VIN>       -> data["vin"]
//	<Year>      -> year
//	<Make>      -> make_
//	<Model>     -> model                 vehicle_desc = "<Year> <Make> <Model>"
//	<ROId>      -> data["ro_id"]
//	<CommEmail> -> data["email"]
//	<CommAddr>  -> data["address"]
func RenderBMS(b *Bundle) []byte {
	doc := bmsDoc{
		Xmlns: bmsNamespace,
		Group: bmsGroup{
			Trans: bmsTrans{
				DocumentInfo: bmsDocumentInfo{
					DocumentVerCode: pickDocumentVerCode(b),
					DocumentStatus:  pickDocumentStatus(b),
				},
				EventInfo: bmsEventInfo{
					RepairEvent: bmsRepairEvent{CloseDateTime: ""},
				},
				VehicleInfo: bmsVehicleInfo{
					VIN:   lookup(b.VEH, "V_VIN"),
					Year:  lookup(b.VEH, "V_MODEL_YR"),
					Make:  lookup(b.VEH, "V_MAKEDESC"),
					Model: lookup(b.VEH, "V_MODEL"),
					ROId:  lookup(b.ENV, "RO_ID"),
				},
				EstimateAddRq: bmsEstimateAddRq{
					Owner: bmsOwner{
						GivenName:      pickOwnerField(b, "FN"),
						OtherOrSurName: pickOwnerField(b, "LN"),
						CommPhone:      pickOwnerField(b, "PH1"),
						CommEmail:      pickOwnerField(b, "EA"),
						CommAddr:       lookup(b.AD1, "OWNR_ADDR1"),
					},
					ActualPickupDateTime: "",
				},
			},
		},
	}

	body, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		// xml.MarshalIndent on a well-formed struct tree should not fail in
		// practice; if it ever does, return a minimal but still-valid BMS
		// envelope with the DocumentVerCode populated from the basename so
		// the scan cycle can still dedup by hash and the error surfaces in
		// logs when the server replies 400. This is better than returning
		// nil bytes which would silently suppress the POST.
		body = []byte(`<BMSEnvelope xmlns="` + bmsNamespace + `"><TransactionGroup><BMSTrans><DocumentInfo><DocumentVerCode>` + b.Basename + `</DocumentVerCode><DocumentStatus>EST</DocumentStatus></DocumentInfo></BMSTrans></TransactionGroup></BMSEnvelope>`)
	}

	var buf bytes.Buffer
	buf.WriteString(`<?xml version="1.0" encoding="utf-8"?>` + "\n")
	buf.Write(body)
	return buf.Bytes()
}

// pickDocumentVerCode returns the first non-empty ENV field in priority order,
// falling back to b.Basename. Guaranteed non-empty when called with a valid
// Bundle (Basename is always set by ParseBundle).
func pickDocumentVerCode(b *Bundle) string {
	priority := []string{"UNQFILE_ID", "ESTFILE_ID", "RO_ID"}
	for _, key := range priority {
		if v := lookup(b.ENV, key); v != "" {
			return v
		}
	}
	return b.Basename
}

// pickDocumentStatus returns ENV.TRANS_TYPE verbatim if present, else the
// default estimate status "E". ENV.TRANS_TYPE ∈ {E, EM, EL, EP, I, C, ...}
// per CIECA spec and is consumed by app.py's ESTIMATE_STATUSES /
// CLOSED_STATUSES sets to schedule the right follow-up jobs.
func pickDocumentStatus(b *Bundle) string {
	if v := lookup(b.ENV, "TRANS_TYPE"); v != "" {
		return v
	}
	return documentStatusEstimateDefault
}

// pickOwnerField returns the OWNR_<suffix> value from AD1 if non-empty,
// otherwise falls back to INSD_<suffix>. Some bodyshops populate only the
// insured block and leave owner blank (or vice-versa); trying both means
// a populated estimate never renders an empty customer record.
func pickOwnerField(b *Bundle, suffix string) string {
	if v := lookup(b.AD1, "OWNR_"+suffix); v != "" {
		return v
	}
	return lookup(b.AD1, "INSD_"+suffix)
}

// lookup returns m[key] if m is non-nil and the key is present, else "".
// Safe against nil maps — Bundle.ENV can be nil when the ENV file is absent.
func lookup(m map[string]string, key string) string {
	if m == nil {
		return ""
	}
	return m[key]
}
