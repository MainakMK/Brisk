# Brisk CDN — Dashboard Fix + Zone-Delete Purge + Cache Settings Panel (3-in-1)

**For Claude Code.** Context: `CLAUDE.md` + `docs/Control_Plane_Ops.md` + `dashboard-reference/brisk-design-spec.md` + the Phase‑2 Step‑3 (pull‑config) + Step‑5/6.5 (NATS purge) + Phase‑4 Step‑1 (multi‑tenant) + Step‑5 (Lua cache rules) prompts. **State:** control plane refreshed to v17; Phases 1–4 live on 3 edges (NY/DE/BLR) serving `cdn.a2zjav.com`; logs + WAF backends are live; a recent dashboard‑wiring task *claimed* to make Logs + Security real but **the sidebar still shows "SOON" and the pages aren't working** (build/deploy didn't take effect). Edges serve independently from last‑known‑good config + cache; purge fans out over NATS JetStream.

> **Read `CLAUDE.md` + the referenced prompts first.** This is **3 fixes/features in one prompt — do them IN ORDER (1 → 2 → 3).** #1 and #2 are small + high‑priority; #3 is a real feature build (the biggest part). Test locally; the live zone `cdn.a2zjav.com` must stay up. Don't enable anything on live zones unintentionally.

---

## PART 1 — Fix the dashboard so Logs + Security actually work (no more "SOON")
The previous wiring task reported Logs + Security as done, but the running dashboard **still shows "SOON"** on both nav items and the pages aren't live. Diagnose + fix for real:
- **Find why it didn't take effect:** likely the dashboard wasn't rebuilt/redeployed (the container is serving a stale build), OR the "SOON" badge lives in the **sidebar nav config** (a different file) and was never removed, OR the new pages/routes exist but aren't imported/routed. Check all three.
- **Rebuild + redeploy the dashboard** (Vite build → the served container/static), confirm the browser loads the new build (cache‑bust / hard refresh; verify a build hash changed).
- **Logs page:** confirm it renders the **real** view wired to `GET /api/v1/zones/{id}/logs` (+ admin `/logs`) with cache status + timing + filters + pagination; remove the **"SOON"** nav label.
- **Security page:** confirm it renders the **real** WAF screen wired to the live WAF APIs (`/zones/{id}/waf`, `/waf/rules`, `/waf/ratelimits`, `/security-events`); remove the **"SOON"** nav label.
- **Verify in the actual browser** (not just "build passes"): both nav items have no "SOON", both pages load real data, tenant‑scoped. Fix until the screenshots would show real pages.

## PART 2 — Deleting a zone purges its cache from ALL PoPs (~20–30s)
**Problem:** deleting a zone currently leaves the edges still serving its cached content — bad (the user deleted it; it must stop serving fast). **Fix:** zone deletion must **fan out to every edge** so each one **removes the zone's vhost + purges that zone's entire cache**, within ~20–30s.
- On `DELETE /api/v1/zones/{id}`: in addition to removing the zone record, **publish a purge‑zone (whole‑zone) message over NATS** (the Step‑5 instant‑purge channel) to every edge serving that zone, **and** trigger a config re‑pull (bump `config_version` / send a removal signal) so the agent **drops the zone's `server` block**. Net effect: edges stop serving the zone AND clear its cache files (the agent's KEY‑prefix delete from Step‑5, scoped to `$host` for that zone).
- **Timing:** purge is instant over NATS (ms–seconds); vhost removal is the next config pull (~15s). Target **stops‑serving within ~20–30s** across all PoPs. Verify each edge independently: after delete, requests to that zone's hostname **no longer serve cached content** (404/`default_server`, not stale HITs).
- **Safety / guard (the lesson from the live‑zone incident):** because delete now actively tears the zone down on the edges, make deletion of a zone that is **assigned to live/in‑rotation edges** require a **strong confirm** (type‑the‑hostname‑to‑confirm), and ideally block/​warn if it's a production zone — so an accidental click can't nuke a live site. (This directly addresses the recent `cdn.a2zjav.com` accidental delete.)
- **Durability:** if an edge is briefly unreachable, the whole‑zone purge replays on reconnect (JetStream) and the vhost removal lands on next pull — so a deleted zone doesn't "come back" serving stale cache.
- Audit the delete (who/when) in the existing audit log.

## PART 3 — Per-zone Cache Settings panel (Bunny-style) — the feature build
The user wants the cache controls a mature CDN exposes (ref: Bunny Smart Cache). Brisk already has the **engine** for most of this (Phase‑1 caching, slice module, cache rules, cookie‑strip for WP, `$host` cache key) — this surfaces them as **per‑zone dashboard toggles** + wires the few missing behaviors into the agent template. Add a **Cache Settings** section to the zone detail (alongside the existing Cache Rules), with these controls, each persisted per zone (migration) + rendered into the agent's nginx (`server.tmpl`) + propagated via `config_version`:

- **Smart Cache** (on/off) — cache based on file extension + MIME type for easy full‑site acceleration (vs. respecting only origin Cache‑Control). When on + cacheable → apply the expiration setting below.
- **Cache expiration time** (edge TTL) — dropdown incl. "respect origin Cache‑Control" + presets (1m…1y) + "do not cache" → `proxy_cache_valid` / per‑zone TTL var (composes with the existing override‑TTL cache rules; rules win where they match).
- **Browser cache expiration time** — the `Cache-Control`/`Expires` sent to the browser (incl. "match server" / "do not cache") → `add_header Cache-Control` via headers‑more.
- **Query string sort** (on/off) — normalize query‑param order so `?a=1&b=2` and `?b=2&a=1` are one cache entry (sort args before they enter the cache key; Lua/`arg_*` normalization, since `$args` is unsorted).
- **Cache error response** (on/off) — briefly cache origin error responses (~5s) to shield the origin from DDoS/retry storms → `proxy_cache_valid 500 502 503 504 5s` (else `no‑cache`).
- **Vary Cache** (multi‑select → adds to the cache key, each combo a separate cached file): **Browser WebP / AVIF support** (vary on `Accept` image support), **Cookie value**, **Desktop/Mobile** (vary on UA class), **Request hostname**, **URL query string**, **User country / state** (needs GeoIP — note dependency). Implement by **extending `proxy_cache_key`** per zone with the selected dimensions (e.g. add a normalized `$http_accept`‑derived webp/avif flag, a device‑class var, `$brisk_country`, etc.). Keep `$host` always in the key (isolation).
- **Query‑string vary parameters** — when set, only the listed params count toward the key (whitelist), instead of all.
- **Vary Cache by Request Headers** — list request headers to fold into the key (e.g. `Accept-Language`, API version).
- **Strip response cookies** (on/off) — strip `Set-Cookie` from responses for a cookieless/cacheable domain → `proxy_ignore_headers Set-Cookie` + `more_clear_headers Set-Cookie` (note: responses with `Set-Cookie` aren't cached by default, so this is what makes cookie‑setting origins cacheable).
- **Optimize for large object delivery** (on/off) — split cached files into chunks for byte‑range/video → **this is Brisk's existing slice module**; expose it as a per‑zone toggle (already built — just wire the switch, recommend on for video).
- **Stale Cache**: **While Origin Offline** + **While Updating** (each on/off) → `proxy_cache_use_stale error timeout updating http_500 http_502 http_503 http_504` + honor `stale-while-revalidate`/`stale-if-error`. Serve stale while revalidating / when origin is down (you already have SWR — surface it as explicit toggles).

**Implementation notes:**
- Most of these are **cache‑key composition** (Vary) or **directive toggles** (TTL, stale, error‑cache, cookie‑strip) — render them per zone into the `server` block (or the Lua layer where dynamic, e.g. query‑sort, webp/avif/device class). Reuse the Lua layer from Phase‑4 Step‑5 for the dynamic key bits.
- **Compose cleanly** with existing Cache Rules (rules override these defaults where they match) and Phase‑1 video/static behavior.
- **Defaults = current behavior** so existing zones (incl. `cdn.a2zjav.com`) are unchanged until the user toggles something. A change bumps `config_version` → edges pull → reload (zero‑downtime).
- Dashboard: a clean **Cache Settings** panel (Voltage), grouped like the reference (Smart Cache, expiration, query handling, Vary, cookies, large‑object, stale), with plain‑language hints + the GeoIP dependency noted for country/state vary.

---

## Acceptance (definition of done)
```bash
# PART 1
# 1) Dashboard rebuilt + redeployed (new build hash); browser shows it
# 2) Logs nav: NO "SOON"; real logs page loads live data (filters + pagination + cache status/timing)
# 3) Security nav: NO "SOON"; real WAF page loads + saves (on/off, detect/block, CRS, custom rules, rate limits, events)
# PART 2
# 4) Delete a TEST zone -> within ~20-30s ALL edges: vhost gone (hostname -> default_server/404, no stale HITs) AND cache purged (verify per edge)
# 5) Whole-zone purge replays on a reconnecting edge (JetStream durability); deleted zone doesn't resume serving
# 6) Deleting an in-rotation/live zone requires type-the-hostname-to-confirm (the accidental-delete guard)
# PART 3
# 7) Cache Settings panel renders per zone; each control persists (migration) + bumps config_version on change
# 8) Smart Cache, edge/browser TTL, query-string sort, cache-error-response, strip-cookies, large-object (slice), stale (offline/updating) each verifiably change edge behavior
# 9) Vary Cache: enabling WebP/AVIF / device / country / cookie / query / header adds that dimension to the cache key (distinct cached variants); $host always in key
# 10) Defaults = current behavior; cdn.a2zjav.com unchanged until toggled; cache rules still override where they match
# 11) Live-site safety: cdn.a2zjav.com served throughout; nothing enabled on live zones unintentionally; build/vet/tsc clean
```
**Done when:** (1) Logs + Security are **real and visible** (no "SOON") in the running dashboard, (2) **deleting a zone tears it down across all PoPs in ~20–30s** (vhost removed + cache purged, durable, with an accidental‑delete guard), and (3) a **per‑zone Cache Settings panel** exposes the Bunny‑style controls wired into the agent — all with defaults preserving current behavior and the live site untouched.

## Pitfalls (do not skip)
1. **Part 1 must be verified in the browser**, not just "build passes" — the last attempt claimed done but "SOON" remained. Confirm the served build actually changed.
2. **Zone delete must purge cache AND remove the vhost** — removing only the record leaves edges serving stale cache (the exact problem); do both, fast, durably.
3. **Accidental‑delete guard** — type‑the‑hostname confirm for in‑rotation zones (the `cdn.a2zjav.com` incident must not recur).
4. **Cache Settings defaults = current behavior** — don't change existing zones until toggled; `$host` stays in every cache key (tenant isolation).
5. **Compose, don't conflict** — Cache Settings are zone defaults; Cache Rules override where matched; Phase‑1 video/static still works; slice toggle = the existing module.
6. **GeoIP‑dependent Vary (country/state)** — note it needs the GeoLite2 DB on edges; don't silently fail.
7. **Everything via config_version pull + NATS purge** — no new transport; zero‑downtime reloads.
8. **Order: 1 → 2 → 3** — land the quick fixes first; #3 is the big build.
9. **Live‑site safety throughout** — `cdn.a2zjav.com` never drops; one careful change at a time.
```
