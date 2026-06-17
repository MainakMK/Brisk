# Brisk CDN — Phase 4 / Step 2 Build Prompt (Custom-Domain CNAMEs + Per-Domain Auto-TLS)

**For Claude Code.** Context: `CLAUDE.md` + `docs/Brisk_Phase1_Build_Spec.md` + all Phase‑2/3/3.7 prompts + `docs/Brisk_Phase4_Step1_MultiTenant_Prompt.md` + `docs/Control_Plane_Ops.md` + `dashboard-reference/`. **Phase 4 Step 1 is complete:** the 3 live edges are **multi‑tenant** — one nginx `server` block per zone (`server_name = cdn_hostname`, per‑zone `proxy_pass` origin + `host_header` override, migration 00012), `$host` cache isolation, a `default_server` that 444s unknown hosts **but carries the wildcard cert and answers `/healthz` 200** (so by‑IP health probes work — don't regress this), `cdn_hostname` uniqueness (409), and `brisk-agent --render`. Live: `cdn.a2zjav.com` → 200 from the geo set; demo tenant proven and removed. Also in place from Phase 3.7: **control‑plane‑managed TLS via lego** (wildcard `*.a2zjav.com`, Bunny DNS‑01, cert in Postgres, **fanned to edges over the config‑pull channel**, zero‑downtime reload, auto‑renew) and **admin auth + tenant‑aware RBAC**.

> **Read `CLAUDE.md`, the Phase‑4 Step‑1 prompt, and the Phase‑3.7 Step‑2 (managed TLS) prompt first.** This is **Step 2 of Phase 4 — the commercial gateway**: customers point **their own domain** at Brisk and get **automatic HTTPS**. Test on Let's Encrypt **staging** first, then production ACME; keep the live fleet safe. Pass the acceptance tests, stop before Step 3 (origin shield).

## Step 4.2 goal (one line)
Let a tenant add a **custom domain** (e.g. `cdn.theirsite.com`) to their zone: Brisk shows the CNAME target, **verifies the DNS points at Brisk**, then **automatically issues a per‑domain certificate** (lego, HTTP‑01 answered by Brisk's own edges), stores + **fans it to all edges via the existing config‑pull channel**, and serves the domain via **SNI** — with automatic renewal, honest status in the dashboard, and rate‑limit discipline.

## ✅ Test safely
Use **Let's Encrypt STAGING** for all development/iteration (production LE has strict rate limits — 5 failed validations/hour and duplicate‑cert limits; burning them on tests can lock you out for days). Switch to production ACME only for the final proof with a real test domain you control (e.g. a subdomain of `mainakghosh.com` CNAME'd at Brisk). The live zones must keep serving throughout.

---

## The architecture (researched — build to this)

### Why HTTP‑01 is the natural primary for a CDN
For customer **bring‑your‑own domains** you can't use Bunny DNS‑01 — Brisk doesn't control the *customer's* DNS. But here's the elegance: once the customer CNAMEs `cdn.theirsite.com → <their-zone-hostname>`, **all HTTP traffic for that domain — including the CA's validation request — already lands on Brisk's edges**. So Brisk answers `http://cdn.theirsite.com/.well-known/acme-challenge/<token>` itself:
- **lego runs centrally in the control plane** (same as the wildcard model) with an **HTTP‑01 provider backed by shared challenge state**: lego stores the active `token → keyAuth` in Postgres/memory via its provider interface.
- **Every edge proxies `/.well-known/acme-challenge/*` to the control plane** (over the existing agent tunnel — the same localhost tunnel port the agent already uses for config pull), so **whichever geo‑routed edge the CA hits**, the challenge answers correctly. This solves the classic multi‑server HTTP‑01 problem (the CA may validate from multiple vantage points and can hit any edge).
- **Critical nginx detail:** the port‑80 server blocks 301‑redirect to HTTPS — the **ACME challenge path must be excluded from that redirect** (serve it over plain HTTP, proxied to the control plane) or validation breaks.
- *(Optional, documented for later: DNS **delegated validation** — customer CNAMEs `_acme-challenge.theirsite.com → <verify-host>.a2zjav.com`, then Brisk answers DNS‑01 in its own Bunny zone. This is how Cloudflare‑for‑SaaS‑style platforms do it; useful for wildcard customer domains. NOT in scope now — note it as a future enhancement.)*

### The custom‑domain lifecycle (state machine — the core of this step)
`pending_dns → verifying → issuing → active → (renewing) → expired/failed/removed`
1. **Add:** tenant adds `cdn.theirsite.com` to their zone in the dashboard → stored `pending_dns`, UI shows **exact CNAME instructions** (`CNAME cdn.theirsite.com → <zone.cdn_hostname>`).
2. **Verify (gate before any ACME attempt):** the control plane **checks DNS** (resolve the domain; confirm the CNAME chain lands on Brisk — the zone hostname / the geo set / a Brisk edge IP). Poll periodically until verified (with backoff + a manual "check now" button). **Never start ACME before DNS verifies** — premature attempts just burn the 5‑failed‑validations/hour budget.
3. **Issue:** lego (control plane) runs HTTP‑01 → cert + key stored in Postgres (same storage as the wildcard) → **fanned to all edges via the config‑pull channel** (the 3.7.2 machinery — serial in the ETag, agent writes cert + zero‑downtime reload).
4. **Serve:** the agent template renders the custom domain — **its own `server` block** (or `server_name` addition with its own cert — one cert pair per block, so per‑domain blocks are cleanest) with `ssl_certificate` = the per‑domain cert, proxying to the **same zone origin** with the same per‑zone settings. SNI picks the right cert by hostname — many certs, same IPs.
5. **Renew:** automatic, ~30 days before expiry (reuse the wildcard renewal loop; iterate all active custom‑domain certs). **Re‑verify DNS before each renewal** — if the customer removed the CNAME, mark `failed/detached` and alert, don't hammer ACME.
6. **Remove/detach:** removing the domain removes its server block + cert distribution (cert rows kept/archived briefly for rollback).

### Schema
`custom_domains` table (migration): `id, zone_id (FK), account_id, domain (unique), status, verify_method (cname), last_verified_at, cert_id/refs, last_error, created_at, updated_at` + a cert storage reference consistent with the wildcard cert rows. Unique on `domain` (409 on dup — a domain can't belong to two zones).

### Apex domains (handle honestly in the UI)
A CNAME **can't live at the apex** (`theirsite.com`) per DNS rules. Detect apex vs subdomain when the tenant enters the domain and show tailored guidance: subdomains (`cdn.theirsite.com`) → normal CNAME (recommended path); apex → use the DNS provider's **ALIAS/ANAME/CNAME‑flattening** if available, or prefer a subdomain. **Do not** hand out static A records to individual edge IPs — that would bypass Brisk's geo routing + health failover (a pinned IP defeats the whole Phase‑3 system). Keep apex support honest: "supported where your DNS provider offers ALIAS/flattening."

### Rate‑limit + abuse discipline (Let's Encrypt)
- **Staging for all tests.** Production limits include duplicate‑certificate caps and **5 failed validations/hour per account/hostname** — design for them: verify DNS first, exponential backoff on failures, cap retries, surface `last_error` to the dashboard instead of silently retrying.
- Queue/serialize issuance (one ACME job at a time or a small worker pool); jitter renewals so they don't thundering‑herd.
- **Don't issue for domains that don't point at Brisk** (the verification gate is also the abuse gate — prevents issuing certs for domains a tenant doesn't actually control/route).

## Part 1 — Control plane: lifecycle + ACME
- `custom_domains` store + the state machine above; endpoints:
  ```
  POST   /api/v1/zones/{id}/domains          # add {domain} -> pending_dns + CNAME instructions
  GET    /api/v1/zones/{id}/domains          # list with status/last_error/expiry
  POST   /api/v1/domains/{id}/verify         # "check now" -> runs DNS verification
  DELETE /api/v1/domains/{id}                # detach (removes vhost + cert distribution)
  ```
  (Tenant‑scoped via the RBAC core — a customer can only manage domains on their own zones; admin sees all.)
- DNS verifier (CNAME‑chain check, poll + backoff + manual trigger). ACME issuer: lego with a **custom HTTP‑01 provider** writing challenge state where the challenge endpoint can read it; staging/production ACME directory switch via config. Renewal loop extended to custom‑domain certs with pre‑renewal DNS re‑verification.

## Part 2 — Edge/agent: challenge route + per‑domain vhosts
- **Challenge route on every edge:** port‑80 `location /.well-known/acme-challenge/ { proxy_pass → control plane via the agent tunnel; }` — excluded from the 80→443 redirect; tiny, unauthenticated, rate‑limit‑friendly. Everything else on 80 keeps redirecting.
- Template renders **per‑custom‑domain server blocks** (own `server_name` + own `ssl_certificate`, same zone origin/settings) once the cert is distributed. The Step‑1 `default_server` (wildcard cert + `/healthz`) stays the by‑IP/unknown‑host catch‑all — **don't regress the health‑probe fix**.
- Cert distribution reuses the 3.7.2 fan‑out (config‑pull serial/ETag → agent writes cert files → `nginx -t` → zero‑downtime reload).

## Part 3 — Dashboard: the customer‑facing flow (Voltage)
- Zone detail gains a **Custom Domains** tab: add domain → instructions screen (the exact CNAME record to create, copy‑able; apex vs subdomain detection with tailored guidance) → live status chip per domain (`Waiting for DNS → Verifying → Issuing certificate → Active 🔒`, or `Action needed` with the human‑readable `last_error`) → cert info when active (issuer, expiry, auto‑renew on) → remove with confirm.
- Honest timing hints: DNS propagation can take time (minutes to 48h depending on the registrar); Brisk re‑checks automatically.
- Admin view: all custom domains across tenants + statuses (ops visibility), renewal health.

## Part 4 — Safety + ops
- Never block the live zones: issuance is per‑domain and async; a stuck domain stays `pending` without affecting anything else.
- Cert/key handling: keys never logged; stored like the wildcard (Postgres), distributed only to edges; removed on detach.
- Expiry monitoring: surface upcoming expiries + failed renewals in the dashboard (and the ops doc); a failed renewal keeps serving the old cert until expiry while alerting (never drop TLS early).
- Update `docs/Control_Plane_Ops.md`: the custom‑domain lifecycle, the challenge‑proxy path, staging vs production ACME, rate‑limit playbook, and the DNS‑delegation future note.

---

## Acceptance tests (Step 4.2 definition of done)
```bash
# STAGING ACME for all iteration; final proof on production ACME with a real test domain
# 1) Add a custom domain to a tenant zone -> pending_dns + exact CNAME instructions (copy-able) in the dashboard
# 2) Verification gate: BEFORE the CNAME exists -> stays pending (no ACME attempted, no rate-limit burn); "check now" works
# 3) Create the CNAME (real test domain -> the zone hostname) -> verifier flips to verifying -> issuing -> ACTIVE
#    - HTTP-01 answered through the edges: the CA's request to ANY geo-routed edge proxies to the control plane and validates
#    - port-80 ACME path is NOT redirected to HTTPS; all other port-80 paths still 301
# 4) Cert fan-out: all 3 edges receive the per-domain cert via config-pull -> zero-downtime reload ->
#    curl -ksI https://cdn.<testdomain>/ on each edge resolves SNI to the RIGHT cert (issuer/SAN = the custom domain), content = the zone's origin
# 5) Multi-tenant intact: the zone's Brisk hostname AND the custom domain both serve; other tenants + cdn.a2zjav.com unaffected;
#    default_server still 444s unknown hosts and answers /healthz by IP (health probes green throughout)
# 6) Renewal: force/dry-run renew -> re-verifies DNS first -> new cert -> zero-downtime reload; with the CNAME removed -> marked failed/detached + alert (no ACME hammering)
# 7) Rate-limit discipline: failed validation backs off exponentially, retries capped, last_error surfaced; issuance serialized/jittered
# 8) Apex handling: entering an apex domain shows the ALIAS/flattening guidance (no static per-edge A records offered)
# 9) Detach: removing the domain removes its vhost + cert from edges; domain uniqueness -> 409 on a second zone claiming it
# 10) RBAC: a customer account manages only its own zones' domains; admin sees all
```
**Done when:** a tenant can add their **own domain**, follow the CNAME instruction, and get **automatic HTTPS** — DNS‑verified before issuance, HTTP‑01 answered through Brisk's own edges, the per‑domain cert **fanned to all edges and served via SNI**, renewals automatic with DNS re‑verification, statuses honest in the dashboard, Let's Encrypt rate limits respected, apex handled honestly, and nothing regressed (multi‑tenant routing, health probes, live zones) — proven end‑to‑end with a real domain on production ACME after staging iteration.

---

## Pitfalls (do not skip)
1. **Staging ACME for all iteration** — production LE rate limits (failed‑validation + duplicate caps) can lock you out; only the final proof runs production.
2. **Verify DNS before any ACME attempt** — the gate that protects rate limits AND prevents issuing certs for domains not actually routed to Brisk.
3. **Don't redirect the challenge path** — `/.well-known/acme-challenge/` must serve over plain HTTP via the proxy; the 80→443 301 must exclude it.
4. **Any edge must answer the challenge** — the CA validates from multiple vantage points against the geo set; the challenge proxy → control plane (over the agent tunnel) makes every edge correct. Don't try per‑edge challenge files.
5. **One cert pair per server block** — render per‑domain blocks; SNI selects by hostname. Don't regress the Step‑1 `default_server` health‑probe fix (wildcard cert + `/healthz` by IP).
6. **No static per‑edge A records for apex** — that bypasses geo routing + failover; apex = ALIAS/flattening guidance or recommend a subdomain.
7. **Re‑verify DNS before renewal** — a detached CNAME must mark the domain failed, not hammer ACME for a domain that left.
8. **Keys never logged; certs removed on detach; failed renewal keeps the old cert serving + alerts** (never drop TLS early).
9. **Serialize/jitter issuance + renewals** — no thundering herd on the CA or the fleet.
10. **Scope** — custom domains + per‑domain TLS only. Origin shield = Step 3 (prompt drafted; header will be reconciled); WAF = Step 4; Lua/cache‑rule enforcement = Step 5; DNS delegated validation + wildcard customer domains = documented future.

## Next — Step 4.3 (do NOT start) — origin shield
The mid‑tier cache: all edge misses route through a designated shield PoP so many PoPs missing the same object collapse to ~one origin fetch — now built on top of multi‑tenant per‑zone origins (shield routing is per zone). A drafted prompt exists (`Brisk_Phase4_Step1_Prompt.md` from the earlier ordering); it will be reconciled to this new sequence + the multi‑tenant reality before running. Wait for the user's go‑ahead.
