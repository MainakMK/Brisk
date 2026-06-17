# Self-Service Agent Rollout — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship new `brisk-agent` versions to the live PoP fleet from the dashboard — CI signs a binary, the control plane stores it, edges pull and self-update one PoP at a time, health-gated, with auto-rollback.

**Architecture:** Pull-based. Control plane holds a signed release + per-edge "desired version"; the agent polls, verifies an ed25519 signature, self-updates via systemd restart, and self-checks/rolls-back. A rollout engine opens one PoP's wave at a time and advances only after a soak window of health. The dashboard triggers + shows live progress. See spec: `docs/superpowers/specs/2026-06-16-agent-self-service-rollout-design.md`.

**Tech Stack:** Go 1.26 (two modules: `brisk-agent`, `brisk-control`), Postgres/TimescaleDB (goose migrations, pgx), chi router, React + TanStack Query (`brisk-dashboard`), GitHub Actions, ed25519 (stdlib `crypto/ed25519`).

---

## Environment & conventions (read first)

- **Not a git repo.** The template's `git commit` steps are replaced by a **build/test gate** at the end of each task. "Commit" = "the gate passes."
- **Go builds/tests run in Docker** (no host Go):
  - control: `docker run --rm -v //d/Webapps/Brisk/brisk-control://control -v brisk-gocache:/go -w //control golang:1.26 bash -c "<cmd>"`
  - agent: `docker run --rm -v //d/Webapps/Brisk/brisk-agent://agent -v brisk-gocache:/go -w //agent golang:1.26 bash -c "<cmd>"`
- **Dashboard typecheck:** `docker exec brisk-control-brisk-dashboard-1 npx tsc -b`. After new files, restart: `docker restart brisk-control-brisk-dashboard-1`.
- **Apply migrations / restart CP:** `docker compose -f D:\Webapps\Brisk\brisk-control\docker-compose.yml up -d --build brisk-control`, then restart tunnels: `docker compose -f D:\Webapps\Brisk\brisk-control\tunnels\docker-compose.yml restart`.
- **DB query:** `docker exec brisk-control-timescaledb-1 psql -U brisk -d brisk -c "<sql>"`.
- **Latest migration is `00029_server_tech.sql`** → new migrations start at `00030`.
- **Patterns to mirror** (read before writing): migration `internal/migrate/migrations/00023_server_resources.sql`; store `internal/store/servers.go` (`SetServerTech`, `serverCols`, `scanServer`); API handler `internal/api/agent.go` (heartbeat) + `internal/api/servers.go`; router `internal/api/router.go`; dashboard hook `brisk-dashboard/src/hooks/use-servers.ts`; dashboard component `brisk-dashboard/src/components/servers/server-ops.tsx`; jobs polling `brisk-dashboard/src/components/purge/jobs-table.tsx`; agent poll loop `brisk-agent/config/source.go` + `brisk-agent/main.go` (`configPollLoop`, `heartbeatLoop`); agent client `brisk-agent/client/client.go`.
- **TRAP (from CLAUDE.md):** a new per-zone/agent field must be added to the config struct AND `wireZone`/parse AND the emit/scan path, or it's silently dropped. For server fields: `Server` struct + `serverCols` + `scanServer` together.

---

## Progress

- [x] **Phase 1 — DONE.** `internal/signing` (Sign/Verify/VerifyAny) + 5 unit tests GREEN. `cmd/briskctl` (keygen + release push) builds; `keygen` verified.
- [x] **Phase 2 — code complete (builds + vets clean).** Migration `00030_agent_releases`; `internal/store/releases.go`; `internal/api/releases.go` (upload verifies signature server-side, list, delete, agent binary download); `config.AgentPubKeys` (BRISK_AGENT_PUBKEYS); routes wired. LIVE-CP upload acceptance (201/400/400) deferred to the batched backend verify.
- [x] **Phase 3 — core DONE.** `brisk-agent/selfupdate/` keys+verify+update + 5 unit tests GREEN (verify valid/tampered/wrong-key/garbage/fail-closed; Apply swap+prev+marker; self-check commit-on-healthy; rollback-after-maxRestarts). REMAINING: client `FetchRelease`/`DownloadBinary`, `main.go` wiring, `selfupdate-lab/` Docker harness.
- [x] **Phase 4 — core DONE.** Migration `00031_rollouts` (rollouts + rollout_targets + audit_log + one-active partial index); `internal/store/rollouts.go` (create/active/list/open/soak/finish/status/audit); `internal/rollout/engine.go` state machine + **5 unit tests GREEN** (happy one-at-a-time, unhealthy→halt, skip-offline, pause, scheduled-start). REMAINING (4.4): wire engine into `cmd/brisk-control/main.go` gated by `BRISK_ROLLOUT_ENABLED` + the shared `EdgeStateFn` (bundled with Phase 5 since the API + `/agent/release` share it).
- [x] **Phase 5 — control-plane side DONE (builds + vets clean).** `GET /agent/release` (per-edge desired version, no-op unless wave open); `internal/api/rollouts.go` (start/active/get/pause/resume/cancel/rollback, 409 on second active, signature-checked release ref); engine wired into `cmd/brisk-control/main.go` gated by `BRISK_ROLLOUT_ENABLED` with an `EdgeStateFn` over `ListServers` (online&!drained / agent_version / !unhealthy); config `RolloutEnabled`. ALSO completes Phase 4.4 (engine wiring).
- [x] **Phase 3 agent wiring — DONE (builds + vets clean).** `client.FetchRelease`/`DownloadBinary`; `main.go` boot `SelfCheckOnStart(...,agentSelfCheck)` (rollback→restart) + `go releaseLoop(base)` (poll `/agent/release` → download → `VerifyBinary` → `Apply` → `RestartSelf`); `releaseCheckInterval`=30s; `agentSelfCheck`=`/healthz` 200. **The whole pull loop now exists in code; both modules compile.**
- [ ] **`selfupdate-lab/` Docker harness** — end-to-end integration proof. **Scoped + ready to build** (mirror `lua-lab/`). Files:
  - `docker-compose.yml`: `db` (timescaledb), `control` (build ../brisk-control; env `BRISK_AGENT_PUBKEYS=<test pub>`, `BRISK_ROLLOUT_ENABLED=true`, `BRISK_HEALTH_ENABLED=false`, admin email/pw), `edge` (Dockerfile.edge).
  - `Dockerfile.edge`: nginx (serves `/healthz`) + the agent binary + a **fake-systemd** entrypoint = `while true; do /usr/local/bin/brisk-agent --config /etc/brisk/agent.yaml; sleep 1; done` (emulates Restart=always so `RestartSelf`/rollback work).
  - `seed/`: a tiny Go prog (or psql) that, after migrations, inserts a `servers` row (edge_id=edge1, status=online, agent_version=0.3.0) + an `agent_tokens` row using `token.Generate()`→`token.Prefix`/`token.Hash` (simple sha256, confirmed), and writes that token into the edge's `agent.yaml`.
  - Release seed: build a "v0.4.0" agent (any trivial change), `briskctl release push` it (CP verifies sig with the same `BRISK_AGENT_PUBKEYS`). For the **bad-sig** case, push with a wrong key → expect the agent to REFUSE. For **unhealthy**, push a build whose `/healthz` fails → expect auto-rollback to 0.3.0.
  - `run.sh` acceptance: `POST /rollouts {version:0.4.0,pops:[edge1],soak:5}` → assert edge heartbeats 0.4.0 + rollout `done`; bad-sig → edge stays 0.3.0; unhealthy → edge rolls back to 0.3.0 + rollout `failed`.
  - **NOTE:** ~6-7 files + a few iteration cycles. Each piece's contract is already unit-tested (15 green); the harness only proves the wires connect over real HTTP + restart. The **Phase-8 gated single-edge canary** is a *real* version of this same proof, so the fake harness is partly redundant with it.
- [x] **Phase 6 — dashboard DONE (tsc -b clean).** `types.ts` (AgentRelease/Rollout/RolloutTarget/RolloutDetail/StartRolloutInput); `hooks/use-releases.ts` (useReleases/useLatestRelease); `hooks/use-rollouts.ts` (useActiveRollout 2s poll + start/pause/resume/cancel/rollback); `components/servers/agent-version-card.tsx` (version-aware Deploy button: greyed when up-to-date or a rollout is live, lit on a new signed release; states no-release/all-up-to-date/split/new-available); `components/servers/deploy-dialog.tsx` (region-grouped PoP picker + soak-seconds, pre-checks behind edges, `Deploy to selected (N)`); `components/servers/rollout-progress.tsx` (live per-PoP panel: per-target icon/state/soak-countdown, split-fleet badge, Pause/Resume/Cancel/Roll-back); mounted both in `pages/servers.tsx`.
- [x] **Phase 7 — DONE (both modules build+vet+test green; dashboard tsc -b clean).**
  - `.github/workflows/agent-release.yml`: on tag `agent-v*` → setup-go 1.26 → build static linux agent with `-ldflags "-X main.agentVersion=<tag>"` (verified the stamp lands in the binary) → build `briskctl` → write `BRISK_AGENT_SIGNING_KEY` secret to a temp file → `briskctl release push` (uploads to `BRISK_CP_URL` with `BRISK_ADMIN_TOKEN`) → shred key. CI uploads only; a human clicks Deploy.
  - `agentVersion` const→var in `brisk-agent/main.go` so `-X` can stamp the release version (the whole self-update comparison depends on this).
  - Audit: `store.ListAudit` + `AuditEntry`; `GET /audit` admin endpoint (`a.listAudit`, `?limit=N`); `a.actorID(r)` threaded into all rollout audit writes + a new `release.upload` audit write; route wired.
  - Retention: hourly `PruneReleases(keep=10, protect=<versions running on any edge>)` goroutine in `cmd/brisk-control/main.go` (gated with the engine under `BRISK_ROLLOUT_ENABLED`).
  - Dashboard: `types.AuditEntry`; `hooks/use-audit.ts`; `components/servers/audit-feed.tsx` ("Deploy history" feed, hidden until first entry); mounted in `pages/servers.tsx`.
- [ ] **Phase 8 — live ship (NEEDS USER).** keygen → paste the 1–2 public keys into `brisk-agent/selfupdate/keys.go` `trustedKeysB64` + set `BRISK_AGENT_PUBKEYS` on the CP + store the private key as the `BRISK_AGENT_SIGNING_KEY` GitHub secret → gated SSH rollout of the self-update-capable agent NY→DE→BLR (byte-identical nginx.conf) → push the first signed release via CI → single-edge canary self-update → fleet.

---

## File structure (what gets created / changed)

**brisk-control (Go):**
- `internal/signing/signing.go` — ed25519 `Sign`/`Verify`/`VerifyAny` over a sha256 digest (+ test). **(done)**
- `internal/migrate/migrations/00030_agent_releases.sql` — `agent_releases` table.
- `internal/migrate/migrations/00031_rollouts.sql` — `rollouts` + `rollout_targets` (+ single-active index) + `audit_log`.
- `internal/store/releases.go` — release CRUD (insert/list/get/get-binary/prune).
- `internal/store/rollouts.go` — rollout + target CRUD + state transitions.
- `internal/api/releases.go` — `POST/GET/DELETE /releases`, `GET /releases/{v}/binary`.
- `internal/api/rollouts.go` — `POST /rollouts`, `GET /rollouts/{id}`, `POST /rollouts/{id}/{pause,resume,rollback}`, `GET /rollouts/active`.
- `internal/api/agent.go` — add `agentRelease` handler (`GET /agent/release`).
- `internal/rollout/engine.go` — wave state machine, soak-gate, scheduler, resume.
- `internal/rollout/regions.go` — region→continent grouping (reuse `internal/dns/regions.go`).
- `internal/api/router.go` — wire new routes.
- `cmd/brisk-control/main.go` — start the rollout engine goroutine.
- `cmd/briskctl/main.go` — `keygen`, `release push`, `deploy`, `rollback`.

**brisk-agent (Go):**
- `selfupdate/keys.go` — the two trusted ed25519 public keys (consts).
- `selfupdate/verify.go` — `VerifyBinary(data, shaHex, sig)` against trusted keys.
- `selfupdate/update.go` — `Apply` (download→swap→marker); `SelfCheckOnStart` → commit/rollback.
- `client/client.go` — add `FetchRelease()` + `DownloadBinary()`.
- `main.go` — call `SelfCheckOnStart()` at boot; add release-check to the poll loop.

**brisk-dashboard (React):**
- `src/lib/types.ts` — `AgentRelease`, `Rollout`, `RolloutTarget`, inputs.
- `src/hooks/use-releases.ts`, `src/hooks/use-rollouts.ts`.
- `src/components/servers/agent-version-card.tsx` — KPI + Deploy button.
- `src/components/servers/deploy-dialog.tsx` — region picker + soak + schedule.
- `src/components/servers/rollout-progress.tsx` — live per-PoP panel.
- `src/pages/servers.tsx` — mount the card + progress panel.

**CI / docs:**
- `.github/workflows/agent-release.yml` — build → sign → upload on tag.
- `docs/features/Brisk_Agent_Rollout_Process.html` — flip the "design, not built" banner when done.

---

# PHASE 1 — Signing library + briskctl keygen

**Goal:** prove the crypto in isolation: sign a digest, verify it, reject tampering/wrong key.

### Task 1.1: Control-plane signing package — **DONE (impl); test + gate remaining**

**Files:**
- Create: `brisk-control/internal/signing/signing.go` ✅ (Sign/Verify/VerifyAny implemented)
- Test: `brisk-control/internal/signing/signing_test.go`

- [ ] **Step 1: Write the failing test**

```go
// brisk-control/internal/signing/signing_test.go
package signing

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
)

func TestSignVerifyRoundTrip(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	digest := []byte("0123456789abcdef0123456789abcdef") // 32-byte sha256 stand-in
	sig := Sign(priv, digest)
	if !Verify(pub, digest, sig) {
		t.Fatal("valid signature must verify")
	}
}

func TestVerifyRejectsTamperedDigest(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	sig := Sign(priv, []byte("0123456789abcdef0123456789abcdef"))
	if Verify(pub, []byte("XXX3456789abcdef0123456789abcdef"), sig) {
		t.Fatal("tampered digest must NOT verify")
	}
}

func TestVerifyRejectsWrongKey(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	otherPub, _, _ := ed25519.GenerateKey(rand.Reader)
	digest := []byte("0123456789abcdef0123456789abcdef")
	if Verify(otherPub, digest, Sign(priv, digest)) {
		t.Fatal("wrong key must NOT verify")
	}
}

func TestVerifyAnyTrusted(t *testing.T) {
	pub1, priv1, _ := ed25519.GenerateKey(rand.Reader)
	pub2, _, _ := ed25519.GenerateKey(rand.Reader)
	digest := []byte("0123456789abcdef0123456789abcdef")
	sig := Sign(priv1, digest)
	if !VerifyAny([]ed25519.PublicKey{pub2, pub1}, digest, sig) {
		t.Fatal("must verify against any trusted key")
	}
	if VerifyAny([]ed25519.PublicKey{pub2}, digest, sig) {
		t.Fatal("must fail when no trusted key matches")
	}
}
```

- [ ] **Step 2: Run** (Docker control): `go test ./internal/signing/ -v 2>&1 | tail -12` → expect PASS (4 tests).
- [ ] **Step 3: Gate** — `go build ./... 2>&1 | tail -5` clean.

### Task 1.2: briskctl keygen + sign helpers

**Files:** Create `brisk-control/cmd/briskctl/main.go`

- [ ] **Step 1:** Implement `keygen` (prints ed25519 private+public base64) and `release push --version --bin --key [--notes]` that computes sha256, signs it via `internal/signing.Sign`, and POSTs `{version,sha256,signature,signed_by:"k1",notes,binary_b64}` to `${BRISK_CP_URL}/api/v1/releases` with `Authorization: Bearer ${BRISK_ADMIN_TOKEN}`. (`deploy`/`rollback` added in Phase 5.) Full code in spec §11 + the earlier draft.
- [ ] **Step 2: Gate** — `go build ./cmd/briskctl/ 2>&1 | tail -5` clean; run `keygen` locally → prints a keypair.

---

# PHASE 2 — Release store (DB + API)

### Task 2.1: Migration `00030_agent_releases.sql`
- [ ] Table `agent_releases(version PK, binary BYTEA, sha256, signature, signed_by, size_bytes, notes, uploaded_by, created_at)` (goose Up/Down, mirror 00023).
- [ ] **Gate:** rebuild CP; `psql "\d agent_releases"` shows 9 columns; logs `OK 00030`.

### Task 2.2: `internal/store/releases.go`
- [ ] `InsertRelease`, `ListReleases`, `LatestRelease`, `GetReleaseMeta`, `GetReleaseBinary`, `PruneReleases(keep,keepVersions)` (pgx, mirror `servers.go`). **Gate:** `go build ./...` clean.

### Task 2.3: `internal/api/releases.go` + routes
- [ ] `uploadRelease` (recompute sha256; **`signing.VerifyAny(trustedPubKeys, sum, sig)`** before store; 400 on mismatch, 409 on dup), `listReleases`, `getReleaseBinary` (`http.ServeContent` for Range/ETag), `deleteRelease` (block if running). Add `AgentPubKeys` to control config (env `BRISK_AGENT_PUBKEYS`, comma-sep base64). Wire: admin group `POST/GET/DELETE /releases`; agent-token group `GET /releases/{version}/binary`.
- [ ] **Gate:** rebuild; `briskctl release push` a dummy signed binary → 201; tampered sha → 400; unsigned → 400; `psql` shows the row.

---

# PHASE 3 — Agent self-update (Docker harness, no real edges)

### Task 3.1: `selfupdate/keys.go` + `selfupdate/verify.go` (+ test)
- [ ] `trustedKeysB64` consts (paste real pubkeys in Phase 8; placeholder now). `VerifyBinary(data, wantSHAHex, sigB64)`: sha match AND ed25519 verify vs any trusted key. Test: valid/tampered/wrong-key (inject test key into `trustedKeysB64`). **Gate:** `go test ./selfupdate/ -v` PASS.

### Task 3.2: `selfupdate/update.go` (+ test)
- [ ] `Paths{Binary,Prev,New,Marker}`, `DefaultPaths()`; `Apply(p,data,version)` (write .new, keep .prev, write marker `version\n0`, atomic rename, return shouldExit); `SelfCheckOnStart(p,maxRestarts,check)` (no marker→normal; check pass→clear marker; fail and n+1>=max→restore .prev, return rolledBack=true; else bump n); `RestartSelf()`=`os.Exit(0)`. Tests on temp dir: swap-keeps-prev, commit-on-healthy, rollback-after-maxRestarts. **Gate:** `go test ./selfupdate/ -v` PASS.

### Task 3.3: `client/client.go` — `FetchRelease()` + `DownloadBinary(version)`
- [ ] Mirror `Heartbeat` auth; `FetchRelease`→`GET /agent/release`→`ReleaseInfo{TargetVersion,URL,SHA256,Signature,SignedBy}`; `DownloadBinary`→`GET /releases/{v}/binary`→bytes. **Gate:** `go build ./...` clean.

### Task 3.4: wire into `main.go` + `selfupdate-lab/` harness
- [ ] At boot: `if selfupdate.SelfCheckOnStart(DefaultPaths(),3,agentSelfCheck){ RestartSelf() }`. Poll loop: `FetchRelease`→if target!=running→`DownloadBinary`→`VerifyBinary`→`Apply`→`RestartSelf`. `agentSelfCheck()` = `nginx -t` + GET `127.0.0.1/healthz`.
- [ ] Harness (mirror `lua-lab/`): CP + DB + 1 edge under a fake-systemd re-exec loop; seed v2 signed release. Assert: happy swap to v2; **bad-signature reject** (stays v1); **unhealthy v2 → rollback to v1**.
- [ ] **Gate:** harness acceptance script — all 3 scenarios PASS; edge nginx never down.

---

# PHASE 4 — Rollout engine

### Task 4.1: Migration `00031_rollouts.sql`
- [ ] `rollouts` + single-active partial unique index + `rollout_targets` + `audit_log` (see spec §4 / earlier draft). **Gate:** apply; a 2nd active rollout insert fails (unique).

### Task 4.2: `internal/store/rollouts.go`
- [ ] `CreateRollout` (tx: rollout + queued targets), `GetRollout`, `ListTargets`, `ActiveRollout`, `SetTargetState`, `SetRolloutStatus`, `NextQueuedTarget`, `WriteAudit`. **Gate:** `go build ./...` clean.

### Task 4.3: `internal/rollout/engine.go` + `regions.go` (+ tests)
- [ ] ~3s tick: scheduled→running at `scheduled_at`; pick next queued → set edge desired version; advance on (new-version heartbeat + healthy + `/healthz`) held for `soak_seconds`; timeout→target+rollout `failed`+reason+halt; offline→`skipped`; all done→`done`. Resume from DB on CP restart. `Continent(region)` helper. Table-driven tests with a fake store + fake edge-state. **Gate:** `go test ./internal/rollout/ -v` PASS.

### Task 4.4: wire engine into `cmd/brisk-control/main.go`
- [ ] Start `rollout.NewEngine(...).Run(ctx)` goroutine, gated by `BRISK_ROLLOUT_ENABLED` (default true; idle with no active rollout). **Gate:** rebuild; logs `rollout engine started`.

---

# PHASE 5 — API + agent /agent/release wiring

### Task 5.1: `GET /agent/release` (in `internal/api/agent.go`)
- [ ] From bearer token → edge; if active rollout target for this edge is "wave open" → return `{target_version,url,sha256,signature,signed_by}` from the release; else `{target_version:<current agent_version>}`. **Gate:** curl as edge token → no-op normally; new version when targeted.

### Task 5.2: `internal/api/rollouts.go` + briskctl deploy/rollback
- [ ] Admin handlers: `POST /rollouts` (409 if active), `GET /rollouts/active`, `GET /rollouts/{id}`, `POST /rollouts/{id}/{pause,resume,rollback}`; each writes audit. briskctl `deploy`/`rollback` call them. **Gate:** extend harness to 3 fake edges; `POST /rollouts` walks edge1→2→3 soaking; `GET /rollouts/{id}` shows advancing states; pause halts after current; rollback reverts.

---

# PHASE 6 — Dashboard

### Task 6.1: types + hooks
- [ ] `types.ts`: `AgentRelease`, `Rollout`, `RolloutTarget`, inputs. `use-releases.ts` (list+latest), `use-rollouts.ts` (`useActiveRollout` polls `/rollouts/active` 2s, `useStartRollout`, `usePauseRollout`, `useRollback`). **Gate:** `tsc -b` clean.

### Task 6.2: `agent-version-card.tsx` (KPI + Deploy button) → mount in `servers.tsx`
- [ ] Three states from `servers[].agent_version` vs latest release: up-to-date (grey "Deploy"), new (lit "Deploy v0.4.0"→dialog), split ("2 of 3 on v0.4.0" + Finish). Match `Brisk_Agent_Rollout_Process.html` §2. **Gate:** `tsc -b` clean + visual.

### Task 6.3: `deploy-dialog.tsx` (region picker)
- [ ] continent→PoP checkboxes (group by `Continent(region)`), soak input (90 default), optional schedule, `Deploy to selected (N)`→`useStartRollout`. **Gate:** `tsc -b` clean + create a rollout.

### Task 6.4: `rollout-progress.tsx` (live panel) → mount in `servers.tsx`
- [ ] per-PoP state pill (`queued→downloading→verifying→swapping→soaking(Xs)→✓/✗ reason`), overall bar, version/who/elapsed, Pause/Resume/Rollback, split badge; poll 2s. Match §4 + live-progress mockup. **Gate:** `tsc -b` clean + states advance on a running rollout + simulated failure shows reason.

---

# PHASE 7 — CI + audit + retention

### Task 7.1: `.github/workflows/agent-release.yml`
- [ ] On tag `v*`: build static agent → sign with `secrets.BRISK_AGENT_SIGNING_KEY` → POST to `secrets.BRISK_CP_URL/api/v1/releases` with `secrets.BRISK_DEPLOY_TOKEN`. **Upload-only**; `vars.BRISK_AUTO_CANARY` opt-in for auto-canary. **Gate:** test tag on a fork → 201 upload appears in `agent_releases` (document self-hosted-runner/tunnel if CI can't reach CP).

### Task 7.2: audit + retention
- [ ] Replace `audit` stub with real `WriteAudit`; after upload call `PruneReleases(10, runningVersions())`. **Gate:** upload 12 dummies → only 10 newest (+running) keep `binary`; `audit_log` populated.

---

# PHASE 8 — Ship self-update agent, then first canary

### Task 8.1: last gated SSH rollout
- [ ] Paste real pubkeys into `selfupdate/keys.go`; build; roll NY→DE→BLR with the existing byte-identical `deploy-tech.sh`-style gate (selfupdate inert → nginx.conf byte-identical). **Gate:** per-edge BEFORE==AFTER, /healthz 200, 3/3, new `agent_version` heartbeats.

### Task 8.2: first real self-update (canary)
- [ ] `briskctl release push` a new version → lands signed. Dashboard **Deploy to ONE edge** (BLR), soak 90s → watch panel `downloading→…→✓`; verify version + /healthz + nginx.conf unchanged.
- [ ] Push a deliberately-bad build → Deploy to canary → confirm **auto-rollback** + failure reason in panel.
- [ ] Deploy good version to remaining PoPs. Flip `Brisk_Agent_Rollout_Process.html` banner → LIVE; update CLAUDE.md + memory.

---

## Self-review
**Spec coverage:** signing→P1; store→P2; self-update→P3; engine→P4; API→P5; dashboard→P6; CI/audit/retention→P7; meta-ship→P8. All §1–§16 covered.
**Placeholders:** only `REPLACE_WITH_PUBLIC_KEY_*` in `selfupdate/keys.go` (real values from `briskctl keygen` in P8) — flagged, intentional.
**Type consistency:** `Sign/Verify/VerifyAny` (control) ↔ `VerifyBinary` (agent); `Release`/`ReleaseInfo`/`AgentRelease`; `Apply`/`SelfCheckOnStart`/`Paths`; `agent_version` is the single source of "running version" everywhere.
**Open items:** confirm partial-unique-index behavior (P4.1); CI→CP reachability (self-hosted runner vs tunnel) (P7.1).
