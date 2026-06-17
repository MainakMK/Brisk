# Brisk CDN — Dashboard Wiring (Logs + Security pages + hostname check)

**For Claude Code.** Context: `CLAUDE.md` + `dashboard-reference/brisk-design-spec.md` + the Phase‑4 Step‑4 (WAF) + Step‑6 (logs API) prompts + `docs/Control_Plane_Ops.md`. **The control plane is now refreshed to v17 and the backends are LIVE:** the **logs pipeline** works (`request_logs` populated, `GET /api/v1/zones/{id}/logs` + admin `/logs` return real tenant‑scoped rows with cache status + timing), and the **WAF backend** works (per‑zone enable/mode, managed OWASP CRS, custom rules, rate limits, `security_events`, endpoints `GET/PUT /zones/{id}/waf`, `/waf/rules`, `/waf/ratelimits`, `/zones/{id}/security-events`). **But the dashboard sidebar still shows "SOON" on Logs and Security** — those two pages are still the OLD placeholders from Phase‑2 Step‑6.0; they were never wired to the now‑live APIs. This is the last front‑end gap.

> **Read `CLAUDE.md` and `dashboard-reference/brisk-design-spec.md` first.** This is a **frontend‑only wiring task** — no backend, no agent, no edge changes, no risk to live sites. Replace the two placeholder pages with real screens calling the already‑built APIs, drop the "SOON" labels, and confirm the zone‑create hostname UX. Build + verify locally; the dashboard talks to the live (laptop) control plane.

## Goal (one line)
Wire the **Logs** and **Security** dashboard pages to their already‑live APIs (replacing the "coming soon" placeholders + removing the SOON labels), and confirm/clean up the **zone‑create CDN‑hostname** flow — all front‑end only.

## Part 1 — Logs page (real, replace the placeholder)
The Logs nav item still renders the Phase‑2 "coming soon" placeholder. Replace it with the **real logs view** (the spec existed in Phase‑4 Step‑6):
- Call `GET /api/v1/zones/{id}/logs?from&to&filters` (+ admin cross‑tenant `/logs`). Show recent‑first, **paginated/virtualized** (don't render thousands of rows client‑side).
- Columns: time, method, host (zone), path, status (colored), **cache status** (HIT/MISS/BYPASS), bytes, **timing** (`request_time`/`upstream_response_time`), client IP, country (if present), edge, request‑id.
- **Filters:** zone, time range, status, cache status, path, IP, country — URL‑synced like Analytics.
- **Near‑real‑time refresh** (sane interval, e.g. 5–10s for short ranges; don't hammer); skeleton/empty/error states (empty = honest "no requests in range", not fake rows).
- Tenant‑scoped via RBAC (a customer sees only their zones; admin all) — reuse the existing auth/`authHeader()` layer.
- Remove the **"SOON"** label from the Logs nav item.

## Part 2 — Security page (real, replace the placeholder)
The Security nav item (under "PLATFORM") still shows "SOON". Build the real **per‑zone Security/WAF screen** (spec from Phase‑4 Step‑4), wired to the live WAF APIs:
- **Zone selector** + **WAF on/off** + **mode** (Detect / Block) toggle with a clear explainer (detect = log‑only).
- **Managed protection:** enable **OWASP CRS** (+ sensitivity/paranoia) + the **WordPress preset** (one‑click), plain‑language descriptions. → `GET/PUT /zones/{id}/waf`.
- **Custom rules** editor: ordered list, condition builder (IP/CIDR / country / path / method / header / UA → Block/Challenge/Log/Allow), drag‑reorder, enable/disable. → `/zones/{id}/waf/rules`.
- **Rate limits** editor: path + N requests / period + key (ip / ip+path) + action + count‑mode (all / errors‑only). → `/zones/{id}/waf/ratelimits`.
- **Security events** view: recent blocks/logs with rule/IP/country/path/action + filters; the "would‑block" tuning view for detect mode. → `/zones/{id}/security-events` (+ admin cross‑tenant).
- Honest hints: rate‑limit counters approximate + per‑edge; detect mode recommended first; **GeoIP country rules need the GeoLite2 DB on the edges** (note as "available once GeoIP is enabled" rather than silently failing).
- Tenant‑scoped (RBAC); remove the **"SOON"** label.

## Part 3 — Zone‑create hostname UX (confirm + clean up)
The user asked: *adding a zone should give it its own CDN hostname (e.g. `test-site.cdn.a2zjav.com`).* The multi‑tenant backend (Phase‑4 Step‑1) already keys routing on a unique `cdn_hostname`. **Confirm + polish the create flow:**
- When creating a zone, the form should make the **CDN hostname clear**: either auto‑generate a sensible default from the zone/site name (e.g. `<slug>.cdn.a2zjav.com`) **or** let the user enter it — whichever the current form does; make it **obvious and copy‑able** on the zone detail (it's what they'll CNAME to).
- Enforce **uniqueness** (the API already 409s on dup — surface that cleanly in the UI).
- Show the hostname prominently after creation with a **copy button** + a short "point your domain here via CNAME, or add a custom domain in the Domains tab" hint.
- If the current form doesn't follow the `<name>.cdn.a2zjav.com` pattern the user expects, adjust the default/placeholder to match (base domain configurable, not hardcoded).
- **No backend change** — just the create/detail UX around the existing `cdn_hostname` field.

## Part 4 — Sweep
- Grep the dashboard for any remaining **"SOON" / "coming soon"** placeholders and confirm only genuinely‑unbuilt things (if any) keep them; Logs + Security must be real.
- Keep Voltage design, dark/light, skeleton/empty/error, responsive, accessibility — consistent with the rest of the app.

## Acceptance (definition of done)
```bash
npm run build      # type-check + prod build pass
open http://localhost:5173
# 1) Logs nav: no "SOON"; page shows REAL live requests from /zones/{id}/logs with cache status + timing; filters work (URL-synced); paginated; honest empty state
# 2) Security nav: no "SOON"; per-zone WAF on/off + Detect/Block; managed CRS + WP preset toggles; custom-rule editor; rate-limit editor; security-events view — all wired to live APIs and saving (config_version bumps where relevant)
# 3) WAF still OFF by default on live zones (the page reflects real state; don't auto-enable anything)
# 4) Zone create: new zone shows a clear, unique CDN hostname (e.g. <name>.cdn.a2zjav.com), copy-able, with a CNAME hint; dup hostname -> clean 409 message
# 5) Tenant scoping: a customer account sees only its own zones' logs/security; admin sees all
# 6) No remaining false "coming soon" on built features; Voltage/dark-light/responsive/skeletons intact
```
**Done when:** the **Logs** and **Security** pages are real, wired to the live APIs, and no longer say "SOON"; the zone‑create flow shows a clear, unique, copy‑able **CDN hostname**; everything is tenant‑scoped and matches the Voltage design — all front‑end only, with the live sites untouched.

## Pitfalls (do not skip)
1. **Frontend‑only** — no backend/agent/edge changes; the APIs already exist and return real data.
2. **Don't auto‑enable WAF/shield/rules** on live zones — the Security page reflects real (off) state; enabling is the user's deliberate action.
3. **Honest empty/loading states** — Logs empty range = "no requests", not fake rows; GeoIP‑dependent country rules noted as needing the GeoLite2 DB, not silently broken.
4. **Paginate/virtualize logs** — never dump thousands of rows into the DOM.
5. **Reuse `authHeader()` + RBAC** — tenant scoping for logs/security; don't leak cross‑tenant.
6. **Hostname UX only** — confirm/polish around the existing `cdn_hostname`; keep the base domain configurable, don't hardcode; surface the 409 cleanly.
7. **Sweep for stale "SOON"** — only genuinely unbuilt items keep it.

## After this
The dashboard fully reflects what's built: Overview, Servers, Zones, Domains, Analytics, **Logs (real)**, Purge, DNS, **Security (real)**, Settings — all usable on the live fleet for your own sites. Next is just **onboarding your sites** (zone → hostname → optional custom domain → cache rules) at your pace.
