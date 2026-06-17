# Brisk CDN — Phase 3.7 / Step 1 Build Prompt (Real Agents on Live Edges + Reachable Laptop Control Plane)

**For Claude Code.** Context in the repo: `CLAUDE.md` + `docs/Brisk_Phase1_Build_Spec.md` + all Phase‑2 prompts + all Phase‑3 prompts + `docs/Brisk_Phase3_Runbook.md` + `dashboard-reference/`. **Phases 1–3 are complete and the data plane is LIVE and real:** 3 regional edges — `US-NY-prod01` (104.248.231.144), `EU-FRA-prod01` (188.245.225.172), `BLR1-01` (139.59.78.21) — serve `cdn.a2zjav.com` over HTTPS with geo Smart‑Record routing (TTL 15s), origin caching of `test.mainakghosh.com`, wildcard `*.a2zjav.com` TLS, ~24–32s health failover, and dashboard drain. **BUT the control plane is still Docker‑on‑the‑laptop, so the real edges can't heartbeat it** — edges are kept "online" by a temporary **heartbeat‑refresher hack** (a DB poke), their nginx was **hand‑written** (not agent‑pulled), and **purge/config/stats fan‑out has only been proven on local stand‑ins, not the live fleet.**

> **Read `CLAUDE.md`, `docs/Brisk_Phase3_Runbook.md`, and the Phase‑2 Step‑2/Step‑3 prompts (token auth + pull‑config) first.** This is **Step 1 of 3 in Phase 3.7 (Productionize the Control Plane)**. **Decision from the user: the control plane STAYS on the laptop for now** (fast iteration, no redeploy loop); it will be deployed to a public VPS *finally, later*. So this step makes the **real edges reach the laptop control plane reliably** and runs the **real `brisk-agent`** on them — designed so the future public‑VPS move is a **one‑line endpoint swap**, not a rebuild. Test against the live fleet. Pass the acceptance tests, stop before Step 2.

## Step 3.7.1 goal (one line)
Get the **real `brisk-agent` running on all 3 live edges**, connected to the **laptop control plane over a stable tunnel/VPN**, producing the **same nginx behavior as the current hand‑written config** (template parity), reporting **real heartbeats** (drop the refresher hack), and **pulling config live** — with the control‑plane endpoint as a **single swappable config value**.

## ⚠️ This touches the LIVE fleet — keep `cdn.a2zjav.com` up
The 3 edges are serving live. Roll the agent out **one edge at a time**, verify each still serves identically before moving on, and keep a rollback (the current hand‑written nginx) ready per edge. Never leave the live set with a broken edge.

---

## The connectivity problem (and the design that makes the future deploy trivial)
The agents need to reach the laptop control plane for: the **config pull API** (Phase‑2 Step‑3, HTTP) and **NATS** (Phase‑2 Step‑5, purge). A laptop behind NAT isn't directly reachable, so we need a stable path from each cloud edge back to the laptop. **Make the control‑plane base URL + NATS URL a single config value per agent** (`BRISK_CONTROL_URL`, `BRISK_NATS_URL`) so that "laptop now → public VPS later" is just changing those values.

**Primary recommendation — mesh VPN (Tailscale):** install Tailscale on the laptop + each edge → every node gets a stable private IP on the tailnet → agents point `BRISK_CONTROL_URL`/`BRISK_NATS_URL` at the **laptop's Tailscale IP**. It auto‑reconnects, survives the laptop's IP changes/sleep, is encrypted, and is the least‑fragile option for multiple cloud nodes reaching one roaming laptop. When you later deploy the control plane to a public VPS, you simply repoint those URLs at the VPS's address (or keep it on the tailnet) — one‑line change.

**Fallback — autossh + systemd reverse tunnels (no third‑party account):** on each edge, an `autossh`‑supervised **reverse tunnel** brings the laptop's control‑plane API + NATS ports to the edge's `localhost` (the exact pattern used back in Phase‑2 local testing: `VPS:127.0.0.1:8088 → laptop`). Use a **systemd unit with `ServerAliveInterval`/`ServerAliveCountMax` + `ExitOnForwardFailure` + `Restart=always`** so it auto‑reconnects and never goes stale. Agents point at `localhost:<tunneled-port>`. More moving parts than Tailscale across 3 roaming‑laptop links, but zero external dependency.

> Pick **one** (recommend Tailscale for reliability with 3 edges); document the choice + setup in the runbook. Either way, the agent config is endpoint‑agnostic.

**Resilience is already built‑in (lean on it):** the agent keeps serving from **last‑known‑good config + local cache** if the control plane is unreachable (Phase‑2 Step‑3), and **NATS JetStream replays missed purges on reconnect** (Phase‑2 Step‑5). So if the laptop sleeps/disconnects, **edges keep serving live traffic**; config/purge/stats just resume when the laptop's back. This is exactly why a laptop control plane is acceptable for now — the data plane is decoupled from control‑plane availability.

## Part 1 — Connectivity (laptop ↔ 3 edges)
- Stand up the chosen path (Tailscale tailnet **or** autossh+systemd reverse tunnels) so each edge can reach the laptop's **control API** and **NATS**.
- Lock it down: tunnel/VPN carries only the control API + NATS; least‑privilege; the control plane is **not** exposed to the open internet (it stays private on the tunnel/tailnet — which also means admin auth is Step 3's job, not a blocker now, but note the control API must not be left publicly bound).
- Verify from each edge: it can hit `BRISK_CONTROL_URL/health` and connect to `BRISK_NATS_URL`.

## Part 2 — Real `brisk-agent` on each edge (template parity — the careful part)
The live edges run **hand‑written nginx** today. The agent must produce **functionally identical** output before we trust it on the live set:
- Roll the agent onto **one edge first** (suggest `US-NY-prod01`, not the BLR1‑01 home box). Provision/run `brisk-agent` (the Phase‑1/2 binary) pointed at `BRISK_CONTROL_URL` with that edge's **agent token** (Phase‑2 Step‑2 token auth).
- The agent pulls config and renders nginx from its **template** (`server.tmpl`). **Critical: the template must reproduce the current hand‑written behavior** — the report lists these as the live config: **wildcard `*.a2zjav.com` HTTPS (+ apex), HSTS, 80→443**, **static assets cached 30d with cookies stripped**, **HTML cached 10m with logged‑in/admin bypass**, **stale‑while‑revalidate + cache‑lock (one origin fetch under load)**, **branded headers** (`X-Brisk-Edge`/`X-Brisk-Cache`), the **`cdn.a2zjav.com` origin = `test.mainakghosh.com`** mapping, slice module for video, Brotli, `aio threads`. Diff the agent‑rendered nginx against the hand‑written one and reconcile until they match.
- Validate the agent‑rendered config (`nginx -t`) and reload; confirm the edge **still serves identically** (cache HIT/MISS, the 147KB test image, video, headers, TLS). Only then proceed to the next edge.
- **TLS note:** TLS was set up by hand (acme.sh DNS‑01). For *this* step, the agent can **adopt/keep the existing certs** (don't break wildcard TLS); fully moving DNS‑01 issuance/renewal *ownership* into the agent is **Step 2**. Just don't regress TLS here.
- Repeat for the other two edges, one at a time, each verified before the next.

## Part 3 — Real heartbeats → drop the refresher hack
- With the agent running, each edge sends **real authenticated heartbeats** (Phase‑2 Step‑2) → the control plane marks it online from a genuine signal, not the DB poke.
- **Remove the heartbeat‑refresher hack** once all three beat for real. Confirm the dashboard shows all 3 online from real heartbeats, and that the Phase‑3 **effective‑status** rule (`online AND heartbeat fresh ≤60s`) + DNS rotation now run off the real signal (a real edge going quiet correctly drops from rotation).
- Sanity: the Phase‑3 health checker + DNS reconciler keep working with real heartbeats (no churn, no false drops).

## Part 4 — Config pull live (prove the loop)
- Confirm each agent **pulls config via the control plane** with ETag/304, jitter/backoff, last‑known‑good fallback (Phase‑2 Step‑3) — over the tunnel/VPN.
- Prove it end‑to‑end: make a **config change** (e.g. edit a zone setting / bump a cache TTL in the dashboard) → `config_version` bumps → **all 3 real edges re‑pull and converge** within the poll interval → `nginx -t` + reload on each, live site stays up. (Full purge + stats fan‑out verification is **Step 2** — here, prove config pull works live on the real fleet.)

## Part 5 — Make the future public deploy a one‑liner + document
- Ensure `BRISK_CONTROL_URL` + `BRISK_NATS_URL` are the **only** things that change when the control plane later moves to a public VPS. No hardcoded laptop addresses anywhere in the agent.
- Update `docs/Brisk_Phase3_Runbook.md` (or a new `docs/Control_Plane_Ops.md`): the connectivity setup (Tailscale/autossh), how to start/stop the laptop control plane, the resilience behavior (edges keep serving if the laptop is offline), the per‑edge agent rollout + rollback, and the **"when we go public, change these 2 URLs"** note.

---

## Acceptance tests (Step 3.7.1 definition of done — live fleet)
```bash
# 1) Connectivity: from EACH edge, reach the laptop control plane + NATS
#    edge$ curl -s $BRISK_CONTROL_URL/health   -> ok
#    edge$ (NATS reachable over $BRISK_NATS_URL)
# 2) Agent rollout (one edge at a time): brisk-agent running on all 3; each renders nginx from server.tmpl
#    that MATCHES the prior hand-written behavior (wildcard TLS+HSTS+80->443, static 30d cookie-strip,
#    HTML 10m logged-in-bypass, SWR+cache-lock, branded headers, origin=test.mainakghosh.com, video slices, brotli, aio)
# 3) Each edge still serves identically post-agent: cdn.a2zjav.com HTTPS, cache HIT/MISS, 147KB image, video, X-Brisk-* headers
# 4) Real heartbeats: all 3 online from genuine agent heartbeats; heartbeat-refresher hack REMOVED; dashboard shows real online
# 5) Effective-status off real signal: stop one agent -> that edge goes stale -> correctly drops from DNS rotation (Phase-3) -> restart -> returns
# 6) Config pull live: change a zone setting in the dashboard -> config_version bumps -> ALL 3 real edges re-pull + converge
#    (nginx -t + reload each) -> cdn.a2zjav.com stays up throughout
# 7) Resilience: stop the laptop control plane -> all 3 edges KEEP serving live (last-known-good + cache) -> restart -> agents reconnect, no flap
# 8) Endpoint swap-ready: BRISK_CONTROL_URL/BRISK_NATS_URL are the only deploy-time changes; no hardcoded laptop IPs
# 9) Live-site safety: cdn.a2zjav.com served correctly the entire rollout; per-edge rollback (hand-written nginx) documented
# 10) Runbook updated with connectivity + ops + the "go public = change 2 URLs" note
```
**Done when:** all 3 live edges run the **real `brisk-agent`** against the **laptop control plane over a stable tunnel/VPN**, render nginx that **matches the hand‑written behavior**, report **real heartbeats** (refresher hack gone), and **pull config changes live** with the whole fleet converging — while `cdn.a2zjav.com` stays up throughout, edges keep serving if the laptop drops, and the future public‑VPS move is a **one‑line endpoint swap**.

---

## Pitfalls (do not skip)
1. **Template parity is everything** — the agent‑rendered nginx must match the live hand‑written config (TLS/HSTS/redirect, static 30d cookie‑strip, HTML 10m logged‑in bypass, SWR+cache‑lock, branded headers, origin mapping, video slices, brotli, aio). Diff and reconcile before trusting it live. A mismatch = a regression on a live site.
2. **One edge at a time + rollback** — never roll the agent onto all 3 at once; verify each, keep the hand‑written nginx as a per‑edge rollback.
3. **Don't regress TLS** — keep the working wildcard certs; agent‑owned DNS‑01 issuance is Step 2, not now.
4. **Endpoint as config, not hardcoded** — `BRISK_CONTROL_URL`/`BRISK_NATS_URL` only; the future public deploy must be a one‑line change.
5. **Control plane stays private** — it's reachable only over the tunnel/tailnet, not bound to the public internet (admin auth is Step 3; until then, don't expose it openly).
6. **Lean on existing resilience** — last‑known‑good config + JetStream replay mean a sleeping laptop doesn't drop live traffic; verify this rather than fighting it.
7. **Real heartbeats before removing the hack** — confirm genuine heartbeats work on all 3, *then* delete the refresher; don't remove it first and blackhole rotation.
8. **Tunnel must auto‑reconnect** — Tailscale handles it; if autossh, use systemd + `ServerAliveInterval`/`ExitOnForwardFailure`/`Restart=always` so it never goes stale.
9. **Scope** — connectivity + real agents + real heartbeats + live config pull only. **Purge + stats fan‑out verification = Step 2; agent‑owned TLS = Step 2; admin auth = Step 3.**

## Next — Step 3.7.2 (do NOT start)
**Live fan‑out + agent‑owned TLS:** verify **purge fan‑out over NATS to all 3 real edges** (with JetStream durability across laptop/tunnel drops) and **stats fan‑out** (per‑PoP + aggregate in Overview/Analytics from the real fleet), then move **wildcard DNS‑01 TLS issuance/renewal ownership into the agent** (replacing the hand‑set acme.sh). Wait for the user's go‑ahead and a Step 3.7.2 prompt.

## Then — Step 3.7.3 (do NOT start)
**Admin auth + deploy‑readiness:** wire the dashboard/control‑plane `authHeader()` seam + admin tokens (mandatory before any public exposure), and document the one‑step laptop→public‑VPS cutover — closing out Phase 3.7 so Phase 4 builds on a self‑driving, secured control plane.
