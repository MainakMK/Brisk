# Brisk CDN — Phase 2 / Step 6.3 Build Prompt (Zones Page + Cache Rules)

**For Claude Code.** Context in the repo: `CLAUDE.md` + `docs/Brisk_Phase1_Build_Spec.md` + the Phase‑2 Step‑1…5 prompts + `dashboard-reference/` + the Step‑6.1/6.2 prompts. **Steps 1–5 + 6.0 + 6.1 + 6.2 are complete:** `brisk-control` exposes the full API; `brisk-dashboard` (React + TS + Vite + Tailwind v4 + shadcn primitives + Recharts, **Voltage palette, dark default**) has the app shell, Overview (real `/overview`), and the **Servers page + Add Server** flow with live tiles + provisioning‑log streaming. Zones/Analytics/Logs/Purge are still placeholders.

> **Read `CLAUDE.md`, `dashboard-reference/brisk-design-spec.md` + `brisk-design-tokens.md`, and the 6.1/6.2 prompts first.** This is **Step 6.3 of Phase 2**. Build the **Zones screen + cache‑rules editor** only — don't touch Analytics/Logs/Purge (6.4–6.5). Pass the acceptance tests, stop before 6.4.

## Step 6.3 goal (one line)
Build the **Zones page**: list/create/edit/delete zones (origin, TLS, video, CORS, Brotli, TTLs), a **per‑zone detail** with tabs, and a **cache‑rules editor** (priority‑ordered condition→action rules) — all wired to the real control plane, with edits propagating to the live edge via Step‑3 pull‑config.

## ✅ Test locally in Docker
Dashboard + `brisk-control` + TimescaleDB + NATS run locally. **Critical safety:** the live zone `brisk.mainakghosh.com` (id 2, assigned to the production edge) is managed from the control plane. Editing zones in the UI changes what the agent pulls — **don't break the live site.** Prefer creating/editing a throwaway test zone; touch the live zone carefully and watch it stay up.

---

## API this screen uses (already built — backend frozen)
```
GET    /api/v1/zones                      # list: id, name, cdn_hostname, custom_domain, origin_url, status, config_version, video, profile, ...
POST   /api/v1/zones                      # create
GET    /api/v1/zones/{id}                 # one zone (+ its cache_rules)
PUT    /api/v1/zones/{id}                 # update (bumps config_version + updated_at)
DELETE /api/v1/zones/{id}
GET    /api/v1/zones/{id}/rules           # list cache rules
POST   /api/v1/zones/{id}/rules           # add rule
DELETE /api/v1/zones/{id}/rules/{rid}     # delete rule
GET    /api/v1/servers/{id}/zones         # which zones a server serves (assignment)
POST   /api/v1/servers/{id}/zones         # assign zone to server
DELETE /api/v1/servers/{id}/zones/{zoneId}
```
Zone fields (from the schema): `name, cdn_hostname, custom_domain, origin_url, tls_mode (selfsigned|mkcert|letsencrypt), video (bool), profile (vod|live), playlist_ttl, segment_ttl, cors_origin, brotli_level, status, config_version`. Cache‑rule fields: `priority, match_type (path_prefix|extension|regex), match_value, action (override_cache_ttl|bypass_cache|force_download|redirect), action_value`. If the UI needs a field not exposed, **flag the gap** — don't invent API.

## Part 1 — Zones list
A clean table/list (per `brisk-design-spec.md`, Voltage) of all zones:
- Columns: **name**, **cdn_hostname** (+ custom_domain if set), **origin**, **status** pill, **video** badge (vod/live), and a small **config_version** indicator. A kebab menu (Edit · Manage rules · Assign to servers · Delete).
- **"Add Zone"** primary button (also the top‑bar quick action).
- Search/filter by name/hostname; server‑side friendly (small dataset now, but keep the query parametric for later).
- Row click → per‑zone detail. Skeleton/empty ("create your first zone")/error states.

## Part 2 — Create / Edit zone (form)
A dialog/sheet (create) and an edit form (detail), using shadcn form components + client‑side validation (Zod or equivalent):
- **Basics:** name, `cdn_hostname` (unique), optional `custom_domain`.
- **Origin:** `origin_url` (validate URL).
- **TLS:** `tls_mode` select (selfsigned | mkcert | letsencrypt).
- **Delivery:** `video` toggle; when on, show `profile` (vod|live), `playlist_ttl`, `segment_ttl`. `cors_origin`. `brotli_level` (1–11, default 5; note: dynamic content uses ~4–6).
- **Status:** active/disabled.
- On submit → `POST`/`PUT`; **PUT bumps `config_version`** server‑side (already implemented) → invalidate the `zones` query + the zone detail. Surface a small "changes will reach edges within the poll interval (~15s)" hint so the user knows propagation isn't instant (that's by design; purge is the instant path, Step 6.5).

## Part 3 — Per‑zone detail (tabbed)
Route `/zones/:id` with tabs (per the design spec):
- **Overview:** key settings at a glance, `config_version`, `updated_at`, which servers serve it (from `/servers/{id}/zones` inverse — list assignments).
- **Settings:** the edit form (Part 2).
- **Cache Rules:** the rules editor (Part 4).
- **Assignments:** assign/unassign this zone to servers (`POST`/`DELETE /servers/{id}/zones`), so the agent on that edge pulls it.
- *(Analytics/Logs per‑zone come later via the Analytics screen filters — link out, don't rebuild here.)*

## Part 4 — Cache‑rules editor (the meaty part)
Model rules as **priority‑ordered condition → action** entries, matching how professional CDNs work: a rule pairs a **match condition** (what requests it applies to) with an **action** (what to do), and **rules are evaluated in priority order** (top‑down, with ordering determining precedence). Build:
- An **ordered list** of rules for the zone, **drag‑to‑reorder** (or up/down controls) that writes back `priority`. Show the evaluation order clearly (rule 1 first).
- **Add/Edit rule** form:
  - **Match:** `match_type` (path_prefix | extension | regex) + `match_value` (e.g. prefix `/assets/`, extension `m3u8`, or a regex). Validate regex client‑side.
  - **Action:** `override_cache_ttl` (+ a TTL value), `bypass_cache`, `force_download`, `redirect` (+ target). Show the relevant `action_value` field per action.
- Make the **cache‑eligibility vs TTL** distinction clear in helper text: a rule can change *how long* something is cached or *whether* it's cached, but origin `Cache‑Control` still matters (mirror the real Nginx behavior from Phase 1 — e.g. `.m3u8` short‑TTL, segments long).
- Each add/delete → `POST`/`DELETE .../rules` → **bumps the zone `config_version`** (already wired) → invalidate queries. Same "~15s to propagate" hint.
- Empty state: explain what edge rules do + an example (e.g. "cache `/assets/*` for 30 days").

> Note: the **agent currently carries cache_rules in its pulled config but the fixed Nginx templates don't render them yet** (flagged in Step 3/5 reports — edge‑rule rendering is a later feature). So in this step the editor **manages rules in the control plane** (create/order/delete, propagated via config_version); actually *enforcing* them at the edge is a future agent task. Make the UI honest: rules are saved + versioned now; add a subtle note that edge enforcement of custom rules is rolling out (don't imply instant enforcement). The built‑in video/static caching from Phase 1 already works regardless.

## Part 5 — States & polish
- Skeletons, empty, error/retry everywhere; optimistic‑ish UX via query invalidation (not fake optimism on destructive ops).
- **Live‑site safety:** a confirm step when editing/deleting the production zone or unassigning it from the live edge ("this changes what the live edge serves"). The control plane already rejects empty‑zone configs at the agent (Step 3), but the UI should still warn.
- Accessibility (labeled inputs, focus‑trapped dialogs, keyboard reorder fallback), responsive, Voltage tokens, dark/light.

---

## Acceptance tests (Step 6.3 definition of done — local Docker)
```bash
docker compose up --build -d
open http://localhost:5173/zones
# 1) List shows existing zones (incl. brisk.mainakghosh.com) with status + video badges + config_version
# 2) Create a TEST zone (origin/TLS/video settings) -> appears in the list; config_version=1
# 3) Edit the test zone (e.g. segment_ttl) -> PUT bumps config_version; "propagates in ~15s" hint shown
# 4) Detail tabs work: Overview / Settings / Cache Rules / Assignments
# 5) Cache rules: add a few (extension m3u8 -> override_cache_ttl 2s; path_prefix /assets/ -> override 30d),
#    reorder by priority, delete one -> each change bumps config_version; list reflects order
# 6) Assignments: assign the test zone to the (test) server; verify it shows under that server's zones
# 7) Live-site safety: editing/deleting brisk.mainakghosh.com or unassigning it prompts a clear confirm
# 8) Propagation (optional, with the real edge reachable): edit a zone -> within ~15s the agent re-pulls
#    (config_version change) -> nginx reloads; the live site stays up the whole time
# 9) Empty/skeleton/error states render; responsive + dark/light correct
npm run build      # type-check + prod build pass
```
**Done when:** zones can be listed/created/edited/deleted, the per‑zone detail tabs work, the **cache‑rules editor** manages priority‑ordered condition→action rules (saved + versioned in the control plane), zone↔server **assignments** work, every change **bumps `config_version`** for pull‑config propagation, the **live site is protected** by confirms and never dropped, and the UI is **honest** about rule propagation (~15s) and that edge enforcement of custom rules is a future agent feature — all in the Voltage design, verified locally.

---

## Pitfalls (do not skip)
1. **Don't break the live site** — confirm on edits/deletes/unassignment of `brisk.mainakghosh.com`; never leave the production edge with zero zones.
2. **Be honest about propagation** — config changes reach edges on the **~15s pull interval**, not instantly (purge is the instant path, 6.5). Show the hint.
3. **Be honest about rule enforcement** — cache_rules are **stored + versioned** now; the edge's fixed templates don't enforce custom rules yet (future agent work). Don't imply instant edge enforcement.
4. **`config_version` bump is the propagation trigger** — rely on the existing PUT/rules behavior; after any change, invalidate the `zones`/zone/rules queries.
5. **Validate inputs** — unique `cdn_hostname`, valid `origin_url`, valid regex for regex rules, sane TTLs; handle 409 on duplicate hostname.
6. **Backend frozen** — use existing endpoints; flag any missing field instead of inventing API or editing the control plane.
7. **Voltage + Recharts theming** — match 6.1/6.2; shadcn forms; no clashing theme.
8. **Scope** — Zones + rules + assignments only. No Analytics/Logs/Purge here.

## Next — Step 6.4 (do NOT start)
**Analytics + Logs:** Tremor/Recharts time‑series (bandwidth, req/s, hit/miss) from `GET /stats?...&resolution=1m` with PoP/zone/time filters, and the Logs screen (currently a reserved "coming soon" slot — keep it honest until a logs API exists). Wait for the user's go‑ahead and a Step 6.4 prompt.
