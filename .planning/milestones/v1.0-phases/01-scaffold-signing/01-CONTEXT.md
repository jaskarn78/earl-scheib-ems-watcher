# Phase 1: Scaffold + Signing - Context

**Gathered:** 2026-04-20
**Status:** Ready for planning
**Mode:** Auto-generated (infrastructure-only phase — discuss skipped)

<domain>
## Phase Boundary

A signed, runnable Windows exe ships from Linux CI on every commit — establishing the full toolchain before any feature code is written. Covers: Go module scaffold, subcommand dispatch skeleton, cross-compile pipeline (Linux → windows/amd64), go-winres resource embedding, osslsigncode + cloud HSM signing, OV code-signing certificate procurement.

Delivers requirements: SCAF-01, SCAF-02, SCAF-03, SCAF-04, SCAF-05, SCAF-06.

</domain>

<decisions>
## Implementation Decisions

### Claude's Discretion
All implementation choices are at Claude's discretion — pure infrastructure phase.

Anchors carried from project/research decisions:
- **Language**: Go 1.22+
- **Module path**: chosen during planning (suggest `github.com/jjagpal/earl-scheib-watcher` or similar; if the user has a preference they can override during plan review)
- **Subcommands**: `--tray`, `--scan`, `--wizard`, `--test`, `--status`, `--install` — all stubs in this phase
- **CI**: GitHub Actions (default); adjust if user prefers GitLab / other
- **Signing**: osslsigncode + cloud HSM (DigiCert KeyLocker or SSL.com eSigner); OV certificate
- **Resources**: go-winres for icon, version info, manifest
- **HMAC secret injection**: `-ldflags "-X main.secretKey=..."` at build time; secret sourced from CI secret store, never checked in
- **Cross-compile**: `GOOS=windows GOARCH=amd64`; CGO_ENABLED=0 for Phase 1 stubs (CGO introduced in Phase 3 for systray + webview2)
- **Cert procurement**: initiate in Phase 1; provisioned into HSM before Phase 4 ships (per research SUMMARY.md)

### Hard constraints from research
- Sign every CI build, not just release tags — progressively builds SmartScreen reputation
- HMAC secret must never appear in config.ini, logs, or source control
- Binary metadata (Properties > Details) must show product name, version, embedded icon — affects install-time user perception

</decisions>

<code_context>
## Existing Code Insights

### Reusable Assets
- Existing Python reference: `claude-code-project/ems_watcher.py` — subcommand shape (`--install`, `--test`, `--status`, `--loop`) to mirror
- `claude-code-project/config.ini` — config shape to parse in Phase 2

### Established Patterns
- No existing Go code — greenfield

### Integration Points
- Phase 2 will depend on: subcommand dispatch (SCAF-01), config loading stub, log init
- Phase 3 will depend on: CGO toolchain added to CI
- Phase 4 will depend on: signed binary artifact produced by CI

</code_context>

<specifics>
## Specific Ideas

- Module layout suggestion (can refine in plan): `cmd/earlscheib/main.go` (subcommand dispatcher) + `internal/` packages added by later phases
- Use Cobra or the stdlib `flag` package for subcommand dispatch — planning decision
- CI matrix: build Linux host, cross-compile windows/amd64, run `osslsigncode sign` + `osslsigncode verify`, attach artifact to workflow run
- For local dev without HSM: support a "dev mode" unsigned build (flag in CI), but verify-step is skipped only in dev

</specifics>

<deferred>
## Deferred Ideas

- EV certificate upgrade (deferred to v2 per REQUIREMENTS.md — only needed if OV SmartScreen reputation is insufficient after ship)
- Code-signing certificate auto-rotation / renewal automation (post-v1 ops concern)
- Reproducible builds / SBOM (nice-to-have, not in scope)
- Multi-arch (arm64) builds — Windows CCC ONE machines are x86_64 only

</deferred>
