# Brisk — selfupdate-lab: the self-update test sandbox (BUILD LATER, when needed)

> **Status:** deliberately **not built** (2026-06-17). Everything it would test is already **proven
> live** — your real fleet (NY/FRA/BLR) self-updated to agent 0.4.1 successfully during the first
> canary. This doc records *why we skipped it*, *when to build it*, and *exactly what to build* so
> future-you (or whoever) can pick it up cold.

---

## In plain English

The **selfupdate-lab** is a **practice dummy** for the agent self-update feature: a fake control
plane + a fake edge server running entirely on your laptop in Docker, so you can test risky
self-update changes **without ever touching your real, live servers**.

Your live PoPs prove self-update **worked once, today**. This sandbox proves it **keeps working,
every time, safely** — including the dangerous cases you'd never want to trigger on production.

---

## Do I need it right now?

**No.** You are feature-complete and it's proven live. Park this.

## When SHOULD I build it?

Build it the day **any** of these becomes true:

1. **You start changing the agent / self-update code again.** The sandbox catches bugs on your
   laptop *before* they can reach a real edge. (Reminder: the download-timeout bug we hit today
   *did* reach a live edge — it was safe because the agent refused the bad download, but a sandbox
   would have caught it first.)
2. **More than one person works on the agent.** Their change gets auto-checked before going near
   production.
3. **You turn on GitHub auto-deploy** (`docs/future/Brisk_Agent_AutoDeploy_When_CP_On_Real_Server.md`).
   You'll want an automatic test gate so a bad build is caught *before* it's offered to the fleet.
4. **Months from now**, when the details are forgotten — the sandbox re-confirms everything still
   works without re-reading code.

## What it helps with (that the live PoPs don't)

| Live PoPs | selfupdate-lab |
|-----------|----------------|
| Prove it worked **once** | Prove it works **every time** (regression net) |
| Can't safely test failure cases | **Deliberately** tests bad-signature + broken-update |
| A bug can reach a real edge | **Zero blast radius** — throwaway fake server |
| Manual, one-off | Automatic, runs in seconds on every change |

The two scenarios it exists to prove (the scary ones):
- **Forged / bad signature → must be REFUSED** (the edge stays on the old version).
- **Broken / unhealthy new version → must AUTO-ROLL-BACK** to the old version.

Plus the happy path: **healthy new version → swaps in cleanly**, nginx never goes down.

---

## What to build (scoped + ready — mirror `lua-lab/`)

A `selfupdate-lab/` folder next to the other Docker labs, ~6–7 small files:

- **`docker-compose.yml`** — three services:
  - `db` (timescaledb), `control` (build `../brisk-control`; env `BRISK_AGENT_PUBKEYS=<test pubkey>`,
    `BRISK_ROLLOUT_ENABLED=true`, `BRISK_HEALTH_ENABLED=false`, admin email/pw),
  - `edge` (built from `Dockerfile.edge`).
- **`Dockerfile.edge`** — nginx (serves `/healthz`) + the agent binary + a **fake-systemd**
  entrypoint: `while true; do /usr/local/bin/brisk-agent --config /etc/brisk/agent.yaml; sleep 1; done`
  — this emulates systemd's `Restart=always` so the agent's `RestartSelf()` / rollback actually
  works in the test.
- **`seed/`** — after migrations, insert a `servers` row (edge_id=edge1, status=online,
  agent_version=0.3.0) + an `agent_tokens` row (use `token.Generate()` → `token.Prefix`/`token.Hash`,
  a simple sha256), and write that token into the edge's `agent.yaml`.
- **Release seeds** — build a trivial **v0.4.0** agent and `briskctl release push` it (the control
  plane verifies the signature with the same test `BRISK_AGENT_PUBKEYS`).
  - **bad-sig case:** push signed with a *wrong* key → expect the edge to REFUSE it.
  - **unhealthy case:** push a build whose `/healthz` fails → expect auto-rollback to 0.3.0.
- **`run.sh`** acceptance:
  - `POST /rollouts {version:0.4.0, pops:[edge1], soak:5}` → assert the edge heartbeats 0.4.0 and the
    rollout reaches `done`.
  - bad-sig → edge stays 0.3.0.
  - unhealthy → edge rolls back to 0.3.0 and the rollout reaches `failed`.

**Why it's lower priority:** each individual piece is already covered by **15 green unit tests**
(`internal/signing` ×5, `selfupdate` ×5, `internal/rollout` ×5). The harness only proves the wires
connect over real HTTP + a real restart — which the **live canary already demonstrated end-to-end**.
The Phase-8 live single-edge canary is, in effect, a *real* version of this same proof.

---

## How to pick it up later

When the time comes, just say **"build the selfupdate-lab"** and point here. The full original plan
(with file-by-file detail) also lives in the implementation plan:
`docs/superpowers/plans/2026-06-16-agent-self-service-rollout.md` (the `selfupdate-lab/ Docker
harness` item) and the spec `docs/superpowers/specs/2026-06-16-agent-self-service-rollout-design.md`.

**Related:**
- The feature it tests (built + live): memory `agent-self-service-rollout-built`.
- Turning on auto-deploy (a trigger to build this): `Brisk_Agent_AutoDeploy_When_CP_On_Real_Server.md`.
