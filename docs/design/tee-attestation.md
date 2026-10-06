# TEE / Attestation — Design & Posture (Track 131)

Status: **design + posture only.** Ships in v0.16.0 as an emit-when-set
advert field, a platform probe, and this document. **Attested execution
and remote verification are explicitly out** — nothing in this sprint
proves work ran inside an enclave.

## Why a posture field at all

The market threat model says *the prompt is the contract* — a buyer can
prove what was asked and hash what came back, but cannot observe the
runtime the seller's agent executed in. For some buyers that matters:
data sensitivity ("did my prompt run where the host kernel can read
it?") and computation integrity ("was inference run on the advertised
model?"). Trusted Execution Environments are the hardware answer to the
first half of that question, and buyers have asked for the signal.

The honest position: a TEE claim is only as strong as the verification
behind it. Full remote attestation — quote generation, collateral
fetch, vendor-chain verification, freshness binding to the session — is
a real subsystem, not a field. So this track ships the *posture layer*:
a seller declares a TEE capability in the advert, the module detects
hardware presence honestly, and buyers weigh the claim accordingly. The
verification machinery is the roadmap, below.

## The survey

### Intel TDX (Trust Domain Extensions)

- **What it is.** VM-level isolation: the guest TD runs with memory
  confidentiality + integrity against the host VMM, kernel, and other
  VMs. Trust root is the Intel TDX module (signed, loaded by the CPU's
  SEAM root mode) plus platform fuses.
- **Attestation.** The guest asks the TDX module for a **quote**
  (`TDG.MR.REPORT` → quote via the QE — a signed ECDSA-P256 structure
  over the TD's measurement registers `MRTD` + `RTMR[0..3]` plus a
  64-byte `REPORTDATA` field for freshness binding). Quote verification
  chains through Intel's **PCCS/PCS** collateral: PCK cert chain → TCB
  info → QE identity → CRLs. DCAP is the verification library.
- **Guest-side surface.** Inside a TDX guest: `/dev/tdx_guest` exposes
  `TDX_CMD_GET_REPORT0` → report → converted to a quote via a quoting
  enclave channel (QGS/QVE). Our probe keys on this device.
- **Honest limits.** The quote binds *measurements of the boot image +
  initial state*, not "what the process later did". Runtime claims need
  `RTMR` extension discipline plus a measured-boot chain (e.g.
  confidential-containers style). A forged host can lie about many
  things but *cannot* forge the PCK chain — the quote verifies or it
  doesn't.

### AMD SEV-SNP (Secure Encrypted Virtualization — Secure Nested Paging)

- **What it is.** Also VM-level: encrypted guest memory with
  integrity/replay protection (SNP adds page-validation so the host
  can't alias or roll back pages). Trust root is the AMD-SP firmware
  running on the Platform Security Processor.
- **Attestation.** The guest calls `SNP_GET_REPORT` (msg via the PSP)
  → a signed attestation report over `MEASUREMENT` (launch digest),
  `REPORT_DATA` (64-byte freshness/user field), TCB version, and
  policy bits. Verification: VCEK cert (derived per-chip + per-TCB)
  fetched from AMD's KDS → ECDSA signature over the report. Versioned
  chip-key endorsement, no CA-signed per-VM cert — **the KDS fetch is
  the online dependency**.
- **Guest-side surface.** `/dev/sev-guest` exposes the report ioctl.
  Our probe keys on it.
- **Honest limits.** Same shape as TDX: the measurement binds the launch
  image; "the model binary that actually ran" needs an init chain that
  measures it (e.g. measured direct boot + dm-verity). SNP's
  `HOST_DATA`/`ID_BLOCK` let the *owner* bind expectations, which is
  the stronger posture for market use.

### Arm TrustZone (via OP-TEE)

- **What it is.** Not VM isolation — a secure-world partition on the
  SoC: trusted applications (TAs) run under a TEE OS (OP-TEE, Qualcomm
  QTEE, Trustonic) while Linux runs in the normal world. Smaller TCB
  than a CVM, but a narrower programming model: a TA is a fixed
  function, not a general agent runtime.
- **Attestation.** No universal standard — ARM **CCA** adds
  realm-style attestation on newer silicon, but the shipped world is
  per-vendor. OP-TEE supports TA-level signing; remote-verification
  flows are mostly bespoke (or ride ARM PSA attestation tokens where
  the platform implements them).
- **Guest-side surface.** Linux userspace sees `/dev/tee0` +
  `/dev/teepriv0` (OP-TEE driver). Our probe keys on `/dev/tee0`.
- **Honest limits.** `/dev/tee0` says an OP-TEE driver exists — it says
  little about which TAs are loaded and nothing standard about remote
  verification. We advertise `trustzone` as *capability*, and the
  design doc is frank that this kind has the weakest verify story.

### (Not probed) Intel SGX

Enclave-level (not VM) TEE — `EPID`/`DCAP` attestation, `/dev/sgx_enclave`.
Excluded from the probe because SGX on modern client parts is deprecated
and the market's "whole agent runtime" shape fits CVMs better than
enclaves; the `tee_kind` override accepts `sgx` for operators who
deliberately ship one.

## What the shipped claim means

```json
"attestation": {"kind": "sev-snp",
                "report_url": "https://seller.example/tee/report",
                "evidence_hash": "0x<sha256>"}
```

- **`kind`** — a capability claim. The probe version says "the guest
  interface exists" (kernel sees the device); an operator override says
  "we assert this posture". Either way it is **self-attested**: nothing
  has verified a quote. A seller on ordinary hardware can write `tdx`
  by hand — the field is honest about being forgeable posture, and
  buyers must weight it as such.
- **`report_url`** — a pointer, not proof: the operator says fresh
  report/quote material is fetchable here. Nothing in v0.16.0 fetches or
  verifies it; existence of the field means the seller claims the
  machinery exists.
- **`evidence_hash`** — a commitment to operator-held evidence
  (e.g. a stored quote bundle) — binds the claim to a specific artifact
  the seller can produce on demand.

What the claim **proves**: nothing, yet. What it **signals**: the seller
runs hardware where verification *could* happen, and is willing to
publish evidence pointers — which raises the cost of lying (a false
claim invites a failed verification later).

## Verification depth — what real attestation would add

1. **Quote generation** (guest side): `/dev/tdx_guest` report or
   `/dev/sev-guest` `SNP_GET_REPORT`, with `REPORT_DATA` bound to a
   buyer-challenge nonce + the session's `task_hash` — freshness and
   session binding in one move.
2. **Collateral fetch**: TDX → PCCS/PCS (PCK chain, TCB info, QE
   identity, CRLs); SNP → AMD KDS (VCEK, ARK/ASK chain). Both are
   network-dependent and cacheable with expiry.
3. **Chain verification**: DCAP quote-verify (TDX) or report-signature
   verify against VCEK (SNP), TCB-status evaluation (up-to-date /
   out-of-date / revoked), and measurement policy (`MRTD`/`MEASUREMENT`
   against a known-good-image allowlist or a measured-boot log).
4. **Binding to the market session**: the verified `REPORT_DATA` must
   hash-bind the session so the quote can't be replayed from another
   purchase — `sha256(session_id ‖ task_hash ‖ buyer_nonce)`.

Every one of these is tractable — the hard part is operational: a
verification service the buyer trusts, or vendored DCAP/KDS clients
plus collateral caching, plus measurement-allowlist governance (who
signs off on "the seller's image is the image it claims").

## Roadmap to attested execution

- **This track (131)**: posture field, probe, docs. Buyers see claims;
  no verification happens.
- **Next**: a seller-side `/v1/tee/report` (or the advertised
  `report_url`) that returns a fresh quote with `REPORT_DATA` bound to
  a challenge — proves the guest-side path end to end.
- **Then**: buyer-side verify — KDS/PCS collateral + signature check,
  result recorded on the purchase (`tee_verified: true|failed|unverifiable`),
  reported through `peer_score` as another additive-evidence outcome.
- **Later**: measured-boot image registry — a signed allowlist of
  seller-image measurements, so `kind` stops being "runs a TEE" and
  becomes "runs the measured image".

## Field contract (what ships now)

- Advert: `attestation {kind, report_url?, evidence_hash?}` —
  emit-when-set; absent means no claim (which is itself honest — most
  hardware has none).
- Probe: `/dev/tdx_guest` → `tdx`, `/dev/sev-guest` → `sev-snp`,
  `/dev/tee0` → `trustzone`; `tee_kind` config overrides (incl.
  `none` to suppress); `tee_report_url` / `tee_evidence_hash` supply the
  optional pointers.
- Buyer side: the claim snapshots onto the purchase record at buy time
  (`tee_attestation`) — `market session` displays what the advert
  *claimed*, labeled self-attested in docs.
- Out of scope: quote fetch, collateral, chain verification, measured
  boot, attested execution.
