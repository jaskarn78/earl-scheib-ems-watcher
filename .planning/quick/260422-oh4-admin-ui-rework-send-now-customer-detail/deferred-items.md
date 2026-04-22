# Deferred Items — Quick Task 260422-oh4

## CI: `amake/innosetup:6.7.1` Docker tag missing

**Discovered:** 2026-04-22 during Task 5 final push verification.

**Scope:** Pre-existing CI failure unrelated to OH4 changes. First observed on commit `8cb5e6b` (pre-OH4) and continues on `3620fe4` (OH4 part 5/5).

**Root cause:** `amake/innosetup:6.7.1` on Docker Hub has been removed — only `amake/innosetup:latest` is still published. CI workflow still pins to the old tag.

**Local Makefile already fixed:** `installer:` and `installer-syntax:` targets use `amake/innosetup:latest` (see commit `1a8d...` predating OH4). Only the GitHub Actions workflow file needs the same swap.

**Impact on OH4:** Zero. Local installer build succeeded; MD5 parity vs live download.exe confirmed; self-update chain operational. CI-built artifact is not consumed by the deployment pipeline (operator stages locally).

**Fix (separate quick task):** Update `.github/workflows/*.yml` to pin `amake/innosetup:latest` instead of `:6.7.1` in both the parse-only syntax check and the full build job.
