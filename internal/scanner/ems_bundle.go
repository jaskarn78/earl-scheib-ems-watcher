package scanner

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// BundleCandidate is a detected EMS 2.01 dBase bundle ready to settle/parse.
// A bundle is the set of sibling files sharing a GUID basename; detection
// requires BOTH .AD1 AND .VEH to be present (case-insensitive).
type BundleCandidate struct {
	// Basename is the shared GUID part (filename minus the last extension),
	// preserving the ORIGINAL case of the first file that introduced it.
	Basename string
	// Files maps lowercased extension-without-dot ("ad1","veh","env",...) to
	// the absolute path on disk. Only the first occurrence of each extension
	// per basename is kept — bundles with duplicate-extension collisions are
	// rare in practice and the first-wins policy is deterministic enough.
	Files map[string]string
	// VirtualPath is the logical path used for dedup (processed_files.filepath)
	// — filepath.Join(dir, basename + ".bundle"). Not a real file; chosen so
	// it cannot collide with individual component file paths.
	VirtualPath string
}

// DetectBundles scans dir for EMS 2.01 dBase bundles.
//
// A bundle = 2+ files sharing a basename (filename minus last extension) where
// the extension set contains BOTH "ad1" AND "veh" (case-insensitive). Ordered
// output: sorted ascending by Basename to keep logs and tests deterministic.
//
// Returns an empty slice on any directory-read failure (logged by the caller's
// existing Candidates() loop; DetectBundles just mirrors that best-effort
// behaviour — a missing watch folder is not an error at bundle-detection time).
func DetectBundles(dir string, logger *slog.Logger) []BundleCandidate {
	entries, err := os.ReadDir(dir)
	if err != nil {
		// Candidates() already logged this — avoid duplicate warning line.
		return nil
	}

	// Group files by basename (preserving original case of first-seen name).
	type groupInfo struct {
		basename string
		files    map[string]string
	}
	groups := make(map[string]*groupInfo) // key = lowercase basename for case-insensitive group join

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		ext := filepath.Ext(name)
		if ext == "" {
			continue
		}
		base := strings.TrimSuffix(name, ext)
		lowerExt := strings.ToLower(strings.TrimPrefix(ext, "."))
		key := strings.ToLower(base)

		g, ok := groups[key]
		if !ok {
			g = &groupInfo{basename: base, files: make(map[string]string)}
			groups[key] = g
		}
		if _, seen := g.files[lowerExt]; !seen {
			g.files[lowerExt] = filepath.Join(dir, name)
		}
	}

	// A bundle needs both ad1 AND veh. Collect matching groups.
	out := make([]BundleCandidate, 0, len(groups))
	for _, g := range groups {
		if _, hasAD1 := g.files["ad1"]; !hasAD1 {
			continue
		}
		if _, hasVEH := g.files["veh"]; !hasVEH {
			continue
		}
		out = append(out, BundleCandidate{
			Basename:    g.basename,
			Files:       g.files,
			VirtualPath: filepath.Join(dir, g.basename+".bundle"),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Basename < out[j].Basename })
	return out
}

// bundleMtimeAnchor returns the LATEST mtime across bundle files as float
// seconds since epoch (UnixNano / 1e9), matching the mtime column semantics
// in processed_files (written by the plain-file track).
func bundleMtimeAnchor(files map[string]string) (float64, error) {
	var latest float64
	first := true
	for _, p := range files {
		fi, err := os.Stat(p)
		if err != nil {
			return 0, err
		}
		m := float64(fi.ModTime().UnixNano()) / 1e9
		if first || m > latest {
			latest = m
			first = false
		}
	}
	return latest, nil
}

// bundleSettle applies SettleCheck to EVERY file in the bundle. Settle=true
// iff every component's size+mtime stabilized. Returns the post-settle latest
// mtime (anchor) and the summed post-settle size.
//
// If any file fails to settle, returns (false, 0, 0) — whole bundle defers to
// the next scan cycle. This matches Python-watcher semantics: a bundle is only
// POSTed when its component set has fully finished writing.
func bundleSettle(b BundleCandidate, opts SettleOptions, log func(string, ...any)) (bool, float64, int64) {
	var anchor float64
	var total int64
	first := true
	for _, p := range b.Files {
		info, ok := SettleCheck(p, opts, log)
		if !ok || info == nil {
			return false, 0, 0
		}
		m := float64(info.ModTime().UnixNano()) / 1e9
		if first || m > anchor {
			anchor = m
			first = false
		}
		total += info.Size()
	}
	return true, anchor, total
}

// bundleSHA256 computes sha256 over concatenated file bytes, sorted ascending
// by lowercase(filepath.Base(path)). Deterministic across scans regardless of
// input map iteration order. Returns empty string on any read error.
//
// Matches the ordering used by internal/ems.sortedBundlePaths, keeping dedup
// hash and Bundle.SourceFiles in sync as a single source of truth.
func bundleSHA256(files map[string]string) (string, error) {
	paths := bundleSortedPaths(files)
	h := sha256.New()
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			return "", err
		}
		_, err = io.Copy(h, f)
		_ = f.Close()
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// bundleSortedPaths returns the paths in files sorted ascending by
// lowercase(filepath.Base). Keep in sync with ems.sortedBundlePaths.
func bundleSortedPaths(files map[string]string) []string {
	out := make([]string, 0, len(files))
	for _, p := range files {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(filepath.Base(out[i])) < strings.ToLower(filepath.Base(out[j]))
	})
	return out
}
