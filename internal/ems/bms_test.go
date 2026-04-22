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
			"V_OWNER_F":  "Marco",
			"V_OWNER_L":  "Rossi",
			"V_OWNER_PH": "5555550100",
		},
		VEH: map[string]string{"V_VIN": "VIN-001"},
		ENV: map[string]string{"E_DOC_NUM": "EST-001"},
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
	b := &Bundle{Basename: "ns-1", AD1: map[string]string{"V_OWNER_F": "n"}, VEH: map[string]string{"V_VIN": "v"}}
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
			"V_OWNER_F":  "A",
			"V_OWNER_L":  "B",
			"V_OWNER_PH": "1234567890",
			"V_OWNER_AD": "123 Main St",
		},
		VEH: map[string]string{"V_VIN": "V"},
		ENV: map[string]string{"E_DOC_NUM": "DOC"},
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
			"V_OWNER_F":  "Marco",
			"V_OWNER_L":  "Rossi",
			"V_OWNER_PH": "(925) 555-0199",
		},
		VEH: map[string]string{"V_VIN": "VIN"},
		ENV: map[string]string{"E_DOC_NUM": "EST-00042"},
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
		AD1:      map[string]string{"V_OWNER_F": "x"},
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
	// Priority: E_DOC_NUM > E_RO > E_EST_NUM > E_DOC_ID > E_REF.
	// When E_DOC_NUM is empty but E_RO has value, E_RO wins.
	b := &Bundle{
		Basename: "prio",
		AD1:      map[string]string{"V_OWNER_F": "x"},
		VEH:      map[string]string{"V_VIN": "y"},
		ENV: map[string]string{
			"E_DOC_NUM":  "",
			"E_RO":       "RO-7",
			"E_EST_NUM":  "EST-99",
			"E_DOC_ID":   "D-1",
		},
	}
	out := RenderBMS(b)
	if !strings.Contains(string(out), "<DocumentVerCode>RO-7</DocumentVerCode>") {
		t.Fatalf("expected RO-7 (E_RO) to win priority; got:\n%s", out)
	}
}

// firstN returns the first n bytes of b (for error messages).
func firstN(b []byte, n int) string {
	if n > len(b) {
		n = len(b)
	}
	return string(b[:n])
}
