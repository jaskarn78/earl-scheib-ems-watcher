package ems

import (
	"bytes"
	"encoding/xml"
)

// BMS namespace. Must match app.py's BMS_NS byte-for-byte — ElementTree does
// namespace-aware path lookups, so a mismatched URI silently yields empty
// results from parse_bms (server 400 "invalid BMS payload").
const bmsNamespace = "http://www.cieca.com/BMS"

// Document status literal. CCC ONE EMS 2.01 bundles we synthesize represent
// an estimate transmission; app.py reads DocumentStatus into data but does
// not branch on the value. "EST" is the conventional CIECA code. If a future
// workflow needs to differentiate statuses, plumb ENV["E_STATUS"] into here.
const documentStatusEstimate = "EST"

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
	DocumentInfo   bmsDocumentInfo   `xml:"DocumentInfo"`
	EventInfo      bmsEventInfo      `xml:"EventInfo"`
	EstimateAddRq  bmsEstimateAddRq  `xml:"VehicleDamageEstimateAddRq"`
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

type bmsEstimateAddRq struct {
	Owner                bmsOwner `xml:"Owner"`
	ActualPickupDateTime string   `xml:"ActualPickupDateTime"`
}

type bmsOwner struct {
	GivenName      string `xml:"GivenName"`
	OtherOrSurName string `xml:"OtherOrSurName"`
	CommPhone      string `xml:"CommPhone,omitempty"`
	CommAddr       string `xml:"CommAddr,omitempty"`
}

// RenderBMS returns an XML byte slice representing b as a BMS 2.6 envelope
// the server-side parse_bms() function will accept. Always prefixed with the
// standard XML prolog. Indented for log-readability; indentation is not
// semantically required.
//
// Field mapping (b → BMS):
//   - DocumentVerCode: first non-empty of ENV[E_DOC_NUM, E_RO, E_EST_NUM,
//     E_DOC_ID, E_REF], falling back to b.Basename. Guaranteed non-empty —
//     this ensures app.py never needs its DocumentID fallback branch.
//   - DocumentStatus: literal "EST".
//   - Owner.GivenName: AD1[V_OWNER_F]
//   - Owner.OtherOrSurName: AD1[V_OWNER_L]
//   - Owner.CommPhone: AD1[V_OWNER_PH]  (raw; clean_phone normalizes server-side)
//   - Owner.CommAddr: AD1[V_OWNER_AD]   (informational; app.py ignores today)
//   - ActualPickupDateTime: ""  (no pickup yet at estimate time)
//   - CloseDateTime: ""         (no close yet at estimate time)
func RenderBMS(b *Bundle) []byte {
	doc := bmsDoc{
		Xmlns: bmsNamespace,
		Group: bmsGroup{
			Trans: bmsTrans{
				DocumentInfo: bmsDocumentInfo{
					DocumentVerCode: pickDocumentVerCode(b),
					DocumentStatus:  documentStatusEstimate,
				},
				EventInfo: bmsEventInfo{
					RepairEvent: bmsRepairEvent{CloseDateTime: ""},
				},
				EstimateAddRq: bmsEstimateAddRq{
					Owner: bmsOwner{
						GivenName:      lookup(b.AD1, "V_OWNER_F"),
						OtherOrSurName: lookup(b.AD1, "V_OWNER_L"),
						CommPhone:      lookup(b.AD1, "V_OWNER_PH"),
						CommAddr:       lookup(b.AD1, "V_OWNER_AD"),
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
	priority := []string{"E_DOC_NUM", "E_RO", "E_EST_NUM", "E_DOC_ID", "E_REF"}
	for _, key := range priority {
		if v := lookup(b.ENV, key); v != "" {
			return v
		}
	}
	return b.Basename
}

// lookup returns m[key] if m is non-nil and the key is present, else "".
// Safe against nil maps — Bundle.ENV can be nil when the ENV file is absent.
func lookup(m map[string]string, key string) string {
	if m == nil {
		return ""
	}
	return m[key]
}
