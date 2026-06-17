# Feature comparison — Bunny vs Cloudflare vs Brisk v1

Legend: ✅ has it · ⚠️ partial/limited · ❌ not applicable. "v1?" = ship in the first Brisk
dashboard. Every Brisk v1 row maps to a **real existing endpoint** from Steps 1–5; gaps are
flagged for 6.1+ (don't invent API).

| Feature | Bunny | Cloudflare | Brisk v1? | Brisk screen | API / Notes |
|---|---|---|---|---|---|
| Network overview / home KPIs | ✅ | ✅ | ✅ **v1** | Overview | `GET /api/v1/overview` (online servers, req/s, bandwidth_bps, hit_ratio, window). |
| Edge servers / PoP management | ❌ (abstracted) | ⚠️ (network settings, no machines) | ✅ **v1 — Brisk differentiator** | Servers | `GET /servers`, `GET /servers/{id}`, `GET /servers/{id}/live`. We own the metal. |
| Add edge server (provisioning) | ❌ | ❌ | ✅ **v1** | Servers → Add | `POST /servers` + stream `GET /servers/{id}/provision-log`. SSH onboarding. |
| Live PoP health (CPU/RAM/disk) | ❌ | ❌ | ✅ **v1** | Servers detail | `GET /servers/{id}/live` (cpu_pct/ram_pct/disk_pct/req_per_sec/bandwidth/hit_ratio). |
| Reprovision / rotate token | ❌ | ⚠️ (API tokens) | ✅ **v1** | Servers detail | `POST /servers/{id}/reprovision`, `POST /servers/{id}/token/rotate`. |
| Zones / pull zones (list + create) | ✅ | ✅ (sites) | ✅ **v1** | Zones | `GET/POST /zones`, `GET/PUT/DELETE /zones/{id}`. |
| Origin config (URL, host, TLS mode) | ✅ | ✅ | ✅ **v1** | Zones → settings | zone fields: origin_url, tls_mode, cdn_hostname, custom_domain. |
| HLS/video options (slice, TTLs, CORS) | ✅ (Stream) | ⚠️ (cache rules) | ✅ **v1** | Zones → settings | zone fields: video, profile, playlist_ttl, segment_ttl, cors_origin. Brisk core strength. |
| Assign zone ↔ server (multi-PoP) | ❌ (auto) | ❌ | ✅ **v1** | Zones / Servers | `GET/POST /servers/{id}/zones`, `DELETE /servers/{id}/zones/{zoneId}`. |
| Cache / edge rules | ✅ (Edge Rules) | ✅ (Cache Rules) | ⚠️ **v1 basic** | Zones → Rules | `GET/POST /zones/{id}/rules`, `DELETE …/rules/{rid}` (path_prefix/extension/regex → override_ttl/bypass/force_download/redirect, priority). |
| Brotli/compression level | ✅ | ✅ | ✅ **v1** | Zones → settings | zone field brotli_level. |
| Purge by URL | ✅ | ✅ | ✅ **v1** | Purge | `POST /zones/{id}/purge` {type:url}. |
| Purge by prefix | ⚠️ (wildcard) | ✅ | ✅ **v1** | Purge | `POST /zones/{id}/purge` {type:prefix}. |
| Purge whole zone | ✅ | ✅ | ✅ **v1** | Purge | `POST /zones/{id}/purge` {type:zone}. |
| Purge everything (all zones) | ✅ | ✅ | ✅ **v1** | Purge | `POST /purge/all`. |
| Purge by cache-tag | ✅ (CDN-Tag) | ✅ | ❌ future | Purge | both support it (Bunny via `CDN-Tag` header + "Search tag"; CF via `Cache-Tag`); Brisk needs tag headers first. |
| Purge by hostname | ⚠️ | ✅ | ❌ future | Purge | needs multi-host zones; later. |
| Purge job status/history | ⚠️ | ⚠️ | ✅ **v1 — Brisk plus** | Purge | `GET /purge/jobs` (status pending/done/partial, edges_done/total). |
| Analytics: requests & bandwidth over time | ✅ | ✅ | ✅ **v1** | Analytics | `GET /stats?...&resolution=1m` (server/zone/time filters). |
| Analytics: cache hit ratio | ✅ | ✅ | ✅ **v1** | Analytics/Overview | computed at read from sum(hits)/(hits+misses). |
| Analytics: by status code | ✅ | ✅ | ❌ future | Analytics | not in stats schema yet; flag for 6.1+ (needs status breakdown in agent stats). |
| Analytics: geo / by country | ✅ | ✅ | ❌ future | Analytics | needs per-request geo enrichment; later. |
| Analytics: top-N (paths/zones/referrers) | ✅ | ✅ | ⚠️ partial | Analytics | top zones/PoPs derivable from stats; top paths/referrers need logs. |
| Time-range picker | ✅ | ✅ | ✅ **v1** | Analytics/Overview | client control over `from`/`to`/`resolution` on `/stats`. |
| Logs (live tail) | ✅ | ✅ (Instant Logs) | ⚠️ **v1 basic** | Logs | live request view; **data gap**: no logs API yet → flag for 6.1+ (read agent access stream / new endpoint). |
| Logs (historical/queryable) | ✅ (ClickHouse) | ✅ (Logpush) | ❌ future | Logs | later phase. |
| Log forwarding/export | ✅ | ✅ | ❌ future | Logs | later. |
| Security / WAF / rate limit / bots | ✅ (Shield) | ✅ | ❌ **Phase 4** | (reserved nav) | explicitly out of v1; note "coming later". |
| Geo-blocking / signed URLs / referrers | ✅ | ✅ | ❌ future | Zones → Security | later (token auth, allow/deny). |
| DNS management | ✅ | ✅ | ❌ future | — | Phase 2+ uses Bunny DNS for routing; not an admin screen v1. |
| TLS / custom hostnames + auto-cert | ✅ | ✅ | ⚠️ partial | Zones → settings | agent does Let's Encrypt; surface status in v1, full cert mgmt later. |
| Origin shield / tiered cache | ✅ | ✅ | ❌ future | — | later (origin-shield). |
| Load balancing / origin groups | ✅ | ✅ | ❌ future | — | later. |
| Storage / object storage | ✅ | ✅ (R2) | ❌ future | — | not a CDN-admin v1 screen. |
| Stream / video library mgmt | ✅ | ⚠️ (Stream) | ❌ future | — | Brisk delivers HLS via zones; a media library is a later product. |
| Edge compute / serverless | ✅ (Scripting/Containers) | ✅ (Workers) | ❌ future | — | later. |
| API tokens / keys | ✅ | ✅ | ⚠️ **v1 basic** | Settings | agent tokens exist; surface token mgmt minimally. |
| Team members / roles | ✅ | ✅ | ❌ future | Settings | customer-portal era (account_id scoping ready). |
| Billing / usage | ✅ | ✅ | ❌ future | Settings | customer-portal era. |
| Audit log | ✅ | ✅ | ❌ future | Settings | nice-to-have later. |
| Global search (⌘K) | ⚠️ | ✅ | ✅ **v1 (client-side)** | Nav shell | jump to zone/server/screen; no new API needed. |
| Dark/light mode | ✅ | ✅ | ✅ **v1** | Nav shell | saved preference. |
| Empty/loading/error states | ✅ | ✅ | ✅ **v1** | all | skeletons + explicit states. |

## v1 scope summary
**Build now (6.1+):** Nav shell (sidebar + top bar + ⌘K + dark mode), Overview, Servers
(+Add/detail/live), Zones (+create/edit + basic cache rules), Analytics (time-series + KPIs +
hit ratio + time-range), Purge (modes + job history), Logs (basic live view). Minimal Settings
(tokens) optional.

**Data gaps to flag for 6.1+ (do NOT invent now):**
- **Logs API** — no endpoint yet; Logs screen needs one (live tail of agent access logs).
- **Status-code breakdown** + **geo/country** + **top paths/referrers** — not in current stats
  schema; require agent/stats enrichment.
- **Cache-tag / hostname purge** — need tag headers / multi-host zones.

**Explicitly later:** Security/WAF (Phase 4), Members/roles + Billing + Audit (customer portal,
Phase 3+), Storage/Stream/Compute/DNS/Load-balancing/Origin-shield (future products).
