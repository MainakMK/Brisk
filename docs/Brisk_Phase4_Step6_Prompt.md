# Brisk CDN — Phase 4 / Step 6 Build Prompt (Hardening + Cleanup Sweep) — closes Phase 4

**For Claude Code.** Context: `CLAUDE.md` + `docs/Brisk_Phase1_Build_Spec.md` + all Phase‑2/3/3.7 prompts + the Phase‑4 Step‑1→5 prompts + `docs/Control_Plane_Ops.md` + `dashboard-reference/`. **Phase 4 Steps 1–5 are complete:** multi‑tenant host routing, custom domains + per‑domain auto‑TLS (SNI), per‑zone origin shield, per‑zone WAF (OWASP Coraza + nginx rate limiting), and a Lua programmable edge (`lua-nginx-module` dynamic on the nginx.org build) that **enforces custom cache rules + header transforms**. Several capabilities are **built and proven in the lab but dormant on the live fleet by default** (shield, WAF, Lua) — turning them on per‑zone is gated/operational. A backlog has accumulated across phases.

> **Read `CLAUDE.md`, all the Phase‑4 prompts, and the Phase‑2 reports (for the backlog items) first.** This is **Step 6 of Phase 4 — the finale.** It rolls the remaining dormant capability onto the live fleet, fills the carried backlog (esp. a **real logs API** + the **origin‑offload counters**), and runs a **security/perf audit**. Test locally; roll live changes one edge at a time. After this, **Phase 4 is complete** and Phase 5 (customer portal + billing) begins.

## Step 4.6 goal (one line)
Close Phase 4: **roll the Lua module onto the live fleet**, ship a **real edge logs API** (replacing the placeholder), make the **origin‑shield offload metric real** (origin‑tier counters), fold in the **carried Phase‑2/3 backlog**, and run a **security + performance audit** of the whole multi‑tenant + WAF + TLS surface.

## ✅ Test locally + careful live rollout
Build/verify locally in Docker; roll the Lua‑module live rollout **one edge at a time** (drain→deploy→verify→undrain), live zones byte‑identical, rollback ready — the proven pattern.

---

## Part 1 — Roll the Lua module onto the live fleet (finish Step 5's live side)
- Deploy `lua-nginx-module` (+ deps) onto NY → DE → BLR via the `EnsureLua` recipe from Step 5, **one edge at a time**, verify each serves byte‑identical (`cdn.a2zjav.com` 200, `Server: Brisk`, cache HIT, video, TLS, WAF, healthz) before the next. After this, custom cache rules + header transforms are enforceable on production zones (still **opt‑in per zone**; the live zone's behavior unchanged unless a rule says otherwise).
- Same discipline for enabling shield/WAF on real tenant zones is **operational** (not forced on) — document the enablement runbook; don't flip production zones on without intent.

## Part 2 — Real logs API (replace the Logs placeholder)
The Logs page has been an honest "coming soon" since Phase‑2 Step 6.0. Build the real thing, per CDN log best practice:
- **Structured JSON access logging** at the edge: a `log_format` capturing the fields that matter — time, client IP, country (Part 5 GeoIP), method, host (zone), path, status, bytes, **cache status** (`$upstream_cache_status` → HIT/MISS/BYPASS/EXPIRED), referer, user‑agent, **upstream timing** (`$request_time`, `$upstream_response_time`), edge id, a **request ID** (`X-Brisk-Request-Id`) for correlation, and the shield/WAF disposition where relevant.
- **Shipping pipeline:** the agent tails the structured log and ships entries to the control plane via the **existing batched, bounded, drop‑oldest pipeline** (reuse the Phase‑2 Step‑4 stats shipping pattern — buffer + flush on reconnect; don't block serving; survive tunnel drops). Store in a **Timescale hypertable** (`request_logs`) with **retention** (logs are high‑volume — set a sane retention like 7–30 days + optional compression; don't keep forever).
- **Logs API + UI:** `GET /api/v1/zones/{id}/logs?from&to&filters` (status/path/ip/cache/country) + an admin cross‑tenant view; the dashboard **Logs page** becomes a real, filterable, near‑real‑time request view (recent‑first, paginated/virtualized — don't render thousands of rows client‑side). Tenant‑scoped via RBAC.
- **Honesty + cost:** near‑real‑time (seconds, batched), not instant‑streaming; note retention; avoid heavy sampling (full‑fidelity matters for security — but cap volume sanely). Per‑zone so a tenant sees only their own requests.

## Part 3 — Origin-shield offload metric (make it real)
Step 3 flagged that the stats schema lacked **origin‑tier counters**, so the "origin offload" number was a placeholder. Add them:
- Count, per zone: **edge→shield requests**, **shield→origin requests** (the collapsed ones), and shield‑tier HIT/MISS — emitted from the shield + edges (via `X-Brisk-Shield` / upstream cache status) into stats.
- Compute **real origin offload** = requests served without hitting origin ÷ total → surface the genuine "you saved N% origin load" number in Analytics (replacing the flagged placeholder). This is the shield's selling‑point metric — now truthful.

## Part 4 — Carried Phase-2/3 backlog (fold in)
- **`PUT /api/v1/zones/{id}/rules/{rid}`** + a **bulk reorder** endpoint → make cache‑rule edits/reorder atomic (today reorder is delete+recreate, churning IDs). Update the dashboard rules editor to use it.
- **`GET /api/v1/zones/{id}/servers`** → the inverse lookup (which edges serve a zone) instead of unioning each server's `/zones`.
- **Network‑aggregate `/stats`** → a true network‑wide aggregate endpoint (today "All PoPs" is merged client‑side); offload the merge to the server.
- **Stats schema depth:** **status‑code breakdown (2xx/4xx/5xx)**, **top paths**, and **latency percentiles** (p50/p95/p99 from `$request_time`/`$upstream_response_time`) — the analytics dimensions flagged missing since Step 6.4. Wire into Analytics (replace the "not yet available" notes).
- (Geo/country lands in Part 5.)

## Part 5 — GeoIP + WAF/rate-limit enhancements (the Step-4 follow-ups)
- **GeoIP** at the edge (MaxMind GeoLite2 or equiv, ABI/license‑aware): resolve client country/ASN → available to logs, analytics (geo breakdown — the flagged map/country dimension), and **WAF country rules** (block/allow/challenge by country, the Step‑4 deferred item).
- **Response‑aware (errors‑only) rate limiting:** count only error responses (401/403) for login/OTP‑style limits (the Step‑4 best‑practice follow‑up) so legitimate users aren't throttled.

## Part 6 — Security + performance audit (the hardening)
A real audit of the whole surface built across Phases 1–4, per CDN hardening best practice:
- **Origin lockdown:** the #1 CDN security gap is leaving the origin reachable directly (bypassing the edge → WAF/rate‑limit/TLS all irrelevant). Document + where feasible implement origin protection (allowlist Brisk egress IPs / a shared secret header the origin checks / mTLS to origin) so traffic **must** come through Brisk. At minimum, document the customer guidance + Brisk's egress IPs.
- **Control‑plane + admin auth review:** confirm admin auth (Phase‑3.7 Step‑3) is solid, tenant scoping can't leak cross‑tenant (re‑test the RBAC boundary), secrets (Bunny key, certs, tokens) never logged/committed, NATS/control‑plane still private (tunnel‑only) until the deliberate public deploy.
- **TLS posture:** TLS 1.3, HSTS, no weak ciphers; per‑domain + wildcard certs renewing; OCSP/staple as configured.
- **WAF validation:** confirm CRS actually blocks the OWASP Top 10 across zones; rate limits hold; fail‑open/closed behaves; body‑inspect cap sane.
- **Multi‑tenant isolation audit:** no cross‑tenant cache bleed (`$host` keys), no cross‑tenant cert/vhost confusion (SNI), no cross‑tenant rule/log/stats leakage (RBAC) — explicitly re‑verify the tenant boundary end‑to‑end.
- **Perf:** confirm the added layers (WAF, Lua, shield, logging) haven't regressed cache‑HIT latency or throughput meaningfully; check worker/cpu/memory under load.
- Produce a short **audit report** (`docs/Security_Audit_Phase4.md`) + fix anything critical found.

## Part 7 — Docs + Phase-4 close-out
- Update `docs/Control_Plane_Ops.md` + `CLAUDE.md`: the full Phase‑4 feature set, the live‑enablement runbooks (shield/WAF/Lua per zone), the logs pipeline + retention, the origin‑lockdown guidance, and the security‑audit findings.
- Confirm the backlog is now **empty** (or list anything deliberately deferred to Phase 5).

---

## Acceptance tests (Step 4.6 definition of done — closes Phase 4)
```bash
# 1) Lua live: lua-nginx-module on all 3 live edges (one at a time); cdn.a2zjav.com byte-identical; cache rules/header transforms now enforceable on prod zones (opt-in)
# 2) Real logs: structured JSON access logs ship to control plane -> request_logs hypertable (with retention);
#    GET /zones/{id}/logs returns recent requests with cache status + timing + request-id; dashboard Logs page is real, filterable, near-real-time, paginated; tenant-scoped
# 3) Origin offload real: shield origin-tier counters populate -> Analytics shows a TRUE origin-offload % (placeholder gone)
# 4) Backlog: PUT /rules/{id} + bulk reorder atomic (no ID churn); GET /zones/{id}/servers works; network-aggregate /stats server-side;
#    status-code breakdown + top paths + latency p50/p95/p99 in Analytics (flagged notes replaced)
# 5) GeoIP: country/ASN in logs + analytics geo breakdown; WAF country rule blocks a test country; errors-only rate limit counts only 401/403
# 6) Security audit: origin-lockdown implemented/documented (origin rejects non-Brisk traffic where configured); RBAC tenant boundary re-verified (no cross-tenant leak);
#    TLS posture clean; WAF blocks OWASP Top 10; multi-tenant isolation (cache/cert/rules/logs/stats) verified; audit report written
# 7) Perf: cache-HIT latency + throughput not meaningfully regressed by WAF/Lua/shield/logging under load
# 8) No regressions: routing, custom-domain TLS, shield, WAF, Lua, health, fan-out all intact; live fleet served throughout
# 9) Docs updated; backlog empty (or deferred items listed for Phase 5)
```
**Done when:** the Lua edge is **live** (cache rules + transforms enforceable on prod, opt‑in), the **Logs page is real** (structured edge logs → API → filterable near‑real‑time UI with retention), the **origin‑offload metric is truthful** (real shield counters), the carried backlog is folded in (atomic rule edits, inverse lookup, network‑aggregate + status/geo/top‑paths/latency stats), **GeoIP + country WAF rules + errors‑only limits** work, and a **security/performance audit** has verified origin lockdown, tenant isolation, TLS, WAF efficacy, and no perf regressions — with the live fleet served throughout. **Phase 4 is complete.**

---

## Pitfalls (do not skip)
1. **Lua live rollout = one edge at a time + rollback**, live zone byte‑identical; don't flip prod zones' shield/WAF/Lua on without intent (opt‑in, documented runbook).
2. **Logs are high‑volume** — buffer/bound/drop‑oldest shipping (reuse stats pipeline), Timescale retention + compression, paginate/virtualize the UI; near‑real‑time not instant; avoid heavy sampling (security needs fidelity) but cap sanely.
3. **Origin offload must be real now** — count actual origin‑tier requests; no placeholder math.
4. **Atomic rule edits** — `PUT`/bulk‑reorder so IDs don't churn; migrate the dashboard editor off delete+recreate.
5. **GeoIP licensing/ABI** — use a permissibly‑licensed DB (GeoLite2), keep it updatable; country detection feeds logs/analytics/WAF consistently.
6. **Origin lockdown is the #1 CDN security gap** — a reachable origin bypasses the whole edge stack; implement/document so traffic must traverse Brisk (egress allowlist / secret header / mTLS).
7. **Re‑verify tenant isolation end‑to‑end** — cache keys, SNI certs, rules, logs, stats: no cross‑tenant bleed; RBAC boundary holds.
8. **Perf budget** — WAF+Lua+shield+logging stacked must not tank HIT latency/throughput; measure under load; keep hot paths lean.
9. **Secrets + privacy** — request logs may contain IPs/PII; document retention + access; keep keys/certs/tokens out of logs.
10. **Scope** — hardening + cleanup + the listed backlog only. New product surface (portal, billing) = Phase 5.

## After Step 4.6 — Phase 4 is DONE ✅ → Phase 5 (customer portal + billing)
Brisk is now a **production‑grade, multi‑tenant, sellable CDN**: many customer sites on their own domains with auto‑TLS, origin shield, WAF + rate limiting, a programmable Lua edge with enforced cache rules, real logs + analytics, and a hardened, audited surface — all self‑hosted on the Go control plane in the Voltage dashboard.

**Phase 5 (next, do NOT start):** the **customer portal** (each tenant manages only their own zones/domains/cache/WAF/analytics/logs/purge via the tenant‑aware RBAC built in Phase 3.7), **usage metering** (per‑tenant bandwidth/requests from the real stats/logs), **billing/plans** (pricing tiers, quotas, invoices, payments), and **self‑service onboarding** (sign‑up → add zone → CNAME → live). This is the commercial layer that turns Brisk into a product you sell. Wait for the user's go‑ahead and a Phase 5 plan.
