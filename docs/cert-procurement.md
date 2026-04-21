# OV Code-Signing Certificate — Procurement Checklist

> **Code signing is now optional.** The portable-zip distribution
> (`EarlScheibWatcher-Portable.zip`) sidesteps most SmartScreen friction
> without a certificate — unsigned `.cmd` launchers and `.exe` binaries
> extracted from a zip archive trigger meaningfully less SmartScreen scrutiny
> than an unsigned installer `.exe`. Only procure an OV certificate if
> SmartScreen warnings become a real blocker for end users.

Lead time if you do proceed: 2–10 business days (CA identity validation).

## Why This Is Required

Every CI build is Authenticode-signed (SCAF-03). Without a real OV cert:
- Windows SmartScreen shows a red block dialog for all installs.
- Marco (non-technical end user) will be unable to run the installer.
- Phase 4 is blocked until this is done.

## Step-by-Step Checklist

### Step 1 — Purchase Certificate

- [ ] Buy an OV Code Signing Certificate from Sectigo via a reseller (~$200–225/yr) or DigiCert directly (~$370/yr)
  - Recommended resellers: Certera (https://certera.com), SSLDragon, NameCheap
  - Search for "Sectigo OV Code Signing Certificate" — avoid EV (same SmartScreen post-2024, higher cost)
- [ ] Note: as of February 2026, maximum certificate lifespan is 1 year (CA/B Forum rule)
- [ ] Set a calendar reminder 60 days before the expiry date

### Step 2 — Complete OV Identity Validation

- [ ] CA will email validation steps within 24 hours of purchase
- [ ] OV requires: business name verification, address, phone number
  - Documents typically needed: government-issued ID, business registration
- [ ] Allow 2–10 business days for validation
- [ ] Track validation status in CA portal

### Step 3 — Provision into Cloud HSM (for CI non-interactive signing)

**Preferred: SSL.com eSigner or DigiCert KeyLocker (cloud HSM — no USB token required)**

- [ ] After cert issuance, provision into cloud HSM via CA portal
- [ ] Export the certificate + key as a password-protected PFX:
  ```
  base64 -w0 signing.pfx > signing.pfx.b64
  ```
- [ ] Store the base64 value as GitHub Actions repository secret:
  - Secret name: `SIGNING_CERT_B64`
  - Location: GitHub repo → Settings → Secrets and variables → Actions
- [ ] Store the PFX password as:
  - Secret name: `SIGNING_CERT_PASS`
- [ ] Store the HMAC webhook secret as (if not already done):
  - Secret name: `GSD_HMAC_SECRET`

### Step 4 — Verify in CI

- [ ] Push a commit to trigger the CI workflow
- [ ] Confirm the `Sign binary` step runs (check Actions log)
- [ ] Confirm `osslsigncode verify` outputs `Signature verification: ok`
- [ ] Download the artifact and test on a Windows 10 VM:
  - Right-click `earlscheib.exe` → Properties → Digital Signatures tab → verify publisher name shows "Earl Scheib Auto Body Concord"
  - Run `signtool verify /pa /v earlscheib.exe` → confirm valid Authenticode signature

### Step 5 — Submit for SmartScreen Reputation (after first real signed release)

- [ ] Submit binary to Microsoft Security Intelligence: https://www.microsoft.com/en-us/wdsi/filesubmission
- [ ] Mark as "Legitimate software that I believe has been incorrectly detected"
- [ ] Submit to VirusTotal (https://www.virustotal.com/gui/home/upload) to propagate to downstream AV vendors

## Status

| Step | Status | Notes |
|------|--------|-------|
| Certificate purchased | [ ] Pending | Start immediately — 2–10 day lead time |
| OV validation complete | [ ] Pending | |
| PFX exported and base64-encoded | [ ] Pending | |
| `SIGNING_CERT_B64` secret added to GitHub | [ ] Pending | |
| `SIGNING_CERT_PASS` secret added to GitHub | [ ] Pending | |
| CI signing step verified in Actions log | [ ] Pending | |
| Signature verified on Windows VM | [ ] Pending | Required before Phase 4 ships |
| SmartScreen submission sent | [ ] Pending | After first release |

## HSM vs USB Token Tradeoffs

| Approach | CI-compatible | Cost | Notes |
|----------|--------------|------|-------|
| Cloud HSM (SSL.com eSigner / DigiCert KeyLocker) | Yes — non-interactive | ~$0–20/mo add-on | Preferred for automation; no physical device needed |
| USB Token (SafeNet eToken / YubiKey) | No — requires physical access | Token ~$50–100 one-time | Blocks CI; must sign on a machine with the token plugged in |

**Recommendation:** Use cloud HSM. The CA/B Forum now requires HSM storage for all code-signing keys (since June 2023). USB tokens are compliant but incompatible with CI automation. Cloud HSM is the only path to automated signing in GitHub Actions.

## Budget and Lead-Time Summary

| Item | Cost | Lead Time |
|------|------|-----------|
| Sectigo OV cert (via reseller) | ~$200–225/yr | 2–10 business days |
| DigiCert OV cert (direct) | ~$370/yr | 2–10 business days |
| SSL.com eSigner cloud HSM | ~$20/mo or bundled | Same as cert |
| DigiCert KeyLocker cloud HSM | Bundled with DigiCert cert | Same as cert |

## Notes

- The self-signed dev cert (`make dev-sign`) proves the pipeline works end-to-end but produces a SmartScreen-blocked binary.
- Real OV signing activates automatically once `SIGNING_CERT_B64` and `SIGNING_CERT_PASS` secrets are added to the repository.
- Phase 4 **MUST NOT** ship until "Signature verified on Windows VM" is checked above (per SCAF-06).
- **Never commit the .pfx file to source control** — store it only as a GitHub secret.
