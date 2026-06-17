# Cloudflare — feature + UX-flow notes (in our own words)

**Capture method:** public documentation only (`developers.cloudflare.com` — Cache, Analytics,
Rules, etc., via their markdown/`llms.txt` doc surface). No logins, no in-dashboard clicks, no
screenshots/CSS/assets. Functional description only; Cloudflare's visual design is their IP.

> Why study Cloudflare: it's the gold standard for **dashboard polish, analytics depth, and
> rules engines**. We study *what the screens do and how flows are organized*, then build
> Brisk's own (much smaller) version with our own look. Cloudflare is huge; we only mine the
> areas relevant to a CDN admin: cache, analytics, logs, purge, rules, network.

---

## Account model + navigation (the IA)
Two scopes, and the nav reflects it:
- **Account scope** — account home lists all your **websites/zones** (domains); account-wide
  analytics, members, audit log, API tokens, billing, notifications live here.
- **Zone scope** — click a domain → a **per-zone left sidebar** of product sections:
  Overview, Analytics & Logs, DNS, Email, SSL/TLS, Security (WAF/Bots/DDoS), Caching,
  Rules (Page/Transform/Redirect/Cache Rules), Network, Traffic (Load Balancing), Speed
  (Optimization), Workers Routes, etc.

**Pattern:** **account → list of resources → per-resource product sidebar.** A top bar carries
account switcher, global search, support/notifications, and the account menu. The per-zone
sidebar is the primary nav once you're inside a domain.

**Brisk relevance:** Brisk is single-account (admin) in v1 with a **global** product sidebar
(Overview, Servers, Zones, Analytics, Logs, Purge) rather than Cloudflare's account→zone nesting,
because Brisk has few zones and also manages **its own servers**. The future customer portal
adopts the account→zone-scoped pattern (customers see only their account_id's zones).

---

## Overview (per-zone home)
**What it shows:** a summary header (zone status/plan), quick stats (requests, bandwidth,
unique visitors, % cached, threats), and shortcuts to common actions. It's a glanceable landing.
**Brisk relevance:** Brisk's **Overview** is network-wide (all PoPs + all zones): hero KPIs +
recent events, mapped to `GET /api/v1/overview`.

---

## Analytics & Logs
**What it shows:** **Traffic analytics** — requests and bandwidth over time, **% cached vs
uncached**, requests by **HTTP status**, by **country**, top paths/hosts/referrers, by
content-type; **Security analytics** — threats/mitigations; **Performance**. There's also
**Web Analytics** (privacy-first client RUM) and **account-level analytics** aggregating zones.
**Controls:** **time-range picker** (last 30m → 30d, custom), comparison vs previous period,
filters (status, country, path, etc.), and an export/share option. Power users get the
**GraphQL Analytics API** (select exact metrics/dimensions) and **Workers Analytics Engine**
(custom SQL-queryable time-series) + **custom dashboards**.
**Chart types:** stacked area/line time-series for requests & bandwidth; big-number KPIs for
totals & cache ratio; bar/column for status & content-type; **world map / country table** for geo;
tables for top-N. Note: data is **sampled** at high volumes (they surface confidence intervals).
**Brisk relevance:** maps to `GET /stats?...&resolution=1m` (time-series with server/zone/time
filters) + `GET /overview` (KPIs). Brisk computes hit ratio at read time from summed counts.
Geo/country + top-N are future (need per-request log enrichment). GraphQL-style flexibility is
out of scope; Brisk exposes fixed, fast endpoints.

---

## Caching (maps to Brisk Zones → cache settings + Cache Rules)
**Sub-sections:** **Configuration** (caching level, browser cache TTL, Always Online,
development mode, tiered cache, **Purge Cache**), **Cache Rules** (the modern replacement for
Page Rules — match requests, set edge/browser TTL, cache key, bypass, cache by status),
**Tiered Cache**, **Cache Reserve** (persistent storage tier), **Cache Analytics**.
**Cache Rules flow:** create rule → define **matcher** (URL/path/extension/cookie/header/etc.)
→ choose **settings** (eligible for cache, edge TTL, browser TTL, custom cache key, serve stale)
→ set **order/priority** → deploy. Rules are ordered and evaluated top-down.
**Brisk relevance:** Brisk's per-zone **cache rules** (`/zones/{id}/rules`) are a small version
of Cache Rules (path_prefix/extension/regex → override_ttl/bypass/force_download/redirect, with
priority). Tiered cache ≈ Brisk's origin-shield (future). Cache Analytics ≈ Brisk Analytics
filtered to a zone.

---

## Purge / Invalidation
**Modes (this is the model to copy functionally):**
- **Purge by single file (URL).**
- **Purge everything** (whole zone).
- **Purge by cache-tag** (needs `Cache-Tag` response headers).
- **Purge by hostname.**
- **Purge by prefix (URL prefix).**
**Where it lives:** Caching → Configuration → **Purge Cache** (a modal/flow with the modes
above), plus an API. **Flow:** choose mode → enter targets (one or many URLs/tags/prefixes/hosts)
→ confirm → purge propagates globally in seconds; UI confirms.
**Brisk relevance:** Brisk already supports **url / prefix / zone / all** (NATS instant purge,
job-tracked). Cache-tag and hostname purge are future (need tag headers / multi-host zones).
Brisk's `GET /purge/jobs` gives the status trail Cloudflare's modal lacks (a Brisk plus).

---

## Logs
**What it does:** **Logpush** (push request logs to external storage/SIEM) and **Instant Logs**
(live tail of requests in the dashboard, Enterprise). Fields: timestamp, client IP/country,
host, path, method, status, bytes, cache status, edge response time, ray ID, user-agent, etc.
**Brisk relevance:** Brisk v1 = **Logs** screen as a live request tail (the agent's access
stream); historical/queryable logs + push integrations are later phases. Field set above informs
Brisk's Logs columns (time, PoP, zone, method, path, status, bytes, cache result, country later).

---

## Security (note for later phases — Phase 4 for Brisk)
**What's there:** WAF (managed + custom rules, rate limiting), Bot management, DDoS protection,
firewall events/analytics, page shield, SSL/TLS. **Brisk relevance:** explicitly **out of v1**
(Brisk Shield/WAF = Phase 4). Note it in nav as "coming later"; don't build.

---

## Network / Traffic
**What's there:** network settings (HTTP/3, 0-RTT, IPv6, gRPC, WebSockets), **Load Balancing**
(pools/origins/health checks/steering), Spectrum, Argo Smart Routing. **Brisk relevance:**
Brisk's **Servers (PoPs)** screen is the closest analog but is about *our* edge machines, not
customer load balancers. Load balancing/origin groups are future (Brisk origin-shield/multi-origin).

---

## Settings / API tokens / Members / Audit
**What's there:** **API Tokens** (scoped, per-permission) + legacy global API key, **Members**
(team + roles/permissions), **Audit Log** (every account action), **Notifications**, **Billing**.
**Brisk relevance:** Brisk has agent tokens already; admin needs token management + basic
settings. Members/roles/billing/audit arrive with the customer portal (Phase 3+).

---

## Cross-cutting UX (described in words)
- **Two-level nav:** account (resource list) → zone (product sidebar). Persistent top bar:
  account switcher, **global search (⌘K-style)**, notifications, help, account menu.
- **Time-range picker** is a first-class, reused control across all analytics.
- **Rules engines** everywhere follow **matcher → action → order/priority → deploy**.
- **Purge** is a guided modal with multiple precise modes.
- **Progressive disclosure:** summary KPIs first, drill into dedicated analytics/log screens.
- **States:** clear empty states ("add your first site"), skeleton loading, explicit errors,
  "sampled data" disclosure on heavy analytics.
- **Dark/light**, responsive, keyboard-friendly, heavy use of **stat cards + time-series + tables**.

---

## ✅ Live logged-in capture (read-only, 2026-06-08)
Verified against the real Cloudflare dashboard (read-only navigation; no clicks on feature
controls, no changes; **no private data recorded** — no account/zone IDs, email, or domain).
Enrichments + updates to the doc-based notes above:

**Two-level model confirmed, but the nav is now product-grouped.** Account home lists domains +
account-wide products. Inside a zone, the left sidebar is grouped into labeled sections (current
order observed):
- **Overview**
- **AI Crawl Control** (Overview / Metrics / Security / Optimization / Signals) ← new AI-bot area
- **Investigate:** Log Explorer · Log search · Trace · **Logpush**  ← this is where "Logs" live now
- **Analytics:** Dashboards · **HTTP Traffic** · Web analytics · Performance · Workers
- **DNS** (Records / Analytics / Settings)
- **Email** (Routing / DMARC / Email Security)
- **SSL/TLS** (Overview / Edge Certs / Client Certs / Origin Server / Custom Hostnames)
- **Security** (Overview / Analytics / Web assets / Security rules / Settings / Access)
- **Speed** (Observatory / Origin Analytics / RUM / Synthetic / Settings / Smart Shield)
- **Caching:** Overview · **Configuration** · **Cache Rules** · Tiered Cache · Cache Reserve
- **Workers Routes**
- **Rules** (Overview / Snippets / Cloud Connector / Page Rules / Settings)
- **Error Pages**, **Network** (Traffic / Argo / Load Balancing / Health Checks / Waiting Room / Web3)
- **Manage account:** Members · Billing · Account API tokens · Audit logs · Notifications · …

**Zone Overview (actual):** time-range toggle **24 Hours / 7 Days / 30 Days**; KPI tiles =
**Unique Visitors · Total Requests · Percent Cached · Total Data Served · Data Cached**, each with
a **Download data** action + "View more analytics"; plus Recommendations, DNS-setup, Quick Actions
(**Under Attack Mode**, **Development Mode**, Run speed test, **Configure caching**), and an
API panel (Zone ID / Account ID / token). → Good model for Brisk Overview's KPI strip + quick actions.

**Caching → Configuration (actual):** a **Purge Cache** block with two buttons — **Custom Purge**
and **Purge Everything** (Custom Purge opens a modal with the precise modes: by URL / tag /
hostname / prefix, per docs). Plus **Caching Level**, **Browser Cache TTL**, CSAM Scanning,
Crawler Hints, **Always Online**, **Development Mode** (bypass cache; doesn't purge), Query-String
Sort (Enterprise). → Brisk's purge (url/prefix/zone/all + job trail) covers the everyday modes;
tag/hostname remain future.

**Caching → Cache Rules (actual):** **Create rule** + two families on one page — **Cache Rules**
and **Cache Response Rules**, each showing an active-count + create button + empty state, with a
"Show all rule types" filter. Confirms the **matcher → settings → priority** model; Brisk's
single cache_rules table is the v1-sized version.

**Analytics → HTTP Traffic (actual):** time-range ("Previous 24 hours"); metrics **Requests /
Bandwidth / Unique Visitors / Requests Through Cloudflare**; a **world map "Requests by Country"**
(OpenStreetMap) + a **Top Traffic Countries/Regions** table; a "share your stats" widget (bytes
saved, SSL requests served, attacks blocked). → Reinforces Brisk's gaps: **geo/country map + top-N
by country** need per-request enrichment (flagged for later).

**Logs (actual):** lives under **Investigate** as **Log Explorer / Log search / Trace / Logpush**
(query + push). → Brisk's Logs stays deferred until a logs endpoint exists.

---

## ✅ Live deep-dive capture (read-only, 2026-06-08) — forms + modals
Opened the actual create-form and purge modal to read their options (no save, no submit, no
purge; cookie banner declined; no private data recorded).

**Cache Rule → "Create rule" flow (full form):**
- "Create rule" opens a menu: **Cache Rules** vs **Cache Response Rules** (two rule engines).
- **Templates** (quick-start cards): *Cache everything*, *Bypass cache for everything*,
  *Cache default file extensions* (replicate Page-Rules behavior) → "Create from template".
- **Rule name** (required).
- **If incoming requests match…**: *Custom filter expression* OR *All incoming requests*.
- **Matcher builder**: **Field** (e.g. URI Full, …) + **Operator** (e.g. wildcard, equals, …) +
  **Value**, joined with **And / Or**, with a live **Expression Preview**
  (`http.request.full_uri wildcard r"…"`) and an **Edit expression** raw editor (Wirefilter-style,
  4000-char limit).
- **Then… (the actions):** **Cache eligibility** (required: *Bypass cache* / *Eligible for cache*);
  then optional "Add setting" for **Edge TTL**, **Browser TTL**, **Cache key** (which request
  components form the key), **Serve stale while revalidating**, **Respect strong ETags**,
  **Origin error page pass-through**, … → matches the **matcher → settings → deploy** model.
  → Brisk's `cache_rules` (match_type + match_value → action + action_value + priority) is the
  intentionally-small version; the Field/Operator/Value builder is a good UX pattern to borrow.

**Custom Purge modal (exact modes observed):** **URL** (purge assets matching the URL(s) exactly,
minus single-file exclusions) · **Hostname** (any URLs with a matching host) · **Tag** (assets
served with a matching **Cache-Tag** response header) · **Prefix** (any assets under a directory) —
plus the separate **Purge Everything** button. Cancel / Purge actions. → Brisk covers URL / prefix
/ zone / all today; **hostname** + **tag** purge stay future (need multi-host zones + tag headers).

**Tiered Cache (actual):** **Smart Tiered Cache** (Cloudflare auto-picks optimal upper tier near
origin) + **Regional Tiered Cache** (add-on middle tier) + a topology preview; now bundled under
"**Smart Shield**" (origin safeguard). → Brisk analog = future **origin shield** / mid-tier cache.

**SSL/TLS Overview (actual):** **Encryption mode** (Flexible / Full / Full-strict / Off) with a
Browser↔Cloudflare↔Origin diagram + automatic mode + scan schedule; a **"Traffic Served Over TLS"**
breakdown (None / TLS 1.2 / TLS 1.3, last 24h). Sub-sections: Edge Certificates, Client
Certificates, Origin Server, Custom Hostnames. → Brisk does TLS at the agent (Let's Encrypt,
TLS 1.2/1.3); v1 surfaces cert status, fuller cert management later.

**Other sections captured at IA level (live sidebar, mostly out of Brisk v1 scope):**
Cache Reserve (persistent cache tier), Security (Overview/Analytics/Web assets/Security rules/
Settings/Access — i.e. WAF/bots), Speed (Observatory/Origin Analytics/RUM/Synthetic/Smart Shield),
Rules (Snippets/Cloud Connector/Page Rules/Settings), Network (Traffic/Argo/Load Balancing/Health
Checks/Waiting Room/Web3), DNS (Records/Analytics/Settings), Email, Workers Routes, Error Pages,
Investigate (Log Explorer/Log search/Trace/Logpush), Manage account (Members/Billing/Account API
tokens/Audit logs/Notifications). All confirmed present; none needed for Brisk v1 (see
`feature-comparison.md` for the phase mapping).

---

## ✅ Live ACCOUNT-LEVEL + Registrar capture (read-only, 2026-06-08)
The non-zone (account-scoped) surface — useful mainly as a blueprint for Brisk's **future
account/customer-portal era**, not v1. Read-only; no private data (domains/emails) recorded.

**Account Home (the account dashboard above zones):** a **bento Analytics board** with a
time-range ("Last 24 hours") and cards:
- **Security** (security insights count high/low, logins blocked), **Performance** (cache rate +
  delta + sparkline, CPU time P90), **Activity** (web traffic + sparkline, Workers invocations).
- **Domains** card = per-domain mini-row (favicon + name + sparkline + request count) — a compact
  multi-resource roll-up.
- **Workers & Pages** card, **Zero Trust security** card (seats, access apps, findings, tunnels).
- **Audit logs** card (tabs All / Dashboard / API; recent actions w/ resource + relative time).
- **Next steps** (enable SSO, invite teammates), and a global **"+ Add"**.
→ This cross-resource bento is the model for Brisk's **future customer-portal overview** (account
roll-up across that customer's zones). Brisk v1 admin Overview is the fleet-wide version of this.

**Account left-nav product catalog (grouped):** Account home · Recents · **Domains**
(Overview / Registrations / Transfers) · **Observe** (Investigate: Log Explorer/Log search/Trace/
Logpush; Analytics: Dashboards/Account analytics/Web analytics) · **Build** (Compute: Workers &
Pages, Containers, Durable Objects, Queues, Workflows, Email; AI; Storage & databases: R2/
Hyperdrive/KV/D1; Media: Stream/Images/Realtime) · **Protect & Connect** (Application security:
WAF/Turnstile; Zero Trust; Networking: Tunnels/Magic/IP addresses; Delivery & performance: Bulk
redirects, Load Balancing, Zaraz) · **Manage account** (Members, Billing, Account API tokens,
OAuth clients, Audit logs, Notifications, Shared config, Blocked content, Abuse reports, Carbon
Impact, Configurations, Tagged Resources). → 95% is outside Brisk's CDN scope; we only mirror the
*shape* (account → resources → settings) for the portal.

**Domains / Registrar (the "domain features"):**
- **Registrations** — "renew registration, update settings, edit WHOIS contacts for domains
  registered with Cloudflare." Actions: **Buy domain**, **Registrar API**, **Default contact**
  (WHOIS, redacted by default for privacy). Table: **Domain · Status (Active/Expired) · Auto-renew
  · Expires · Manage**.
- **Transfers** — transfer domains in/out.
- **Overview** — the domains/zones list.
→ Brisk is **not a registrar** and won't be in v1 (we accelerate origins, we don't sell domains).
Note only: if Brisk ever bundles DNS/domains (Phase 3+ via Bunny DNS), this Registrations table
(status/auto-renew/expiry/WHOIS) is the reference shape. Not in scope now.

**Members (team / roles) — relevant to the future portal:** tabs **All members / Groups /
Settings**, **Invite members**, table columns **Name · Super admin · Groups · API access · 2FA ·
Status · Actions**. So team management = members + **role groups** + per-member API-access/2FA/
status. → Brisk's customer portal (Phase 3+) needs exactly this: members + role groups scoped to an
`account_id`. The DB already carries `account_id`; v1 admin stays single-user.

**Other Manage-account items (enumerated, all later phases):** Billing, **Account API tokens**
(scoped tokens + OAuth clients), **Audit logs** (every account action), **Notifications**,
Configurations. → Brisk v1 needs only minimal token/account settings; the rest is portal-era.

## ✅ Live Security / WAF capture (read-only, 2026-06-08) — Brisk Phase 4, not v1
Captured on request; documents the shape for Brisk's future Shield/WAF phase (NOT v1).

**Security → Overview:** "identify security action items + view posture." An on-demand
**Scan** (Scan now); a **Detection tools** panel listing protections with running status +
counts — **Web app exploits · DDoS attacks · Bot traffic · API abuse · Client-side abuse ·
Fraud protection**; a **Security action items** feed (recommendations with severity
Moderate/Low + tags *Security insight / Configuration suggestion / Bot traffic* + Review action);
a **Traffic overview** (Daily requests: Total, **Mitigated %**, Served by Cloudflare vs origin).

**Security → Security rules (the WAF):** two tabs — **Security rules** + **DDoS protection**.
Filters (rule type / search / status), **Create rule** + **Templates**, and three rule families:
- **Custom rules** (e.g. 0/5 used on Free) — your own WAF rules (matcher → action, same
  matcher→action→priority builder as Cache Rules).
- **Rate limiting rules** (e.g. 0/1 used) — request-rate thresholds → action.
- **Managed rules** (Cloudflare-maintained WAF rulesets; "Upgrade to Pro" to enable).
Sub-sections also seen: **Analytics**, **Web assets**, **Settings**, **Access** (Zero Trust).

→ **Brisk relevance:** all of this is **Phase 4 (Brisk Shield)** — reserve a "Security" nav slot
(disabled "soon" in v1), don't build. If/when built, the model is: managed rulesets + custom
rules + rate limiting + bot/DDoS, with a security overview (posture + mitigated %) — mirroring the
matcher→action pattern Brisk already uses for cache rules.

## What Brisk should take (functionally, not visually)
1. **Persistent top bar** with global search, dark-mode, account menu, quick-create.
2. **Time-range picker as a shared, reused control** across Overview + Analytics.
3. **Rules = matcher → action → priority → deploy**, ordered list (Brisk cache_rules).
4. **Purge = guided modal with precise modes** (url/prefix/zone/all now; tag/host later) + a job/status trail.
5. **Analytics layout:** KPI row → time-series (requests & bandwidth, cached vs uncached) →
   status-code breakdown → geo/top-N (geo later). Keep it clean, no chart junk, sampled-data honesty.
6. **Logs** as a live tail with a rich, filterable field set.
7. **Two-scope model** (account → zone) is the blueprint for Brisk's **future customer portal**;
   v1 admin keeps a single global sidebar. Keep components **account-scopable** so the portal slots in.
8. **Security/WAF, Members/roles, Billing** = explicitly later phases; reserve nav space, don't build.
