# Brisk — Self-Service Agent Rollout · Design Spec

**Date:** 2026-06-16
**Status:** Approved design → next step is the implementation plan (writing-plans).
**Visual reference:** `docs/features/Brisk_Agent_Rollout_Process.html` (final design diagram).

---

## 1. Goal

Let an operator ship a new `brisk-agent` version to the live PoP fleet **from the dashboard**, safely,
**without SSH keys, without a laptop push, and without me**. CI builds + signs the binary; the control
plane stores it; edges **pull and update themselves**, one PoP at a time, health-gated, with
auto-rollback. The Deploy button is **version-aware** (greyed when up to date, lit when a new version
exists) and rollouts can be **region-targeted** (e.g. update Asia + N. America now, Europe off-peak).

**Replaces:** today's SSH-push (`tunnels/deploy-*.sh`) for day-2 updates. SSH stays only for
first-time provisioning of a brand-new box.

### Success criteria
- Pushing a git tag produces a **signed** release in the control plane; nothing rolls out on its own.
- The Servers page shows **running vs available** agent version; the Deploy button lights up only when
  a newer signed release exists.
- Clicking Deploy → pick PoPs/regions + soak → edges self-update **one at a time**, each watched for a
  soak window before the next; a **live progress panel** shows every PoP's state and the **real error
  reason** on failure.
- A bad/unsigned binary is **refused** by edges. An unhealthy update **auto-rolls-back** on that edge
  and **halts** the rollout. nginx never stops serving throughout.
- Works with a separate dashboard VPS and firewalled edges (edges need **no inbound** for updates).

---

## 2. Scope

**In:** release store + ed25519 signing/verification; agent self-update; rollout engine (waves,
region targeting, health-gate/soak, pause/resume/rollback, concurrency guard, CP-restart resume,
scheduled start); control-plane API; dashboard (KPI, version-aware button, region picker, live
progress + errors); CI build→sign→upload (upload-only default; auto-canary opt-in); `briskctl`
(`keygen`, `release push`, `deploy`, `rollback`); audit logging; release retention.

**Out (future):** A/B traffic-percentage canaries within a single PoP; multi-arch builds (amd64 only
for now); automatic dependency/OS patching; rolling the **control-plane** app (that stays the existing
GitHub-Packages → Docker → control-VPS flow — out of scope here).

---

## 3. Architecture overview

Five units, each with one clear job:

| Unit | Where | Responsibility |
|---|---|---|
| **Release store + API** | `brisk-control/internal/release` + `internal/api/releases.go` | hold signed binaries; upload/list/serve; verify on upload |
| **Rollout engine** | `brisk-control/internal/rollout` | drive waves: open one PoP, soak-gate, advance/halt; pause/rollback; resume after CP restart; schedule |
| **Agent self-update** | `brisk-agent/selfupdate` | poll desired version → download → verify → swap → restart → self-check → auto-rollback |
| **Dashboard** | `brisk-dashboard/.../servers` | version KPI, Deploy button, region picker, live progress panel |
| **CI + briskctl** | `.github/workflows` + `brisk-control/cmd/briskctl` | build→sign→upload on tag; CLI for keygen/push/deploy/rollback |

**Trust model:** the network channel is authenticated (agent bearer token, as today), but the **real**
trust anchor is the **ed25519 signature** — an edge runs a binary only if it's signed by a key the
agent compiles in. So a compromised control plane/store still can't push a malicious agent.

**Independence (golden rule #3):** if the control plane is down, edges keep running their current
binary; an in-flight rollout pauses and resumes when it's back.

---

## 4. Data model (new)

All additive; existing `servers.agent_version` + `servers.region` are reused.

### `agent_releases`
| column | type | notes |
|---|---|---|
| `version` | TEXT PK | e.g. `0.4.0` (semver, unique) |
| `binary` | BYTEA | the static linux-amd64 binary (~20 MB) |
| `sha256` | TEXT | hex digest, verified on upload + by the agent |
| `signature` | TEXT | base64 ed25519 signature over the sha256 (or raw bytes) |
| `signed_by` | TEXT | which trusted key id signed it (`k1`/`k2`) |
| `size_bytes` | BIGINT | |
| `notes` | TEXT | release notes / changelog line |
| `uploaded_by` | TEXT | account id (audit) |
| `created_at` | TIMESTAMPTZ | |

**Retention:** keep the latest **N=10** releases (configurable); prune older binaries (`binary` set
NULL or row deleted) but never the version currently running on any edge.

### `rollouts`
| column | type | notes |
|---|---|---|
| `id` | BIGSERIAL PK | |
| `release_version` | TEXT FK→agent_releases | target version |
| `target_pops` | TEXT[] | edge_ids included in this rollout |
| `soak_seconds` | INT | per-edge watch window (default 90) |
| `status` | TEXT | `scheduled` / `running` / `paused` / `done` / `failed` / `cancelled` |
| `scheduled_at` | TIMESTAMPTZ NULL | if set, engine starts then (maintenance window) |
| `created_by` | TEXT | account id (audit) |
| `created_at` / `started_at` / `finished_at` | TIMESTAMPTZ | |
| `error_reason` | TEXT | set when `failed` |

**Concurrency guard:** at most **one** `running`/`paused` rollout at a time (DB partial unique index).
A new Deploy while one is active is rejected (409) with a clear message.

### `rollout_targets`
| column | type | notes |
|---|---|---|
| `rollout_id` | BIGINT FK | |
| `edge_id` | TEXT | the PoP |
| `from_version` / `to_version` | TEXT | |
| `state` | TEXT | `queued`/`downloading`/`verifying`/`swapping`/`soaking`/`done`/`failed`/`skipped` |
| `error_reason` | TEXT | human reason on failure (see §8) |
| `soak_until` | TIMESTAMPTZ NULL | when the soak window ends |
| `updated_at` | TIMESTAMPTZ | |
| PK | (`rollout_id`,`edge_id`) | |

### `audit_log` (reuse or add)
deploy started/paused/rolled-back/finished, release uploaded — `who`, `what`, `when`, `details`.

---

## 5. Signing & keys (ed25519)

- **You generate the keypair** (`briskctl keygen` → prints a private key + public key; or `openssl`/`age`).
  The private key is yours; nobody else creates it.
- **Private key** → GitHub repo **Settings → Secrets and variables → Actions** (`BRISK_AGENT_SIGNING_KEY`).
  Encrypted, write-only, auto-masked in logs, handed to CI only during a run. A local copy may live in a
  password manager for manual `briskctl` signing.
- **Public keys** → **two** compiled into the agent as constants (`trustedKeys = []ed25519.PublicKey{k1, k2}`).
  Two so you can **rotate**: sign with k2, the fleet already trusts it, retire k1 later — no fleet redeploy
  to *start* trusting the spare. (Adding a genuinely new key still needs an agent release, but the spare
  buys you a safe window.)
- **Verification:** the agent computes sha256 of the downloaded binary, checks it equals the release's
  `sha256`, then verifies the `signature` against the sha256 using each trusted key; **any** match = trusted,
  none = **refuse** (no swap).

---

## 6. Release store + control-plane API

- `POST /api/v1/releases` (admin or CI token) — multipart/stream: `version`, `binary`, `signature`,
  `signed_by`, `notes`. Server recomputes sha256, **verifies the signature** before storing, rejects on
  mismatch or duplicate version.
- `GET /api/v1/releases` — list (version, sha, size, signed_by, notes, created_at, `is_latest`).
- `GET /api/v1/releases/{version}/binary` — **agent-token-authenticated** stream of the binary (used by
  the self-updater). Supports `Range`/resume + `ETag`=sha256.
- `DELETE /api/v1/releases/{version}` — admin; blocked if any edge runs it.
- `GET /api/v1/agent/release` — **the per-edge desired-version endpoint** the agent polls (extends the
  existing poll loop). Returns `{ target_version, url, sha256, signature, signed_by }` **only when this
  edge's wave is open**; otherwise `{ target_version: <current> }` (no-op).

---

## 7. Agent self-update flow (`brisk-agent/selfupdate`)

The agent already polls config; add a release check on the same loop (≈ every poll, e.g. 30–60s):

```
1. GET /agent/release → desired version for THIS edge.
2. if desired == running  → done (no-op).
3. if wave open & desired != running:
     a. download binary (auth channel, Range-resumable) → /usr/local/bin/brisk-agent.new
     b. verify: sha256 == release.sha256  AND  signature verifies against a trusted key
        → fail ⇒ delete .new, report state=failed reason="signature/sha mismatch", DO NOT swap
     c. cp current → brisk-agent.prev ; write an "update in progress" marker file
     d. atomic rename .new → brisk-agent ; chmod 0755 ; exit(0)
4. systemd restarts the new binary (~3s).
5. NEW process startup self-check:
     - render config + `nginx -t`  + local `/healthz` 200, within a short window
     - PASS ⇒ clear the marker, report version=new + state=healthy ("committed")
     - FAIL or repeated restart (marker still present after N starts) ⇒
         restore brisk-agent.prev → exit ⇒ systemd brings back the OLD binary (auto-rollback),
         report state=failed reason="new agent unhealthy → rolled back to <prev>"
```

**Crash-loop guard:** the marker file + a restart counter mean a binary that won't even start is reverted
to `.prev` automatically, **without** needing the control plane. This is the safety net that makes
self-update safe even for a totally broken build.

**Heartbeat already reports `agent_version`** (shipped this session) — that's how the control plane and
dashboard see which version each edge is actually on, before/during/after.

---

## 8. Rollout engine (`brisk-control/internal/rollout`)

A small state machine, one goroutine, driven off `rollouts` + `rollout_targets` (so it survives a CP
restart by reading state back).

- **Start:** create `rollout` + `rollout_targets(queued)` for the selected PoPs (ordered; default by
  region/proximity, user order honored). If `scheduled_at` is set, wait until then.
- **Per wave (one PoP):** set that edge's target → it appears in `GET /agent/release` for that edge →
  agent self-updates → engine watches:
  - edge **heartbeats the new version** AND stays **health-probe healthy + `/healthz` 200** for
    `soak_seconds` → mark `done`, open next.
  - **timeout** (no healthy new-version heartbeat within e.g. `soak + 120s`) or edge reports `failed`
    → set rollout `failed`, record `error_reason`, **stop** (that edge already self-rolled-back).
- **Skip rules:** a targeted edge that's offline/drained at its turn → mark `skipped` (reason shown),
  continue with the rest; the rollout finishes `done` but the split is surfaced.
- **Controls:** `Pause` (finish the current edge, don't open the next), `Resume`, `Rollback` (= start a
  new rollout back to the previous version for the affected PoPs).
- **Error reasons (human):** `signature/sha mismatch`, `download timed out`, `nginx -t failed`,
  `new agent unhealthy within <soak>s → rolled back`, `edge offline at its turn (skipped)`,
  `edge unreachable / no heartbeat`.

### Health gate reuse
Uses the existing 5s health checker + `/healthz`. If the checker is disabled for an edge, the gate falls
back to "new-version heartbeat + `/healthz` 200 held for the soak window."

---

## 9. Dashboard (Servers page) — full UI spec

**(a) Agent-version KPI + Deploy button** — a panel at the top of the Servers page:
- **Up to date:** `brisk-agent · v0.4.0  ✓ all PoPs up to date` + a **greyed** `Deploy` button.
- **New version available:** `v0.3.0 → v0.4.0 available` with a **NEW** pill + a **lit indigo**
  `Deploy v0.4.0` button.
- **Partial / split:** `2 of 3 PoPs on v0.4.0 · 1 on v0.3.0` + a `Finish rollout` affordance.
- Data source: `servers.agent_version` (per edge) vs latest `agent_releases.version`.

**(b) Deploy dialog (region picker):** opens on click —
- checkbox list grouped by **continent → PoP** (Asia · BLR, N. America · NY, Europe · DE, …), derived
  from each server's `region` via the geo map; peak/current-traffic hint chips optional.
- **Soak seconds** field (default 90, adjustable).
- Optional **Schedule for later** (datetime) for maintenance windows.
- Primary button: `Deploy to selected (N)`.

**(c) Live progress panel** — appears while a rollout is `running`/`paused`:
- Per-PoP row with a state pill that advances:
  `queued → downloading → verifying → swapping → soaking (62s left) → ✓ done` or `✗ failed: <reason>`.
- An overall progress bar (`done/total`), the release version, who started it, elapsed time.
- **Pause / Resume / Rollback** buttons (admin only).
- On failure: the row shows the **exact reason** (from §8) and that the edge rolled back.
- Polls the rollout status endpoint (~2s) like the existing jobs tables.

**(d) Split-fleet indicator:** whenever PoPs run mixed versions, a persistent badge reminds you to
finish (so a partial rollout isn't forgotten).

---

## 10. CI (GitHub Actions)

On `git tag v*`:
1. build static `linux/amd64` binary (`golang:1.26`, CGO off) — same as today.
2. **sign** the sha256 with `BRISK_AGENT_SIGNING_KEY` (the GitHub secret).
3. **upload** to `POST /releases` using an admin/CI API token (also a GitHub secret) → **stop**.

Default = **upload-only** (button lights up; a human deploys). A repo variable
`BRISK_AUTO_CANARY=true` can opt into "upload + auto-start a 1-PoP canary, then wait."

---

## 11. `briskctl` (small Go CLI)

- `briskctl keygen` → prints a fresh ed25519 private + public key (you own it).
- `briskctl release push --version vX --bin ./brisk-agent --key <file>` → sign + upload (manual path).
- `briskctl deploy vX --pops BLR,NY --soak 90s` → start a rollout via the API (break-glass / scripts).
- `briskctl rollback` → roll the affected PoPs back to the previous version.
- Talks only to the **control-plane API** (admin token); never SSHes to an edge.

---

## 12. Security

- Upload endpoint: **admin/CI token only**; signature **verified server-side** before store.
- Binary download endpoint: **agent-token authenticated**; integrity = sha256; trust = signature.
- The signing private key never leaves GitHub's secret store (or your password manager).
- Edges need **no inbound** for updates (pull model) — keep them firewalled to :443 serving + outbound.
- All deploy actions **audited** (who/what/when). Deploy/pause/rollback require the admin RBAC scope.

---

## 13. Safety & edge cases (the easy-to-miss ones — now included)

- **Crash-loop / totally broken build** → marker-file + restart-count auto-rollback to `.prev`, no CP needed.
- **CP restarts mid-rollout** → engine resumes from `rollouts`/`rollout_targets` (no lost progress).
- **Edge offline/drained at its turn** → `skipped` with reason, rollout continues; split surfaced.
- **Two deploys at once** → blocked by the single-active-rollout guard (409).
- **Old + new agents coexist** (additive heartbeat/config fields) → a split fleet is safe and visible.
- **DB growth** → release retention (last N), pruning protects currently-running versions.
- **Download interrupted** → Range-resumable + sha verify before swap; never swap a partial file.
- **nginx never stops** → only the agent restarts (~6s); `nginx -t` before any reload; last-good kept.
- **Don't drop a live hostname** → rollout never removes an edge from DNS; (optional) drain-during-swap
  is unnecessary because nginx keeps serving, so it is **not** included by default.

---

## 14. Observability / audit
- `rollout_targets.state` history + `error_reason` give a full per-edge timeline.
- `audit_log` records every deploy/pause/rollback/upload with the actor.
- Existing stats/heartbeat already expose live version per edge for the KPI.

---

## 15. Testing strategy
- **Go unit tests:** sign/verify (good, tampered, wrong-key, both trusted keys); rollout state machine
  (advance on healthy, halt on timeout/fail, skip offline, pause/resume, single-active guard,
  CP-restart resume); self-update verify + self-check + auto-rollback logic.
- **Local Docker harness** (3 fake edges + control plane + DB): happy path; **bad-signature reject**;
  **unhealthy → auto-rollback + halt**; **pause/resume**; **region-targeted partial + split indicator**;
  **scheduled start**.
- **Dashboard:** `tsc` clean + visual check of KPI/button states, region picker, live progress + a
  simulated failure row.
- **Live proof (gated):** publish a real signed release, deploy to **one** edge first (canary), watch the
  soak + auto-rollback path on a deliberately-bad build, then a real version across the fleet.

---

## 16. Shipping THIS feature (meta)
- The control plane gains the new tables + engine + endpoints (additive migration). The agent gains the
  `selfupdate` package — **inert until a release exists and a wave opens**, so deploying the
  selfupdate-capable agent is itself a normal byte-identical gated rollout (today's process), one last time
  by SSH. After that, the fleet updates itself.
- First real self-update is validated on **one edge** (canary) before fleet-wide.

---

## 17. Open questions for plan stage
- Exact poll interval for the release check (reuse config-poll cadence vs separate).
- Whether `audit_log` is a new table or extends an existing one.
- Continent grouping: derive from `region` via geo map vs a small explicit region→continent table.
- `briskctl` distribution (built alongside brisk-control; not shipped to edges).
