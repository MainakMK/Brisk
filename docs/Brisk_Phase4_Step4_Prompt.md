# Brisk CDN — Phase 4 / Step 4 Build Prompt (WAF + Rate Limiting, per-zone)

**For Claude Code.** Context: `CLAUDE.md` + `docs/Brisk_Phase1_Build_Spec.md` + all Phase‑2/3/3.7 prompts + the Phase‑4 Step‑1/2/3 prompts + `docs/Control_Plane_Ops.md` + `dashboard-reference/`. **Phase 4 Steps 1–3 are complete:** multi‑tenant host routing (one `server` block per zone, per‑zone origin, `$host` isolation, `default_server` health‑safe), custom domains + per‑domain auto‑TLS (lego HTTP‑01 via edge `:80` challenge proxy, SNI), and per‑zone origin shield (mid‑tier cache, graceful fallback). Edges run **nginx.org 1.30.2** with `headers-more` + `ngx_brotli` + slice; `brisk-agent` + `brisk-control` are **Go**; config reaches edges via the **config‑pull + `config_version`** channel; admin auth + tenant‑aware RBAC exist.

> **Read `CLAUDE.md`, the Phase‑4 Step‑1 (multi‑tenant) prompt, and the Phase‑2 Step‑3 (pull‑config) prompt first.** This is **Step 4 of Phase 4 — WAF + rate limiting.** Per‑zone security that mirrors how Cloudflare/Bunny/Akamai/KeyCDN structure it. Test locally in Docker. Pass the acceptance tests, stop before Step 5 (Lua edge logic).

## Step 4.4 goal (one line)
Give each zone a **Web Application Firewall + rate limiting** at the edge: a **managed OWASP ruleset** (SQLi/XSS/etc.), **custom rules** (IP/country/path/header → block/challenge/log), and **rate limits** — all per‑zone, configurable from the dashboard, with a **detect (log‑only) vs block** mode and **security event** visibility.

## ✅ Test locally in Docker
`brisk-control` + edges + an origin + a benign "attacker" (curl with SQLi/XSS payloads, a flood script). Verify malicious requests are blocked/logged per zone while legitimate traffic passes, with zero impact on other zones. No VPS needed.

---

## How the major CDNs structure WAF (build to this — researched)
Cloudflare, Akamai, Bunny, and KeyCDN converge on **three layered controls**, evaluated in order:
1. **Custom rules** (run first) — your own expressions on request fields (IP, country/ASN, path, method, headers, user‑agent) with actions **Block / Challenge / Log / Skip**. A terminating action (block/challenge) stops later evaluation.
2. **Rate limiting** — throttle/block traffic exceeding a defined request rate over a counting period, scoped per client (IP)/path. Best practice: target exact URI paths (e.g. login/OTP), optionally **count only error responses** (401/403) so legitimate users aren't limited, and treat counters as *approximate* (a few excess requests may slip through before enforcement).
3. **Managed rulesets** (run after) — pre‑configured, vendor‑maintained rules: the **OWASP Core Rule Set (CRS)** covering the OWASP Top 10 — SQL injection, XSS, code injection, scanner/bot detection, etc. — plus CMS‑specific protections (WordPress `/wp-login.php`, `/xmlrpc.php`). You **enable** these, you don't write them.

Plus cross‑cutting concepts every provider exposes:
- **Detect (log‑only) vs Block mode** — deploy in log‑only first to tune false positives, then enforce. (CRS calls this anomaly‑scoring/paranoia; expose at least detect‑vs‑block per zone.)
- **Per‑zone scoping** — each tenant configures their own WAF; one tenant's rules never affect another.
- **Security events / firewall log** — what was blocked, which rule, when, from where — for the dashboard + tuning.
- **Common presets** — WordPress brute‑force protection (rate‑limit `/wp-login.php`, block `/xmlrpc.php`), block bad user‑agents/empty headers, country/IP allow‑deny.

## The engine: OWASP Coraza (Go, CRS v4)
Use **OWASP Coraza** (`github.com/corazawaf/coraza/v3`) — an open‑source, OWASP‑maintained, **pure‑Go** WAF engine that runs the **OWASP CRS v4** and supports ModSecurity SecLang rulesets. It's the designated modern successor to **ModSecurity** (Trustwave ended ModSecurity support in 2024). Because `brisk-agent`/`brisk-control` are already Go, Coraza embeds **natively** — Apache‑2.0 licensed (clean for a commercial product), high performance (microsecond‑level inspection), CRS v4 compatible.
- **Where it runs:** the cleanest fit for an Nginx data plane is a **Coraza‑based filtering layer the agent owns** — either Coraza via its **nginx C connector / coraza-spoa**, or a small **Go pre‑filter** the agent runs in front of nginx that inspects requests and applies CRS + custom rules + rate limits, then proxies clean traffic to nginx. **Pick the approach that integrates cleanly with the nginx.org build + the per‑zone `server` blocks** (evaluate coraza‑spoa vs the nginx connector vs an in‑agent Go proxy hop; document the choice + why). Avoid ModSecurity (EOL).
- For **rate limiting**, Coraza/CRS can do some of it, but Nginx's native **`limit_req`/`limit_conn`** (per‑zone `limit_req_zone` keyed on `$binary_remote_addr` + path) is battle‑tested and cheap — use Nginx native rate limiting for the throughput path and Coraza for the rule inspection. Document the split.

## Part 1 — Schema + control plane (per-zone WAF config)
- Per‑zone WAF settings (migration): `waf_enabled` (bool), `waf_mode` (`detect` | `block`), `managed_ruleset` (off | owasp_crs + a sensitivity/paranoia level), and a `wp_preset` toggle (WordPress hardening). 
- `waf_custom_rules` table: per zone, ordered, `{priority, match (field, op, value) , action (block|challenge|log|allow), enabled}` — fields: ip/cidr, country, path/prefix/regex, method, header, user‑agent.
- `waf_rate_limits` table: per zone, `{path_match, requests, period_seconds, key (ip|ip+path), action (block|challenge), count_mode (all|errors_only)}`.
- `security_events` (Timescale hypertable like stats): `{ts, zone_id, client_ip, country, rule_id/type, action, path, method, ua, edge_id}` — the firewall log.
- Endpoints (tenant‑scoped via RBAC — a customer manages only their zones; admin all):
  ```
  GET/PUT /api/v1/zones/{id}/waf                 # enable, mode, managed ruleset, wp preset
  GET/POST/DELETE /api/v1/zones/{id}/waf/rules   # custom rules (ordered)
  GET/POST/DELETE /api/v1/zones/{id}/waf/ratelimits
  GET /api/v1/zones/{id}/security-events?from&to # firewall log (filter by action/rule/ip)
  GET /api/v1/security-events                    # admin: across tenants
  ```
- WAF config flows to edges via the **existing config‑pull + `config_version`** channel (bump on any WAF change → edges re‑pull → reload). No new transport.

## Part 2 — Edge enforcement (Coraza + Nginx native rate limit), per zone
- The agent renders **per‑zone WAF enforcement**: each zone's traffic is inspected by Coraza with that zone's managed ruleset + custom rules at the configured **mode** (detect = log only, never block; block = enforce terminating actions). One tenant's rules are isolated to its `server` block — never cross‑zone.
- **Custom rules first, then rate limiting, then managed CRS** (the provider evaluation order); a terminating action short‑circuits.
- **Rate limiting** via Nginx `limit_req_zone`/`limit_req` (+ `limit_conn`) keyed per‑zone per‑client (and per‑path where configured); return 429 (or challenge). Support **errors‑only counting** where Nginx allows (or approximate via map on upstream status). Counters are per‑edge (document that, like CF's per‑datacenter scope — not globally exact).
- **Emit security events** (blocked/logged) → ship to `security_events` via the existing stats pipeline (batched, bounded, drop‑oldest — reuse Phase‑2 Step‑4 shipping).
- **Performance + safety:** WAF inspection must not tank throughput (Coraza is fast, but body‑inspection has a size cap — set a sane max body inspect size like the CDNs do; large video/file bodies shouldn't be deep‑scanned). WAF must **fail open or closed per a documented policy** (recommend: on WAF engine error, **fail open** for availability but log loudly — a broken WAF shouldn't take a tenant's site down; make it configurable). Don't break the origin‑shield path, custom‑domain TLS, or `default_server` health probe.
- **Detect mode default** for newly‑enabled WAF (tune before enforcing), per best practice.

## Part 3 — Dashboard (per-zone WAF UI, Voltage)
- **Zone → Security tab:**
  - WAF **on/off** + **mode** (Detect / Block) toggle, with a clear explainer (detect = log only).
  - **Managed protection:** enable OWASP CRS (+ sensitivity), the **WordPress preset** (one‑click `/wp-login.php` rate‑limit + `/xmlrpc.php` block + bad‑UA), with plain‑language descriptions.
  - **Custom rules** editor: ordered list, condition builder (IP/country/path/method/header/UA → Block/Challenge/Log), drag‑reorder, enable/disable.
  - **Rate limits** editor: path + N requests / period + key + action (e.g. "5 req/min per IP on `/wp-login.php`").
  - **Security events** view: recent blocks/logs with rule, IP, country, path, action + filters; "what would have been blocked" in detect mode (the tuning view).
- Admin: cross‑tenant security overview (top attacked zones, top blocked IPs).
- Honest hints: rate‑limit counters are approximate + per‑edge; detect mode recommended first; managed rules may need tuning for false positives.
- Role‑aware so the customer portal (Phase 5) exposes each tenant's own Security tab.

## Part 4 — Safety + ops
- Live‑site safety: WAF defaults **off** per zone; enabling defaults to **detect** mode; `cdn.a2zjav.com` and existing zones unaffected until explicitly configured. A WAF change is a `config_version` bump (poll‑interval propagation) — confirm zones keep serving through enable/disable.
- Document in `docs/Control_Plane_Ops.md`: the engine choice + integration, the evaluation order, fail‑open/closed policy, body‑inspect cap, per‑edge counter scope, and the CRS tuning workflow (detect → review events → block).

---

## Acceptance tests (Step 4.4 definition of done — local Docker)
```bash
docker compose up --build -d        # control + edges + origin + attacker
# 1) Per-zone enable: zone A WAF=block; zone B WAF=off
# 2) OWASP CRS blocks attacks on zone A: SQLi (?id=1' OR '1'='1) -> 403; XSS (<script>) -> 403; clean request -> 200
#    zone B (off) -> same payloads pass through (proves per-zone isolation)
# 3) Detect mode: set zone A -> detect -> the SAME attacks return 200 but are LOGGED as would-block in security_events (no enforcement)
# 4) Custom rule: block country/IP/path (e.g. block /admin from a test IP) -> 403; allow rule passes; ordering respected (terminating short-circuits)
# 5) Rate limit: 5 req/min on /wp-login.php per IP -> 6th request 429; other paths unaffected; other IPs unaffected
# 6) WordPress preset: one-click -> /xmlrpc.php blocked, /wp-login.php rate-limited, bad UA blocked
# 7) Security events: blocks/logs appear in GET /zones/{id}/security-events with rule/ip/country/path/action; admin cross-tenant view works
# 8) Body-inspect cap: a large file/video upload/body isn't deep-scanned (no throughput collapse); normal requests inspected
# 9) Fail policy: simulate WAF engine error -> behaves per documented policy (fail-open + loud log by default), zone stays up
# 10) No regressions: origin shield, custom-domain TLS (SNI), default_server /healthz by IP, multi-tenant routing all intact; config_version bump propagates WAF changes
# 11) RBAC: a customer manages only its own zone's WAF; admin sees all
```
**Done when:** each zone can enable a **WAF (detect/block) with the OWASP CRS + custom rules + rate limiting**, malicious requests are blocked/logged **per zone** (isolated from other tenants), the **WordPress preset** works one‑click, **security events** are visible + filterable in the dashboard, rate limiting throttles per client/path, WAF respects a body‑inspect cap + a documented fail policy, and nothing regresses (routing, TLS, shield, health) — all verified locally, with WAF **off by default** on live zones.

---

## Pitfalls (do not skip)
1. **Use OWASP Coraza (Go, CRS v4), not ModSecurity** — ModSecurity is EOL (2024); Coraza is the maintained Go successor and embeds natively in the Go agent. Apache‑2.0.
2. **Evaluation order: custom rules → rate limit → managed CRS**, terminating actions short‑circuit — mirror the provider model so behavior is predictable.
3. **Per‑zone isolation** — one tenant's WAF/rules/limits must never affect another; scope to the zone's `server` block; RBAC‑gate the API.
4. **Detect mode first** — default newly‑enabled WAF to log‑only so tenants tune false positives before enforcing; expose the "would‑block" events.
5. **Body‑inspect size cap** — don't deep‑scan large video/file bodies (throughput + the CDNs all cap this); set a sane max.
6. **Fail policy documented** — on WAF engine error, default **fail‑open + loud log** (a broken WAF shouldn't blackhole a tenant); make it configurable.
7. **Rate‑limit reality** — counters are approximate + per‑edge (per‑datacenter, like Cloudflare); say so in the UI; target exact paths; offer errors‑only counting for login/OTP.
8. **Reuse config‑pull + stats pipeline** — WAF config via `config_version`; security events via the existing batched shipping. No new transport.
9. **No regressions** — origin shield, custom‑domain SNI TLS, `default_server` health probe, multi‑tenant routing must all keep working.
10. **Live‑site safety + scope** — WAF off by default; enabling never drops a live zone. This step = WAF + rate limiting only. Lua/cache‑rule enforcement = Step 5; cleanup = Step 6; bot‑management/challenge UX can start simple (a basic challenge/JS‑challenge) and deepen later.

## Next — Step 4.5 (do NOT start) — Lua/OpenResty edge logic (+ custom cache-rule enforcement)
Programmable edge via Lua/OpenResty: finally **enforce the custom cache rules** the dashboard has stored since Phase‑2 (the deferred backlog item — rules stored + versioned but not yet applied at the edge), plus request/response transforms, header manipulation, and the hooks WAF challenges/advanced routing can build on. Wait for the user's go‑ahead and a Step 4.5 prompt.
