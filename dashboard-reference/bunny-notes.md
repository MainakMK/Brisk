# Bunny.net — feature + UX-flow notes (in our own words)

**Capture method:** public documentation + product pages only (`docs.bunny.net`, API
reference, `llms.txt` index). No logins, no in-dashboard clicks, no screenshots/CSS/assets.
This is a *functional* description of what Bunny does and how the flows are structured —
used to decide what Brisk needs, not to copy their UI. Brisk's visual design is its own.

> Why study Bunny: it's the closest competitor to Brisk's positioning — a no-frills,
> developer-friendly, pay-as-you-go CDN with strong HLS video delivery. Their product
> taxonomy is a good map of "what a modern self-serve CDN exposes."

---

## Product taxonomy (top-level navigation)
Bunny is multi-product. The left/primary nav is a list of **products**, each with its own
sub-navigation:

- **CDN** — pull zones (the core "accelerate a site/origin" product).
- **Stream** — video libraries (upload/encode/deliver HLS, DRM, watermark, captions).
- **Storage** — global object storage zones (origin for the CDN, or standalone).
- **Optimizer** — automatic image/CSS/JS optimization layered on a pull zone.
- **Magic Containers** — deploy Docker apps at the edge (compute).
- **Scripting (Edge Scripting)** — serverless JS at the edge (Bunny's "Workers").
- **Database** — serverless SQLite over HTTP.
- **Shield** — WAF / DDoS / security layer.
- **DNS** — authoritative DNS with scripting.
- **Account & Billing** — account settings, team management, billing, affiliate, API keys, 2FA, audit log.

**Brisk relevance:** Brisk v1 = the **CDN (pull zones)** product + **stats** + **purge** +
**servers/PoPs** (Brisk owns its own edges, which Bunny abstracts away). Stream, Storage,
Optimizer, Containers, Scripting, Database, Shield, DNS are future phases (mostly Phase 3+/4).

---

## CDN / Pull Zones (the core area — maps directly to Brisk Zones)

**What it is:** a "pull zone" connects a public hostname to an **origin** (a URL, a Bunny
storage zone, or a load-balanced origin group). Bunny caches origin responses at its edge
PoPs and serves them globally.

**Where it lives:** CDN product → list of pull zones → click a zone → per-zone settings
organized into tabbed sub-sections.

**List view (data/controls shown):** table/cards of pull zones — name, the CDN hostname
(`*.b-cdn.net`), status, and a quick traffic/health indicator. Primary action: **Add Pull Zone**.

**Add-pull-zone wizard (the create flow):**
1. Name the zone (becomes the `*.b-cdn.net` hostname).
2. Enter the **origin** — origin URL, or pick a storage zone, or an origin group (load balancer).
3. Choose **pricing tier / geo-replication regions** (which continents to serve from).
4. Create → zone goes live on the platform hostname immediately; optional custom hostname + TLS added after.

**Per-zone settings (sub-sections — this is the important IA):**
- **General / Hostnames** — the `*.b-cdn.net` host + add **custom hostnames** (CNAME) with
  free auto-TLS (Let's Encrypt) or uploaded certs; force-SSL/HSTS toggles.
- **Origin** — origin URL/host, host-header override, origin connect/response timeouts,
  follow-redirects, **origin shield** (a mid-tier cache to protect origin), origin SSL verify.
- **Caching** — cache expiration / "cache control" behavior, query-string handling
  (ignore/specific/sort), cache by `Vary`, stale-while-revalidate / stale-on-error,
  "smart cache" defaults, **cache key** customization, browser cache TTL.
- **Edge Rules** — the powerful "if request matches X, then do Y" engine (see below).
- **Security** — token authentication (signed URLs), allowed/blocked referrers, allowed/blocked
  countries (geo-blocking), allowed IPs, rate limiting, bot/Shield hooks.
- **Headers** — add/override request & response headers, CORS settings.
- **Optimizer** — toggle image/asset optimization for the zone.
- **Statistics** — per-zone analytics (same shape as global stats, scoped to the zone).
- **Logging** — enable real-time/raw request logs, log forwarding destinations.
- **Storage / Replication** — geo-replication regions for the zone's cache tier.

**UX pattern worth adopting:** a zone is a single object with many **tabbed setting groups**;
the list is shallow (name + host + status), depth is in the per-zone tabs. Create flow is a
short wizard (name → origin → regions → done), settings come later.

---

## Edge Rules (Bunny's "if-this-then-that" for requests)
**What it does:** ordered rules evaluated per request. Each rule = **trigger(s)** (match on
URL, path, extension, country, request header, query param, status code, etc.) + **action(s)**
(override cache TTL, bypass cache, redirect, set/override headers, block, set CORS, etc.).
**Where it lives:** per-zone → Edge Rules tab; list of rules with priority/order, enable/disable
toggle per rule, add/edit/delete. **Flow:** add rule → pick trigger type + value → pick action +
value → set priority → save. **Brisk relevance:** maps to Brisk's `cache_rules`
(`match_type` ∈ path_prefix/extension/regex, `action` ∈ override_cache_ttl/bypass_cache/
force_download/redirect, plus `priority`). Brisk v1 ships a simpler version of this.

---

## Purge / Invalidation
**What it does:** clears cached content so the edge re-fetches from origin.
**Forms:** **Purge URL** (single file), purge by URL with wildcard, and **purge the whole
zone**. There's a global "Purge Cache" action and a per-zone purge.
**Where it lives:** a prominent per-zone action (and a global purge), plus an API endpoint.
**Flow:** enter URL (or choose "purge everything for this zone") → confirm → purge executes
near-instantly across edges; a toast/confirmation reports success.
**Brisk relevance:** directly maps to Brisk `POST /zones/{id}/purge` (type url|prefix|zone) +
`POST /purge/all` + job status `GET /purge/jobs`. Brisk already does instant purge over NATS.

---

## Statistics / Analytics
**What it shows:** bandwidth, requests, **cache hit rate**, data served, requests by status
code, geographic distribution (per-region/continent), top referrers, and origin-shield/SafeHop
(origin reliability) stats. Optimizer and Stream have their own stat sets (image optimization
savings, DRM, transcribing).
**Controls:** **time-range selector**, granularity, and filters by **pull zone** and **region**.
**Chart types:** time-series line/area for bandwidth & requests over time; big-number KPI tiles
for totals & hit ratio; a **map / per-region breakdown** for geo distribution; tables for top-N.
**Where it lives:** global Statistics view + a per-zone Statistics tab (same charts, scoped).
**Brisk relevance:** maps to `GET /overview` (KPIs) + `GET /stats?...&resolution=1m` (time-series,
filterable by server/zone/time). Geo map is a future enhancement (Brisk has PoP-level data now).

---

## Logs (real-time + raw request logs)
**What it does:** access to **raw CDN request logs** per pull zone — fields like timestamp,
status, bytes, cache result (HIT/MISS), client country, URL, referrer, user-agent, edge PoP.
Logs are queryable (Bunny's v2 logging is backed by ClickHouse with filter pushdown) and can be
**forwarded** to external destinations; also **origin error logs** per zone/date.
**Where it lives:** per-zone Logging tab + a Logs API. **Controls:** time range, filter by
status/country/free-text search, export/forward.
**Brisk relevance:** Brisk v1 = a real-time request view (Logs screen) reading the agent's
access stream; historical/queryable logs (ClickHouse-style) is a later phase. The **field set**
above is the model for Brisk's Logs columns.

---

## Servers / Network / Regions
**Key difference vs Brisk:** Bunny **abstracts the network** — users don't manage individual
servers; they pick **regions/geo-replication tiers** (which continents to serve from) and Bunny
runs the PoPs. There's a public network/PoP map on the marketing site, but the dashboard exposes
*regions*, not *machines*.
**Brisk relevance:** Brisk is the opposite — **we own and operate the edges**, so Brisk has a
first-class **Servers (PoPs)** screen (add server, provisioning log, live CPU/RAM/disk, per-PoP
traffic) that Bunny has no real equivalent for. This is a Brisk differentiator in the admin UI.

---

## Settings / API / Tokens / Account
**What's there:** account settings, **team management** (members/roles), **billing**
(pay-as-you-go balance, invoices), **API keys** (account-level programmatic access),
**2FA**, **audit log** (user action history), affiliate program. Per-zone **token
authentication** keys for signed URLs are separate from account API keys.
**Brisk relevance:** Brisk admin v1 needs API/token management for agents (already has the
agent-token system) and basic account settings; full billing + multi-tenant teams arrive with
the customer-portal era (Phase 3+). Audit log is a nice-to-have later.

---

## Cross-cutting UX (described in words)
- **Navigation:** product switcher (list of products) → within a product, a list of resources
  (zones) → per-resource tabbed settings. Shallow lists, deep tabs.
- **Search:** global search across resources; docs have ⌘K search.
- **Create flows:** short wizards (name → origin → regions) then progressive settings.
- **Account switching / context:** account + billing context in a top/side area.
- **Empty states:** "create your first pull zone" style prompts when a product is empty.
- **Real-time feel:** stats and purge feel near-instant; logs stream.
- **Developer-first:** every dashboard action has an API equivalent (API reference is first-class).

---

## ✅ Live logged-in capture (read-only, 2026-06-08)
Verified against the real Bunny dashboard (read-only; no clicks, no changes; **no private account
data recorded** — structure/flows only). Corrections + enrichments to the doc-based notes above:

**Global nav (actual grouping):** top bar = **Overview** · **Delivery** (CDN, Storage, Stream,
DNS) · **Purge** · **Edge Platform** (Magic Containers, Scripting, Database) · **Monitoring**
(Statistics, Logs, Origin Errors) · **Add** (quick-create) · **Balance** · **Help & Support** ·
account menu. So Purge and the Monitoring trio (Statistics/Logs/Origin Errors) are **top-level**,
not buried per-zone.

**Home dashboard (actual):** a 24h **stats strip** (Bandwidth used / Requests served / Cache hit
rate, each with a % delta) + **Service status** + **Latest pull zones** list (name + hostname +
tier) + **Latest storage zones** + docs/blog cards. Good model for Brisk Overview's "recent
resources + KPI strip."

**Per-pull-zone settings menu (the real IA — left sidebar inside a zone):**
- **General:** Hostnames · Origin · WebSockets · Pricing & routing (geo-replication tiers)
- **Caching:** General · **Origin shield** · **Request coalescing**  ← Brisk already does
  request coalescing (proxy_cache_lock); origin shield is our future mid-tier cache
- **Security:** General · Logging · SSL · 502/504 error pages · S3 authentication · **Token
  authentication** (signed URLs) · **Traffic manager** (steering/LB) · **Headers** · **Shield** (WAF)
- **Optimizer:** Settings · Image classes · Burrow (preview) · Prerender (preview)
- **Statistics** (per-zone) · **Edge rules** · **Network limits**
- Per-zone header shows **usage this month** (Traffic / Requests / Cache HIT) + a **Purge Cache**
  button; **Hostnames** panel = add custom hostname + CNAME instructions + SSL / Force-SSL / SSL-type table.

**Statistics page (actual controls + data):** filters = **Granularity** (e.g. Daily) · **Pull
Zone** · **Region/Datacenter**. Shows KPI tiles (Bandwidth, Requests, Cache hit rate) + **Bandwidth
served split (total/cached/uncached)** + **Requests split (total/cached/uncached)** + **Origin
response time (avg)** + **Origin traffic** + **Origin shield traffic (internal vs origin)** +
**Data-center traffic distribution (per-city list)** + **Non-2xx breakdown (3xx/4xx/5xx)**.
→ Confirms Brisk's gaps: **cached-vs-uncached split, status-code breakdown, and per-PoP/geo
distribution** are things Brisk doesn't compute yet (flagged for later).

**Logs ("Log explorer", actual):** filters by **Status code** + **Pull Zone**, **Clear filters**,
**Report** + **Download** actions, and a clear **empty state** when there isn't enough traffic.

**Purge page (actual) — CORRECTION to my doc notes:** three modes on one page —
1. **Purge URL list** — paste exact CDN URLs; **folders/wildcards via `*`** in the path supported
   (except with Perma-Cache).
2. **Purge pull zone** — select a zone, clears everything (warns about origin load).
3. **Purge by tag** — a **"Search tag"** field purges only files whose **`CDN-Tag` response header**
   matches. So **Bunny DOES support cache-tag purge** (I'd marked it ❌ earlier — corrected in
   `feature-comparison.md`). Brisk's tag purge stays a future item (needs tag headers).

**Net new takeaways for Brisk:** (a) consider top-level Purge + Monitoring nav (we already do);
(b) "Request coalescing" and "Origin shield" are first-class Bunny caching concepts — Brisk has
coalescing today, origin-shield later; (c) Bunny's per-zone settings are a deep tabbed menu — our
Zones tabs (Settings/Rules/Servers/Analytics/Purge) are the right shape, just smaller.

### Deep-dive (live, with screenshots — read-only, nothing saved/created/purged)
**Pull-zone list (actual):** table columns **Name · Origin · Domain · Tier (Standard/Volume) ·
Traffic · Cost · ⋯actions**; "Add Pull Zone" + search. Origin column shows the origin type
(URL / Storage Zone / Magic Container / Edge Script).

**Add Pull Zone wizard (full, single scrolling page):**
1. **Pull Zone name** → becomes `<name>.b-cdn.net` (letters+numbers only).
2. **Origin type**: *Origin URL* vs *Storage Zone* toggle; **Origin URL** (required) + **Host
   header** (optional; else derived from origin).
3. **Choose tier**: *Standard* vs *High Volume*.
4. **Pricing zones** = **geo-replication region toggles**, each with a per-GB price (Asia &
   Oceania / Europe / Middle East & Africa / North America / South America). Disabling a zone
   routes that region's traffic to the next-closest enabled zone.
5. **Protect with Bunny Shield** (recommended) — enable WAF/DDoS at creation.
6. Live **delivery-cost** summary + **Add Pull Zone**.
→ Brisk's create flow (name → origin → PoPs/regions → done) matches this; "pricing zones =
region toggles" is the model for Brisk's zone↔server (PoP) assignment UX.

**Edge Rules builder (the real if-this-then-that):**
- Per-zone **Edge rules** tab: ordered list; each rule shows its **Action(s)** + a **Conditions**
  block (IF ALL/ANY → rows like *Request URL* matches `*://host/*`), an enable toggle, ⋯menu;
  plus "Add Edge Rule", "Purge Cache", action-type filter, search.
- **Add edge rule form:** **Description**; **Actions** (one or more, "+ Add action"); **Conditions**
  (top-level **Match any / all / none**, each condition = **field** dropdown (e.g. Request URL) +
  **Match any/all/none** + value(s) with wildcards + "+ Add Property"; "+ Add condition").
- **Action catalog** (each tagged **Cache** or **Origin**): Add/Set Request/Response Header,
  Block Request, Browser Cache (Cache-Control) Override, Bypass AWS S3 Authentication, Bypass
  Perma-Cache, Disable Bunny Optimizer, Enable Request Coalescing, Enable Token Authentication,
  Force Download, Force SSL, Ignore Cache URL Query String, Maximum Connections Per IP, Override
  Cache Time, Redirect, Set Status Code, Origin URL Rewrite, … (~30 actions).
→ Brisk's `cache_rules` (match_type→action+priority) is a deliberately tiny subset. The
  **action+condition** model (multi-action, nested match logic) + the Cache/Origin tagging is the
  reference if Brisk grows a richer rules engine later. Several actions map to things Brisk already
  does at the nginx layer (request coalescing, force-download, cache-time override, redirect).

**Statistics (visual layout confirmed):** filter bar = **Granularity (Daily) · Pull Zone ·
Region · Date-range picker**; **KPI tiles** (Bandwidth used / Requests served / Cache hit rate)
each with a sparkline; then stacked **area charts** — *Bandwidth served* (Total/Cached/Uncached
overlay) and *Requests served* (Total/Cached/Uncached) — plus origin response time, origin-shield
traffic, per-datacenter distribution, 3xx/4xx/5xx (as noted above). → Brisk Analytics =
KPI-tiles-with-sparkline + stacked area (hits vs misses) is exactly this pattern.

### Other products (live, structure only — all out of Brisk v1 scope)
- **Storage zones** list columns: Name · Tier (Standard / S3) · Files stored · **Replication**
  (None / Replicated) · Storage size; action **Add Storage Zone**. (Bunny's object storage; Brisk
  uses customer origins, not its own storage product, in v1.)
- **Stream** = **video libraries** list (name · video count · storage · traffic) + **Add Video
  Library**. (Brisk delivers HLS via zones; a managed media library is a later product.)
- **DNS** = **DNS Zones** + **Scripts** tabs, **Add DNS zone**, empty state "create your first
  domain". (Brisk uses Bunny DNS for routing behind the scenes; not an admin screen in v1.)
- **Account area** menu: **Account settings** · **Billing** · **Billing history** · **API key** ·
  **Manage team** · Change password · 2FA · Close account. → Brisk admin v1 needs only minimal
  token/account settings; billing + team/roles arrive with the customer portal (Phase 3+).

## What Brisk should take (functionally, not visually)
1. **Zone = one object, many tabbed setting groups**; shallow list + deep per-zone tabs.
2. **Short create wizard** (name → origin → regions/PoPs → done), settings after.
3. **Edge Rules** as trigger→action list with priority (Brisk's cache_rules, simpler in v1).
4. **Purge** as a prominent per-zone action with url/prefix/zone/everything modes + a job/status trail.
5. **Stats** = KPI tiles + time-series + geo/region breakdown + top-N, with time-range + zone/region filters.
6. **Logs** with the field set above (time, status, bytes, cache result, country, URL, PoP).
7. **Developer-first**: every screen maps to an API call.
8. **Brisk's edge over Bunny's admin:** a real **Servers/PoPs** screen (we own the metal).
