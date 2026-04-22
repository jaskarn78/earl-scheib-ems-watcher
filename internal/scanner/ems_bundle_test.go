package scanner

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Valentin-Kaiser/go-dbase/dbase"
	"golang.org/x/text/encoding/charmap"
)

// ---- Bundle DETECTION tests (no real .dbf needed) --------------------------

func TestDetectBundles_AD1PlusVEH_OneBundle(t *testing.T) {
	dir := t.TempDir()
	writePlain(t, dir, "G-1.AD1", "x")
	writePlain(t, dir, "G-1.VEH", "y")
	writePlain(t, dir, "G-1.ENV", "z")

	got := DetectBundles(dir, testLogger(t))
	if len(got) != 1 {
		t.Fatalf("expected 1 bundle, got %d: %#v", len(got), got)
	}
	if got[0].Basename != "G-1" {
		t.Errorf("Basename=%q want G-1", got[0].Basename)
	}
	for _, ext := range []string{"ad1", "veh", "env"} {
		if _, ok := got[0].Files[ext]; !ok {
			t.Errorf("expected Files[%q] present", ext)
		}
	}
	wantVP := filepath.Join(dir, "G-1.bundle")
	if got[0].VirtualPath != wantVP {
		t.Errorf("VirtualPath=%q want %q", got[0].VirtualPath, wantVP)
	}
}

func TestDetectBundles_AD1Only_Zero(t *testing.T) {
	dir := t.TempDir()
	writePlain(t, dir, "G-1.AD1", "x")

	got := DetectBundles(dir, testLogger(t))
	if len(got) != 0 {
		t.Fatalf("expected 0 bundles (no VEH), got %d", len(got))
	}
}

func TestDetectBundles_CaseInsensitiveExt(t *testing.T) {
	dir := t.TempDir()
	writePlain(t, dir, "G-1.Ad1", "x")
	writePlain(t, dir, "G-1.vEh", "y")

	got := DetectBundles(dir, testLogger(t))
	if len(got) != 1 {
		t.Fatalf("expected 1 bundle (case-insensitive), got %d", len(got))
	}
	if _, ok := got[0].Files["ad1"]; !ok {
		t.Errorf("expected Files[ad1] via case-insensitive normalization")
	}
	if _, ok := got[0].Files["veh"]; !ok {
		t.Errorf("expected Files[veh] via case-insensitive normalization")
	}
}

func TestDetectBundles_MultipleBundles(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"G-1.AD1", "G-1.VEH", "G-2.AD1", "G-2.VEH"} {
		writePlain(t, dir, n, "x")
	}
	got := DetectBundles(dir, testLogger(t))
	if len(got) != 2 {
		t.Fatalf("expected 2 bundles, got %d: %#v", len(got), got)
	}
	names := []string{got[0].Basename, got[1].Basename}
	if !(contains(names, "G-1") && contains(names, "G-2")) {
		t.Errorf("expected G-1 and G-2, got %v", names)
	}
}

func TestDetectBundles_IgnoresPlainXML(t *testing.T) {
	dir := t.TempDir()
	writePlain(t, dir, "estimate.xml", "<root/>")
	writePlain(t, dir, "G-1.AD1", "x")
	writePlain(t, dir, "G-1.VEH", "y")
	got := DetectBundles(dir, testLogger(t))
	if len(got) != 1 {
		t.Fatalf("expected 1 bundle, got %d", len(got))
	}
	if got[0].Basename != "G-1" {
		t.Errorf("Basename=%q want G-1 (XML file must not absorb or become a bundle)", got[0].Basename)
	}
}

// ---- Bundle HASH tests -----------------------------------------------------

func TestBundleSHA256_Deterministic_SortedByLowerName(t *testing.T) {
	dir := t.TempDir()
	p1 := writePlainBytes(t, dir, "G-1.AD1", []byte("AAA"))
	p2 := writePlainBytes(t, dir, "G-1.VEH", []byte("BBB"))
	filesA := map[string]string{"ad1": p1, "veh": p2}
	filesB := map[string]string{"veh": p2, "ad1": p1}

	h1, err := bundleSHA256(filesA)
	if err != nil {
		t.Fatalf("bundleSHA256(A): %v", err)
	}
	h2, err := bundleSHA256(filesB)
	if err != nil {
		t.Fatalf("bundleSHA256(B): %v", err)
	}
	if h1 != h2 {
		t.Errorf("hash differs across shuffled input: %q vs %q", h1, h2)
	}
	if h1 == "" || len(h1) != 64 {
		t.Errorf("expected 64-hex-char sha256, got %q", h1)
	}
}

// ---- Full pipeline tests using real .dbf fixtures --------------------------

// scannerTestDBF is a copy of internal/ems.makeTestDBF. We duplicate it here
// (three simple calls) to avoid cross-package test plumbing.
func scannerTestDBF(t *testing.T, dir, filename string, colName string, length uint8, value string) string {
	t.Helper()
	finalPath := filepath.Join(dir, filename)

	baseWork := filepath.Join(t.TempDir(), "WORK")
	if err := os.MkdirAll(baseWork, 0o755); err != nil {
		t.Fatalf("mkdir workdir: %v", err)
	}
	t.Chdir(baseWork)
	workName := "FIXTURE.DBF"

	col, err := dbase.NewColumn(colName, dbase.Character, length, 0, false)
	if err != nil {
		t.Fatalf("NewColumn: %v", err)
	}
	tbl, err := dbase.NewTable(
		dbase.FoxBasePlus,
		&dbase.Config{
			Filename:   workName,
			Converter:  dbase.NewDefaultConverter(charmap.Windows1252),
			TrimSpaces: true,
			Untested:   true,
		},
		[]*dbase.Column{col}, 0, nil,
	)
	if err != nil {
		t.Fatalf("NewTable: %v", err)
	}
	r, err := tbl.RowFromMap(map[string]interface{}{colName: value})
	if err != nil {
		t.Fatalf("RowFromMap: %v", err)
	}
	if err := r.Add(); err != nil {
		t.Fatalf("row.Add: %v", err)
	}
	if err := tbl.Close(); err != nil {
		t.Fatalf("tbl.Close: %v", err)
	}

	workPath := filepath.Join(baseWork, workName)
	actual := workPath
	if _, err := os.Stat(actual); err != nil {
		entries, _ := os.ReadDir(baseWork)
		for _, e := range entries {
			if !e.IsDir() {
				actual = filepath.Join(baseWork, e.Name())
				break
			}
		}
	}
	data, err := os.ReadFile(actual)
	if err != nil {
		t.Fatalf("read %s: %v", actual, err)
	}
	if err := os.WriteFile(finalPath, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", finalPath, err)
	}
	return finalPath
}

func TestRun_BundleTrack_POSTsOnce(t *testing.T) {
	dir := t.TempDir()
	scannerTestDBF(t, dir, "G-777.AD1", "OWNR_FN", 20, "Runner")
	scannerTestDBF(t, dir, "G-777.VEH", "V_VIN", 20, "VIN-777")

	d := openTestDB(t)

	var sent []struct {
		Path string
		Body []byte
	}
	var mu sync.Mutex
	sender := func(p string, b []byte) bool {
		mu.Lock()
		defer mu.Unlock()
		sent = append(sent, struct {
			Path string
			Body []byte
		}{p, append([]byte(nil), b...)})
		return true
	}

	cfg := RunConfig{
		WatchFolder: dir,
		DB:          d,
		Logger:      testLogger(t),
		Sender:      sender,
		SettleOpts:  SettleOptions{Samples: 2, Interval: 1 * time.Millisecond},
	}

	proc1, err1 := Run(cfg)
	if err1 != 0 {
		t.Fatalf("run1 errors=%d want 0", err1)
	}
	if proc1 != 1 {
		t.Fatalf("run1 processed=%d want 1", proc1)
	}
	if len(sent) != 1 {
		t.Fatalf("expected 1 send, got %d", len(sent))
	}
	if !bytes.HasPrefix(sent[0].Body, []byte("<?xml")) {
		t.Errorf("body does not start with <?xml: %q", firstN(sent[0].Body, 40))
	}
	if !bytes.Contains(sent[0].Body, []byte("<GivenName>Runner</GivenName>")) {
		t.Errorf("body missing GivenName=Runner: %s", sent[0].Body)
	}
	if !strings.HasSuffix(sent[0].Path, ".bundle") {
		t.Errorf("sender path=%q want .bundle suffix", sent[0].Path)
	}

	// Second run must dedup.
	proc2, err2 := Run(cfg)
	if proc2 != 0 || err2 != 0 {
		t.Fatalf("run2: expected (0,0), got (%d,%d)", proc2, err2)
	}
	if len(sent) != 1 {
		t.Fatalf("dedup: expected 1 send total, got %d", len(sent))
	}
}

func TestRun_BothTracks_Coexist(t *testing.T) {
	dir := t.TempDir()
	// Plain XML track file.
	xmlPath := filepath.Join(dir, "plain.xml")
	if err := os.WriteFile(xmlPath, []byte("<root/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Bundle track files.
	scannerTestDBF(t, dir, "G-8.AD1", "OWNR_FN", 20, "Two")
	scannerTestDBF(t, dir, "G-8.VEH", "V_VIN", 20, "V")

	d := openTestDB(t)

	var sent []struct {
		Path string
		Body []byte
	}
	sender := func(p string, b []byte) bool {
		sent = append(sent, struct {
			Path string
			Body []byte
		}{p, append([]byte(nil), b...)})
		return true
	}

	proc, errs := Run(RunConfig{
		WatchFolder: dir,
		DB:          d,
		Logger:      testLogger(t),
		Sender:      sender,
		SettleOpts:  SettleOptions{Samples: 2, Interval: 1 * time.Millisecond},
	})
	if errs != 0 {
		t.Fatalf("errors=%d want 0", errs)
	}
	if proc != 2 {
		t.Fatalf("processed=%d want 2", proc)
	}
	if len(sent) != 2 {
		t.Fatalf("expected 2 sends (plain + bundle), got %d", len(sent))
	}

	// One should be raw XML bytes, one should be synthesized BMS.
	var plainSeen, bundleSeen bool
	for _, s := range sent {
		if bytes.HasPrefix(s.Body, []byte("<root/>")) {
			plainSeen = true
		}
		if bytes.Contains(s.Body, []byte("<BMSEnvelope")) {
			bundleSeen = true
		}
	}
	if !plainSeen {
		t.Error("expected one send with plain XML body (<root/>)")
	}
	if !bundleSeen {
		t.Error("expected one send with synthesized BMSEnvelope body")
	}
}

// TestRun_BundleSendFailure_NoDedupRow — failed send does NOT mark as processed.
func TestRun_BundleSendFailure_NoDedupRow(t *testing.T) {
	dir := t.TempDir()
	scannerTestDBF(t, dir, "G-F.AD1", "OWNR_FN", 20, "F")
	scannerTestDBF(t, dir, "G-F.VEH", "V_VIN", 20, "V")

	d := openTestDB(t)
	calls := 0
	sender := func(_ string, _ []byte) bool {
		calls++
		return false
	}
	cfg := RunConfig{
		WatchFolder: dir,
		DB:          d,
		Logger:      testLogger(t),
		Sender:      sender,
		SettleOpts:  SettleOptions{Samples: 2, Interval: 1 * time.Millisecond},
	}
	proc, errs := Run(cfg)
	if proc != 0 || errs != 1 {
		t.Fatalf("expected (0,1), got (%d,%d)", proc, errs)
	}

	// Re-run: sender should be called AGAIN (no dedup row stored).
	proc2, errs2 := Run(cfg)
	if proc2 != 0 || errs2 != 1 {
		t.Fatalf("run2: expected (0,1) retry, got (%d,%d)", proc2, errs2)
	}
	if calls != 2 {
		t.Fatalf("expected 2 sender calls across retries, got %d", calls)
	}
}

// TestMainSender_BundleTriggerQueryParam — validates the main.go Sender wrapper
// appends ?trigger=ems_bundle ONLY for bundle paths.
// This test replicates the sendFn logic because it's in main package; we inline
// a minimal copy to prove the query-param logic.
func TestMainSender_BundleTriggerQueryParam(t *testing.T) {
	// The logic we mirror from cmd/earlscheib/main.go:
	wrapURL := func(base, filePath string) string {
		if strings.HasSuffix(filePath, ".bundle") {
			if strings.Contains(base, "?") {
				return base + "&trigger=ems_bundle"
			}
			return base + "?trigger=ems_bundle"
		}
		return base
	}

	cases := []struct {
		base     string
		filePath string
		want     string
	}{
		{"https://x/e", "/w/plain.xml", "https://x/e"},
		{"https://x/e", "/w/G-1.bundle", "https://x/e?trigger=ems_bundle"},
		{"https://x/e?key=v", "/w/G-1.bundle", "https://x/e?key=v&trigger=ems_bundle"},
	}
	for _, c := range cases {
		got := wrapURL(c.base, c.filePath)
		if got != c.want {
			t.Errorf("wrapURL(%q,%q)=%q want %q", c.base, c.filePath, got, c.want)
		}
	}

	// E2E: wire a httptest.Server that records RawQuery and verifies the
	// query-param was actually sent on the wire.
	var gotQuery string
	var qMu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		qMu.Lock()
		gotQuery = r.URL.RawQuery
		qMu.Unlock()
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	// Use stdlib http.Post directly — we're verifying the URL, not the signer.
	resp, err := http.Post(wrapURL(srv.URL, "/w/G-42.bundle"), "application/xml", strings.NewReader("<x/>"))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
	qMu.Lock()
	defer qMu.Unlock()
	if gotQuery != "trigger=ems_bundle" {
		t.Errorf("RawQuery=%q want trigger=ems_bundle", gotQuery)
	}
}

// ---- helpers ---------------------------------------------------------------

func writePlain(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func writePlainBytes(t *testing.T, dir, name string, b []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return p
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func firstN(b []byte, n int) string {
	if n > len(b) {
		n = len(b)
	}
	return string(b[:n])
}
