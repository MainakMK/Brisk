# Brisk CDN — Phase 4 / Step 1 Build Prompt (Multi-Tenant Host-Based Origin Routing)

**For Claude Code.** Context: `CLAUDE.md` + `docs/Brisk_Phase1_Build_Spec.md` + all Phase‑2/3 + Phase‑3.7 prompts + `docs/Control_Plane_Ops.md` + `dashboard-reference/`. **Phases 1–3 + 3.7 are complete:** Brisk is a live, self‑driving, secured multi‑PoP CDN — 3 real edges (`US-NY-prod01`, `EU-FRA-prod01`, `BLR1-01`) on nginx.org 1.30.2 (`Server: Brisk`, Brotli, video slicing), real `brisk-agent` pulling config over autossh tunnels from the laptop control plane, geo DNS + ~30s failover + drain, purge/stats fan‑out verified, control‑plane‑managed wildcard TLS (lego Bunny DNS‑01), and admin auth with a tenant‑aware RBAC core. **Today the edges effectively serve ONE site:** `cdn.a2zjav.com` → origin `test.mainakghosh.com`. The `zones` table already has `cdn_hostname` + `origin_url` per zone, and the cache key already includes `$host`.

> **Read `CLAUDE.md`, `docs/Brisk_Phase1_Build_Spec.md`, the Phase‑2 Step‑3 (pull‑config) + Phase‑3.7 Step‑2 (agent nginx template/`server.tmpl` + managed TLS) prompts first.** This is **Step 1 of Phase 4 (re‑scoped so multi‑tenancy comes first).** It makes Brisk serve **many customer sites** from the same edges, routed by `Host`. **No custom‑domain CNAMEs / per‑domain TLS yet** (that's Step 2) — this step does host‑based origin routing for **Brisk‑subdomain hostnames** under the existing wildcard cert. Test locally + carefully on the live fleet. Pass the acceptance tests, stop before Step 2.

## Step 4.1 goal (one line)
Turn the single‑origin edge into a **multi‑tenant edge**: the agent renders **one nginx `server` block per zone** (keyed on `server_name = cdn_hostname`, proxying to that zone's own `origin_url`), so requests are routed to the right origin **by Host header**, with **per‑tenant cache isolation** — letting Brisk host many sites without collision.

## ⚠️ Live fleet — don't break `cdn.a2zjav.com`
The existing live zone must keep working byte‑for‑byte through this change (it just becomes "one of N zones" instead of the only one). Roll out carefully, validate the live zone first, keep rollback ready.

---

## How multi-tenant routing works (build to this)
Nginx routes by the **`Host` header**: when a request arrives, Nginx matches `Host` against the `server_name` of each **`server` block** and dispatches to the match. To host many sites on the same edge you give **each site its own `server` block** with its own `server_name` + its own `proxy_pass` origin. Key rules from the model:
- **`server_name` must be unique per block** — you can't have two blocks with the same `server_name` (duplicate = conflict). One zone = one hostname = one block.
- A `default_server` block catches unmatched hosts (return a clean 404/444, not a random tenant's site).
- The **cache key already includes `$host`** (Phase 1), so two tenants' identical paths never collide in cache — keep that.
- This is the standard "one TLS server block (or dynamic hostname lookup) per domain" pattern that lets one IP serve many sites via SNI (SNI/per‑domain certs come in Step 2; here all hostnames are Brisk subdomains under the existing wildcard `*.a2zjav.com`).

**Brisk mapping:** the agent's `server.tmpl` currently renders effectively one site. Change it to **loop over all zones assigned to this edge** and emit a `server` block per zone — each with `server_name = <zone.cdn_hostname>`, `proxy_pass = <zone.origin_url>`, and that zone's settings (TLS, video, cache rules, Brotli, CORS). The control plane already sends the agent its assigned zones via pull‑config; this step makes the template render them all as independent vhosts.

## Part 1 — Zone model: ensure per‑tenant origin + hostname are first‑class
- Confirm/extend `zones`: `cdn_hostname` (unique — the routing key), `origin_url` (per‑zone origin), plus the existing per‑zone settings (tls_mode, video/profile/ttls, cors_origin, brotli_level, status, config_version, account_id). Add a `host` mapping if needed so the agent knows the **Host header to send upstream** (some origins need their own host, not the CDN host — see Part 3).
- **Uniqueness:** enforce unique `cdn_hostname` at the DB + API (409 on dup) — two zones can't share a routing hostname.
- **Account scoping:** each zone has `account_id` (already there from the RBAC work) — keep it; it's how the customer portal will later show "my zones."
- For now hostnames are **Brisk subdomains** (e.g. `cust1.cdn.a2zjav.com`, `cust2.cdn.a2zjav.com`) under the existing wildcard cert. (Customers' *own* domains + per‑domain certs = Step 2.)

## Part 2 — Agent template: one server block per assigned zone
Rework `server.tmpl` (Phase‑3.7 Step‑2) to **iterate the edge's assigned zones** and render, per zone:
- `listen 443 ssl;` + `server_name <cdn_hostname>;` (and `listen 80` → 301 to https).
- TLS: use the **managed wildcard cert** for `*.a2zjav.com` (from Phase‑3.7 Step‑2) — all current tenant hostnames are covered by it. (Per‑domain certs via SNI = Step 2; design the template so adding a `ssl_certificate` per block later is easy.)
- `proxy_pass <origin_url>;` — that zone's origin, with the correct upstream **Host header** (Part 3), `proxy_cache brisk_cache`, the cache key with `$host` (isolation), `proxy_cache_lock`, slice for video zones, Brotli, branded `X-Brisk-*` headers, and that zone's cache rules.
- Carry forward **all** Phase‑3.7 Step‑2 fixes (Cloudflare‑proxied origin via `resolver` + variable `proxy_pass` where needed, `user www-data`, WP caching, stale‑while‑revalidate + cache‑lock, HSTS, etc.) — applied **per zone**.
- A **`default_server`** block for unmatched Host → clean 404/444 (never leak one tenant's content to a wrong/unknown host).
- Validate `nginx -t` and reload; the template must scale to many zones cleanly (consider per‑zone `include` files or one generated config — keep it maintainable and fast to reload).

## Part 3 — Upstream Host header + origin types (the subtle part)
Per‑tenant origins vary; handle both cleanly:
- **Directly‑hosted origins** (a real server IP or hostname → IP): the easy case. `proxy_pass` to the origin; send the **upstream Host header** the origin expects (usually the origin's own hostname, configurable per zone — some origins serve by their own host, not the CDN host). Make the upstream Host a per‑zone setting (default: the origin's host).
- **Proxied origins (e.g. behind Cloudflare)**: the harder case Brisk already solved for `test.mainakghosh.com` (resolver + variable `proxy_pass` + correct SNI/Host). Generalize that handling per zone so any tenant can use a proxied origin.
- Document the per‑zone origin settings (origin URL, upstream host, TLS to origin on/off).

## Part 4 — Dashboard: per‑tenant zones (extend existing Zones UI)
- The Zones page (Phase‑2 Step 6.3) already does create/edit — ensure it captures **per‑zone origin + cdn_hostname + upstream‑host** clearly, and shows the **assigned account** (admin view). When a zone is created/assigned to an edge, it appears as its own vhost after the next config pull.
- Show the tenant's **CDN hostname** prominently (this is what they'll CNAME to in Step 2) and a copy‑to‑clipboard.
- Honest propagation hint (~15s pull) as before.
- Role‑aware: admin sees all tenants' zones; the structure is ready for the customer portal to show only `account_id`'s zones.

## Part 5 — Test multi-tenancy for real
- Stand up (locally and/or on the live fleet carefully) **2+ zones** with **different origins** under different Brisk subdomains, assigned to the edges.
- Prove **Host‑based routing**: `cust1.cdn.a2zjav.com` serves origin A, `cust2.cdn.a2zjav.com` serves origin B, from the **same edges/IPs**, simultaneously — no collision.
- Prove **cache isolation**: identical paths on two tenants cache separately (different `$host` keys); purging one tenant doesn't purge the other.
- Prove the **live zone** (`cdn.a2zjav.com`) is unaffected.

---

## Acceptance tests (Step 4.1 definition of done)
```bash
# Local Docker first, then carefully on the live fleet
# 1) Two zones, two origins, two Brisk-subdomain hostnames, assigned to the edge(s)
#    curl -ks https://cust1.cdn.a2zjav.com/ -> origin A's content
#    curl -ks https://cust2.cdn.a2zjav.com/ -> origin B's content   (same edge/IP, routed by Host)
# 2) Existing live zone unaffected: cdn.a2zjav.com -> 200, Server: Brisk, HIT (byte-for-byte as before)
# 3) Cache isolation: GET /index.html on cust1 and cust2 cache under different keys ($host); a purge on cust1 doesn't affect cust2
# 4) Per-zone settings honored: a video zone uses slicing; a zone with brotli serves br; cache rules apply per zone
# 5) Upstream Host: a zone whose origin needs its own Host header works (content correct); a Cloudflare-proxied origin works (generalized fix)
# 6) Unknown host: a request with an unmatched Host -> default_server clean 404/444 (no tenant content leaked)
# 7) Unique hostname enforced: creating a 2nd zone with an existing cdn_hostname -> 409
# 8) Scale/reload: with N zones assigned, nginx -t passes and reload is clean/fast; config remains maintainable
# 9) Propagation: create+assign a new zone -> config_version bumps -> edges re-pull -> new vhost live within the poll interval
# 10) Live-site safety: cdn.a2zjav.com served correctly throughout; rollback ready
```
**Done when:** the edges serve **multiple tenant sites routed by Host header**, each zone an independent `server` block with its **own origin** and settings, **per‑tenant cache isolation** holds, unknown hosts hit a clean default, hostnames are unique, the config scales/reloads cleanly, new zones go live via the normal pull‑config flow, and the existing live zone is untouched — verified locally and on the live fleet.

---

## Pitfalls (do not skip)
1. **One zone = one unique `server_name` block** — Nginx rejects duplicate `server_name`; enforce unique `cdn_hostname` (409 on dup).
2. **`default_server` for unknown hosts** — return a clean 404/444; never let an unmatched Host fall through to a random tenant's site (content‑leak / wrong‑site bug).
3. **Cache isolation via `$host` in the key** — keep it; verify two tenants' identical paths don't collide and cross‑tenant purge doesn't bleed.
4. **Upstream Host header is per‑zone** — many origins serve by their own host, not the CDN host; make it configurable; generalize the Cloudflare‑proxied‑origin fix per zone.
5. **Carry forward ALL Phase‑3.7 Step‑2 behavior per zone** — TLS/HSTS, `user www-data`, WP caching, SWR+lock, branded headers, video slicing, Brotli — applied to each generated block, not just the first.
6. **Live zone must not regress** — `cdn.a2zjav.com` byte‑for‑byte unchanged; validate first; rollback ready.
7. **Template must scale + reload fast** — many `server` blocks; use includes / clean generation; `nginx -t` before reload; don't make reloads slow as tenants grow.
8. **Wildcard cert covers Brisk subdomains only** — all Step‑1 tenant hostnames are `*.a2zjav.com`; **customer‑owned domains + per‑domain SNI certs are Step 2** — design the template so adding per‑block `ssl_certificate` is trivial, but don't build custom‑domain TLS here.
9. **Account scoping intact** — every zone keeps `account_id`; don't break the RBAC tenant‑scoping from Phase‑3.7 Step‑3.
10. **Scope** — host‑based multi‑tenant origin routing only. Custom domains/auto‑TLS = Step 2; origin shield = Step 3; WAF = Step 4; Lua/cache‑rule enforcement = Step 5.

## Next — Step 4.2 (do NOT start) — custom-domain CNAMEs + per-domain auto-TLS
Let customers point **their own domain** (e.g. `cdn.theirsite.com`) at a Brisk‑managed CNAME, verify ownership, then **automatically issue a per‑domain certificate** (extending the lego machinery from Phase‑3.7 Step‑2 from the wildcard to per‑customer certs) and serve it via **SNI** (one cert per hostname, many on one IP) — the gateway to onboarding paying customers. Wait for the user's go‑ahead and a Step 4.2 prompt.
