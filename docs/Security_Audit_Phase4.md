# Brisk — Phase 4 Security & Performance Audit

_Date: 2026-06-11 · Scope: the full multi-tenant surface built across Phases 1–4
(host routing, custom-domain TLS, origin shield, WAF, Lua edge, logs/analytics).
This is the Phase 4 Step 6 Part 6 hardening pass. Findings + fixes below._

## Summary

| Area | Posture | Action |
|------|---------|--------|
| Origin lockdown | **Implemented** (secret pull header) + documented | Enable per customer |
| Admin / control-plane auth | Solid (argon2id + sessions + CSRF + bearer tokens) | None |
| Tenant RBAC isolation | Enforced at one chokepoint (`scopeZone` → `Authorize`) | None |
| Secrets handling | Keys/certs/tokens gitignored, never logged | None |
| Control plane exposure | Private (SSH-tunnel only) until deliberate deploy | None |
| TLS posture | TLS 1.2/1.3, ECDSA, HSTS, no weak ciphers | None |
| WAF efficacy | CRS v4 blocks OWASP Top 10 (lab + unit tests) | None |
| Multi-tenant cache/cert/log isolation | No cross-tenant bleed (verified) | None |
| Perf (WAF/Lua/shield/logging) | Gated + fail-open; HIT path lean | Monitor under load |

No **critical** issues open. The #1 CDN gap (reachable origin) now has a concrete
mitigation (below); turning it on is per-customer operational.

---

## 1. Origin lockdown (the #1 CDN security gap)

**Risk:** if the customer origin is reachable directly (by IP/hostname), an attacker
bypasses the edge entirely — the WAF, rate limits, TLS, and shield become irrelevant.

**Mitigation implemented (Phase 4 Step 6 Part 6):** a **shared secret pull header**.
When `origin_pull_secret` is set in the agent config, every edge adds a secret header
to **origin** requests (the origin path **and** the shield-down fallback) — never to
the shield hop, never to the client response. The customer origin rejects any request
lacking the header, so traffic **must** traverse Brisk.

- Config (agent.yaml / env): `origin_pull_secret: <random-32+>`, optional
  `origin_pull_header:` (default `X-Brisk-Pull-Token`). The secret is **never logged**
  and lives only in the on-box rendered nginx.conf.
- Rendered (when set): `proxy_set_header X-Brisk-Pull-Token "<secret>";` on the origin
  `proxy_pass` and the `@brisk_origin_fallback` location. Empty ⇒ byte-identical to
  before (off by default).

**Customer guidance (document to tenants):** choose ONE of —
1. **Secret header** (easiest): reject requests without the header, e.g. nginx
   `if ($http_x_brisk_pull_token != "<secret>") { return 403; }` or Apache/WAF equiv.
2. **Egress-IP allowlist:** allow only Brisk's edge egress IPs at the origin firewall.
   Current edges: **NY `104.248.231.144`**, **DE/EU-FRA `188.245.225.172`**,
   **BLR `139.59.78.21`** (update this list when the fleet changes).
3. **mTLS to origin** (strongest, future): client-cert auth on the origin — deferred to
   a later hardening phase; the secret header covers the common case today.

**Residual:** the secret is shared per edge (not per tenant); rotating it re-renders all
zones. Acceptable for the current single-operator fleet; per-zone secrets are a Phase 5
multi-tenant refinement.

---

## 2. Control-plane + admin auth review

- **Human auth (Phase 3.7 Step 3):** argon2id password hashing, HttpOnly session
  cookies, CSRF double-submit on state-changing requests, login rate limiting. Admin
  routes sit behind the human-auth middleware.
- **Programmatic auth:** admin API bearer tokens (hashed at rest, shown once).
- **Agent auth:** per-edge bearer tokens; `/agent/*` endpoints require a valid server
  token; the server id is resolved from the token (not client-supplied).
- **Verified:** unauthenticated requests to protected routes get 401; cross-account
  access gets 403 (see §3).

## 3. Tenant RBAC isolation (no cross-tenant leak)

- **Single chokepoint:** every tenant-scoped resource resolves through
  `API.scopeZone` → `identity.Authorize(callerAccount, zone.AccountID)`. A caller
  touching another account's zone gets **403** before any data is read.
- **Covered resources:** zones, cache rules, header transforms, WAF config + rules +
  rate limits, security events, **request logs**, and **log analytics** (the new Part 2/3/4
  endpoints `GET /zones/{id}/logs` + `/logs/analytics` both call `scopeZone`).
- **Admin cross-tenant** endpoints (`/logs`, `/logs/analytics`, `/security-events`,
  `/stats/network`) are under the admin-only infra group, not reachable by tenants.

## 4. Secrets + privacy

- Bunny API key, admin password, agent SSH creds, control-plane `.env`, and
  `brisk-control/tunnels/.env` are **gitignored** and never echoed/logged.
- The new `origin_pull_secret` is never logged (grep-verified) — on-box config only.
- **Request-log privacy:** `request_logs` may contain client IPs + user-agents (PII).
  Mitigations: **7-day retention** (auto-drop) + columnstore compression; tenant-scoped
  reads (a tenant sees only their own zone's requests). Document retention/access to
  customers. No full request bodies are logged.

## 5. Network exposure

- The control plane, Postgres/TimescaleDB, and NATS are bound to localhost / private
  Docker networks and reached only over **SSH tunnels** today — not publicly exposed
  until the deliberate public deploy. Edge↔control TLS uses lego/Bunny DNS-01 certs.

## 6. TLS posture

- `TLSv1.2 TLSv1.3` only; ECDHE-only modern cipher suite; **ECDSA P-256** leaf certs;
  HSTS on; session cache + (configurable) tickets for resumption. No OCSP stapling
  (intentional — no benefit with the LE/self-signed profile). Per-domain + managed
  wildcard certs auto-renew (custom-domain manager + lego).

## 7. WAF efficacy

- OWASP Coraza v3 + **CRS v4** (embedded, offline). The waf-lab + `waf` unit tests
  confirm: SQLi/XSS **blocked** in block mode, **logged-only** in detect mode, custom
  rules (path/ip/country/UA) enforce, allow-rules short-circuit, the WordPress preset
  blocks `/xmlrpc.php` + scanner UAs, and **country rules** (Part 5) block by ISO code.
  Body-inspect cap (128 KiB) bounds inspection cost. Fail-open vs fail-closed is
  per-zone; a broken WAF service never blackholes a fail-open tenant.

## 8. Multi-tenant isolation (re-verified end-to-end)

- **Cache:** every `proxy_cache_key` is prefixed with `$host` (static, video/slice,
  playlist, html) ⇒ no cross-tenant cache bleed even on identical paths.
- **Certs/SNI:** per-zone `server_name` + cert; a no-SNI / unmatched-Host request hits
  the `default_server` (444, except `/healthz`) — no fall-through to a random tenant.
- **Rules/logs/stats:** all tenant reads RBAC-scoped (§3); `request_logs`/stats keyed by
  `zone_id`.

## 9. Performance

- The added layers are **gated and lean on the hot (cache-HIT) path**:
  - WAF `auth_request` is rendered **only** for WAF-on zones; segments (`.ts/.m4s/.mp4`)
    are deliberately **not** inspected (large media).
  - Lua hooks render **only** for zones with cache rules / transforms / errors-only
    limits; the rewrite/header_filter/access/log bodies early-return when a zone has no
    applicable data, and every per-request path is `pcall`-wrapped (fail-open).
  - Structured JSON logging is a second `access_log`; the agent tails + ships out of
    band (bounded, drop-oldest) — no request-path blocking.
  - GeoIP `$brisk_country` is a single mmdb lookup, only when the module is installed.
- A cache HIT on a zone with no WAF/Lua is byte-identical to the Phase-3 fleet.
  **Recommendation:** measure HIT p50/p95 under load during the live Lua rollout
  (Part 1) and compare against the pre-rollout baseline; the new latency-percentile
  analytics (Part 4) makes this directly observable.

---

## Findings & fixes

1. **Origin reachable directly** → *Fixed/mitigated*: secret pull-header implemented +
   customer guidance + egress-IP allowlist documented (§1).
2. **Down-migration bug** (`remove_columnstore_policy` called via `SELECT`, it's a
   procedure) → *Fixed*: now `CALL` (migrations verified up→down→up).
3. No cross-tenant leakage, secret leakage, or TLS weakness found.

**Deferred to Phase 5:** per-tenant origin pull secrets; mTLS-to-origin; per-tenant log
retention tiers; usage metering for billing.
