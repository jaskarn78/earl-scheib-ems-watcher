package ems

import (
	"bytes"
	"encoding/xml"
	"strings"
	"testing"
)

// bmsProbe mirrors the subset of app.py's parse_bms element names the Go side
// must emit. Fields are matched by local name under the BMS namespace, which
// matches Python's ElementTree `.//bms:TAG` lookup with a registered NS.
type bmsProbe struct {
	XMLName         xml.Name `xml:"BMSEnvelope"`
	DocumentVerCode string   `xml:"TransactionGroup>BMSTrans>DocumentInfo>DocumentVerCode"`
	DocumentStatus  string   `xml:"TransactionGroup>BMSTrans>DocumentInfo>DocumentStatus"`
	GivenName       string   `xml:"TransactionGroup>BMSTrans>VehicleDamageEstimateAddRq>Owner>GivenName"`
	SurName         string   `xml:"TransactionGroup>BMSTrans>VehicleDamageEstimateAddRq>Owner>OtherOrSurName"`
	CommPhone       string   `xml:"TransactionGroup>BMSTrans>VehicleDamageEstimateAddRq>Owner>CommPhone"`
}

func Test_RenderBMS_ParsesCleanly(t *testing.T) {
	t.Parallel()
	b := &Bundle{
		Basename: "G-1",
		AD1: map[string]string{
			"OWNR_FN":  "Marco",
			"OWNR_LN":  "Rossi",
			"OWNR_PH1": "5555550100",
		},
		VEH: map[string]string{"V_VIN": "VIN-001"},
		ENV: map[string]string{"UNQFILE_ID": "EST-001"},
	}
	out := RenderBMS(b)
	if !bytes.HasPrefix(out, []byte(`<?xml version="1.0" encoding="utf-8"?>`)) {
		t.Fatalf("output must start with XML prolog; got %q", firstN(out, 60))
	}
	var dump struct {
		XMLName xml.Name
	}
	if err := xml.Unmarshal(out, &dump); err != nil {
		t.Fatalf("stdlib xml.Unmarshal: %v", err)
	}
}

func Test_RenderBMS_HasCCCNamespace(t *testing.T) {
	t.Parallel()
	b := &Bundle{Basename: "ns-1", AD1: map[string]string{"OWNR_FN": "n"}, VEH: map[string]string{"V_VIN": "v"}}
	out := RenderBMS(b)
	if !bytes.Contains(out, []byte(`xmlns="http://www.cieca.com/BMS"`)) {
		t.Fatalf(`expected xmlns="http://www.cieca.com/BMS" in output; got:\n%s`, out)
	}
}

func Test_RenderBMS_ElementsMatchPython(t *testing.T) {
	t.Parallel()
	b := &Bundle{
		Basename: "match-1",
		AD1: map[string]string{
			"OWNR_FN":    "A",
			"OWNR_LN":    "B",
			"OWNR_PH1":   "1234567890",
			"OWNR_ADDR1": "123 Main St",
		},
		VEH: map[string]string{"V_VIN": "V"},
		ENV: map[string]string{"UNQFILE_ID": "DOC"},
	}
	out := RenderBMS(b)
	mustContain := []string{
		"<DocumentVerCode>", "<DocumentStatus>",
		"<Owner>", "<GivenName>", "<OtherOrSurName>", "<CommPhone>",
		"<EventInfo>", "<RepairEvent>", "<CloseDateTime>",
		"<ActualPickupDateTime>",
	}
	for _, tag := range mustContain {
		if !bytes.Contains(out, []byte(tag)) {
			t.Errorf("output missing tag %s\nrendered:\n%s", tag, out)
		}
	}
}

func Test_RenderBMS_ValuesRoundtrip(t *testing.T) {
	t.Parallel()
	b := &Bundle{
		Basename: "rt-1",
		AD1: map[string]string{
			"OWNR_FN":  "Marco",
			"OWNR_LN":  "Rossi",
			"OWNR_PH1": "(925) 555-0199",
		},
		VEH: map[string]string{"V_VIN": "VIN"},
		ENV: map[string]string{"UNQFILE_ID": "EST-00042"},
	}
	out := RenderBMS(b)

	var probe bmsProbe
	if err := xml.Unmarshal(out, &probe); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	if probe.GivenName != "Marco" {
		t.Errorf("GivenName=%q want Marco", probe.GivenName)
	}
	if probe.SurName != "Rossi" {
		t.Errorf("OtherOrSurName=%q want Rossi", probe.SurName)
	}
	if probe.CommPhone != "(925) 555-0199" {
		t.Errorf("CommPhone=%q want (925) 555-0199", probe.CommPhone)
	}
	if probe.DocumentVerCode != "EST-00042" {
		t.Errorf("DocumentVerCode=%q want EST-00042", probe.DocumentVerCode)
	}
}

func Test_RenderBMS_FallbackToBasename(t *testing.T) {
	t.Parallel()
	b := &Bundle{
		Basename: "FALLBACK-BASENAME",
		AD1:      map[string]string{"OWNR_FN": "x"},
		VEH:      map[string]string{"V_VIN": "y"},
		// ENV intentionally nil — DocumentVerCode MUST fall back to Basename.
	}
	out := RenderBMS(b)
	if !strings.Contains(string(out), "<DocumentVerCode>FALLBACK-BASENAME</DocumentVerCode>") {
		t.Fatalf("expected DocumentVerCode=FALLBACK-BASENAME fallback; got:\n%s", out)
	}
}

func Test_RenderBMS_ENVPriorityOrder(t *testing.T) {
	t.Parallel()
	// Priority: UNQFILE_ID > ESTFILE_ID > RO_ID, then fallback to Basename.
	// When UNQFILE_ID is empty but ESTFILE_ID has value, ESTFILE_ID wins.
	b := &Bundle{
		Basename: "prio",
		AD1:      map[string]string{"OWNR_FN": "x"},
		VEH:      map[string]string{"V_VIN": "y"},
		ENV: map[string]string{
			"UNQFILE_ID": "",
			"ESTFILE_ID": "EST-99",
			"RO_ID":      "RO-7",
		},
	}
	out := RenderBMS(b)
	if !strings.Contains(string(out), "<DocumentVerCode>EST-99</DocumentVerCode>") {
		t.Fatalf("expected EST-99 (ESTFILE_ID) to win priority over RO_ID; got:\n%s", out)
	}
}

// Test_pickDocumentStatus_ClosedROOverride verifies that pickDocumentStatus
// returns "C" only when AD2.DATE_OUT is non-empty AND TTL.G_TTL_AMT > 0.
// RO_CMPDATE alone must NOT trigger "C" — CCC ONE populates it on fresh
// estimates (likely the document creation date), causing false positives.
func Test_pickDocumentStatus_ClosedROOverride(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		ad2       map[string]string // nil → .ad2 absent
		ttl       map[string]string // nil → .ttl absent
		transType string
		want      string
	}{
		{
			name:      "open-estimate",
			ad2:       nil,
			ttl:       nil,
			transType: "E",
			want:      "E",
		},
		{
			name:      "closed-no-bill",
			ad2:       map[string]string{"RO_CMPDATE": "2026-04-30", "DATE_OUT": "2026-04-30"},
			ttl:       map[string]string{"G_TTL_AMT": "0.00"},
			transType: "E",
			want:      "E",
		},
		{
			name:      "closed-with-bill",
			ad2:       map[string]string{"RO_CMPDATE": "2026-04-30", "DATE_OUT": "2026-04-30"},
			ttl:       map[string]string{"G_TTL_AMT": "$1,234.56"},
			transType: "E",
			want:      "C",
		},
		{
			name:      "ad2-present-but-blank",
			ad2:       map[string]string{"RO_CMPDATE": "", "DATE_OUT": ""},
			ttl:       map[string]string{"G_TTL_AMT": "999.00"},
			transType: "E",
			want:      "E",
		},
		{
			name:      "date-out-only-with-bill",
			ad2:       map[string]string{"RO_CMPDATE": "", "DATE_OUT": "2026-04-30"},
			ttl:       map[string]string{"G_TTL_AMT": "500.00"},
			transType: "E",
			want:      "C",
		},
		{
			// Regression: CCC ONE sets RO_CMPDATE on fresh estimates (creation
			// date), so RO_CMPDATE alone with a non-zero estimate total must
			// NOT trigger the closed-RO override.
			name:      "ro-cmpdate-only-no-date-out",
			ad2:       map[string]string{"RO_CMPDATE": "2026-05-07", "DATE_OUT": ""},
			ttl:       map[string]string{"G_TTL_AMT": "$3,500.00"},
			transType: "E",
			want:      "E",
		},
		{
			name:      "trans-type-EM-passthrough",
			ad2:       nil,
			ttl:       nil,
			transType: "EM",
			want:      "EM",
		},
		{
			// Regression (260508-q9c): parse.go used to store the zero-time
			// string "0001-01-01 00:00:00 +0000 UTC" for empty dBASE date
			// columns, which tripped lookup(b.AD2, "DATE_OUT") != "" and
			// misclassified fresh estimates (with any non-zero G_TTL_AMT) as
			// closed ROs. This case pins the downstream contract: an empty
			// DATE_OUT must always return "E", regardless of TTL grand total.
			name:      "empty-date-out-with-bill-regression-q9c",
			ad2:       map[string]string{"DATE_OUT": ""},
			ttl:       map[string]string{"G_TTL_AMT": "1500.00"},
			transType: "E",
			want:      "E",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var env map[string]string
			if tc.transType != "" {
				env = map[string]string{"TRANS_TYPE": tc.transType}
			}
			b := &Bundle{
				Basename: "test-" + tc.name,
				AD1:      map[string]string{"OWNR_FN": "x"},
				VEH:      map[string]string{"V_VIN": "y"},
				ENV:      env,
				AD2:      tc.ad2,
				TTL:      tc.ttl,
			}
			got := pickDocumentStatus(b)
			if got != tc.want {
				t.Errorf("pickDocumentStatus = %q, want %q", got, tc.want)
			}
		})
	}
}

// Test_parseAmount verifies the tolerant currency-string parser.
func Test_parseAmount(t *testing.T) {
	t.Parallel()
	cases := []struct {
		input string
		want  float64
	}{
		{"", 0},
		{"   ", 0},
		{"0", 0},
		{"0.00", 0},
		{"1234.56", 1234.56},
		{"$1,234.56", 1234.56},
		{"$0.00", 0},
		{"1,234", 1234},
		{"abc", 0},
		{"$abc", 0},
		{"  $42.00  ", 42},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			got := parseAmount(tc.input)
			if got != tc.want {
				t.Errorf("parseAmount(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

// firstN returns the first n bytes of b (for error messages).
func firstN(b []byte, n int) string {
	if n > len(b) {
		n = len(b)
	}
	return string(b[:n])
}
