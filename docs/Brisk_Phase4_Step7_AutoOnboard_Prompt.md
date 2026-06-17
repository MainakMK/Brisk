# Brisk CDN — Phase 4 / Step 7: Fully-Automatic Zone Onboarding + Live-Zone Restore + Delete-Teardown Fix

**For Claude Code.** Context: `CLAUDE.md` + `docs/Brisk_Phase1_Build_Spec.md` + all Phase-2/3 + Phase-3.7 + Phase-4 prompts + the 3-in-1 (`Brisk_Dashboard_Delete_Cache_Prompt.md`) + the diagnostic report (below) + `docs/Control_Plane_Ops.md`. **State (v18):** control plane on the laptop; 3 edges `US-NY-prod01` (104.248.231.144), `EU-FRA-prod01` (188.245.225.172), `BLR1-01` (139.59.78.21). Multi-tenant routing = one nginx `server` block **per zone assigned to that edge** (Phase-4 Step 1); assignment via join table `server_zones(server_id, zone_id)`. DNS reconciler manages the geo-routed edge A-set under `cdn.a2zjav.com`. Wildcard TLS `*.a2zjav.com` via **lego v4 + Bunny DNS-01**, centrally managed, fanned to edges.

### Diagnostic findings this prompt acts on (confirmed, read-only)
1. **The live zone `cdn.a2zjav.com` (id 6) was hard-deleted** (05:05:37, dashboard, type-the-hostname guard worked). Its cache **was** purged (MISS on all 3 edges). **But its vhost never dropped** — deleting the *last* zone left each edge with a zero-zone config, and the agent's ≥1-zone empty-config guard rejected it and kept last-known-good. So **the live site is still serving, but only because the empty-config guard is holding** — and because the new zone (id 12) can't render (no cert) the config stays empty. The moment any edge successfully re-renders a config that doesn't include `cdn.a2zjav.com`, the live vhost drops → outage. **This must be stabilized first (Part 0).**
2. **`purge_jobs.zone_id REFERENCES zones(id) ON DELETE CASCADE`** (migration 00004) cascade-deletes the purge-tracking row when the zone is deleted → purge history lost (the NATS publish itself still fired). **Bug.**
3. **No auto-assign:** `createZone` (internal/api/zones.go) assigns nothing; the only path is manual `POST /servers/{id}/zones`. The new zone 12 is on BLR only (manually). **Missing feature.**
4. **No DNS for new-zone hostnames:** `testmainak.cdn.a2zjav.com` → NXDOMAIN. The reconciler only manages the `cdn.a2zjav.com` edge set. **Missing feature.**
5. **TLS depth gap:** `*.a2zjav.com` covers `cdn.a2zjav.com` but **not** `testmainak.cdn.a2zjav.com` (wildcards match exactly one label). `tls_mode=letsencrypt` issued no per-host cert. **Design gap.**

> **⚠️ LIVE SITE IS FRAGILE. Do PART 0 first and DO NOT PROCEED past its gate until `cdn.a2zjav.com` serves 200/HIT cleanly on all 3 edges from a *managed, re-renderable* config.** This is one prompt with **ordered parts (0 → 6); each has its own acceptance gate; verify the live site after every part.** Roll edge changes one box at a time (NY → FRA → BLR) with rollback ready. Test locally in Docker where possible before touching the fleet.

## Step 4.7 goal (one line)
Make zone onboarding **fully automatic** — creating a zone auto-assigns it to all online edges, auto-provisions its DNS, and auto-covers it with TLS so it goes live with **zero manual steps** — and **fix zone-delete teardown** so deleting a zone (even the last one) properly removes its vhost; **but first restore the accidentally-deleted live zone** so the fleet is on solid config.

---

## Locked design decisions (build to these — chosen for "fully automatic")
- **Hostname scheme stays `<tenant>.cdn.a2zjav.com`** (matches the live `cdn.a2zjav.com` brand + commercial-CDN convention).
- **DNS = one wildcard CNAME** `*.cdn.a2zjav.com → cdn.a2zjav.com`, managed by the reconciler. Because `cdn.a2zjav.com` is already a geo-routed smart A-set with health monitoring, **every tenant hostname inherits geo-routing + failover automatically, with zero per-zone DNS writes.** (Bunny confirmed: smart routing/health/weights live on the A-set; a CNAME to it inherits them, and the end-resolver still queries `cdn.a2zjav.com` so geo accuracy is preserved.) `cdn.a2zjav.com` itself is unaffected (the `*` wildcard does not match the bare `cdn` label).
- **TLS = one additional wildcard cert** `*.cdn.a2zjav.com`, issued + auto-renewed by the **existing lego v4 + Bunny DNS-01** machinery, fanned to edges alongside `*.a2zjav.com`. The agent picks the **covering** cert per `server_name` (`cdn.a2zjav.com` → `*.a2zjav.com`; `<tenant>.cdn.a2zjav.com` → `*.cdn.a2zjav.com`). Two wildcards cover all current + future zones — **no per-host cert churn.** (lego v5.2.2 exists but a major bump is out of scope; stay on v4.)
- **Assignment = auto-assign all online/in-rotation edges** on zone create (and auto-assign all existing zones to a newly-added edge), idempotent, drain-aware.
- **Delete teardown = explicit per-zone removal signal**, not "the list went empty," so the ≥1-zone guard stays intact while real teardown still works.
- **Defaults preserve current behavior;** `cdn.a2zjav.com` stays byte-for-byte the same throughout.

---

## PART 0 — STABILIZE: restore the live zone `cdn.a2zjav.com` (do this first; hard gate)
Goal: get the live site onto a **proper, re-renderable** zone record + assignment so it's no longer surviving on a stuck empty-config guard. Use **existing, proven endpoints only** here (no new code yet) to minimize risk.

1. **Capture the exact live config that's currently serving.** It's still in each edge's last-known-good nginx config. On one edge, read the live `cdn.a2zjav.com` `server` block (`nginx -T`) and the zone's settings the agent last received, to reproduce them faithfully: origin `https://test.mainakghosh.com` (Cloudflare-proxied → `resolver` + variable `proxy_pass` + correct upstream Host/SNI), `user www-data`, WP caching rules, SWR + cache-lock, HSTS, Brotli, branded headers, video off, TLS `*.a2zjav.com`.
2. **Quarantine the half-broken new zone first** so it can't poison renders: temporarily unassign zone 12 from BLR (`server_zones` row (3,12)) **or** set zone 12 `status` to a non-live/draft state. (It'll be re-onboarded automatically in Part 6.) This makes BLR's config render cleanly with just `cdn.a2zjav.com`.
3. **Recreate the zone** `cdn.a2zjav.com` (same origin + settings as captured) via the normal create flow, then **manually assign it to all 3 edges** (`POST /servers/{id}/zones` for NY, FRA, BLR). Bump `config_version`.
4. **Verify per edge (one at a time):** the edge pulls the new config → `nginx -t` **passes** → reload succeeds → `cdn.a2zjav.com` serves **200**, `Server: Brisk`, valid TLS (`*.a2zjav.com`), and a warm request is **HIT**. Confirm the vhost is now driven by the **managed** config (not stuck last-known-good): a trivial `config_version` bump should re-render cleanly.
5. **Confirm the live site is solid** before moving on.

### 🔒 GATE 0 (must pass before Part 1)
```bash
for ip in 104.248.231.144 188.245.225.172 139.59.78.21; do
  echo "== $ip =="; curl -ksI --resolve cdn.a2zjav.com:443:$ip https://cdn.a2zjav.com/ | egrep -i 'HTTP/|server:|x-brisk-cache|x-brisk-edge'
done
# Expect on all 3: HTTP 200, Server: Brisk, X-Brisk-Cache: HIT (after warm), TLS valid
# AND: cdn.a2zjav.com exists as a zone row, assigned to all 3 edges, config_version managed (a bump re-renders cleanly, nginx -t passes)
```
**Do not proceed until GATE 0 is green on all three edges.**

---

## PART 1 — Auto-assign zones to all online edges (on create + on new-edge)
Make assignment automatic; manual assign stays as an override.

1. **On zone create** (`createZone`, internal/api/zones.go): after `store.CreateZone`, **auto-INSERT `server_zones` for every online + in-rotation edge** (skip drained/offline; respect the rotation state from Phase-3 Step-5). Idempotent (no dup rows). Bump the zone's `config_version` so edges pull. Wrap create+assign in one transaction.
   ```go
   // after creating the zone:
   edges := store.ListOnlineInRotationServers(ctx)      // online && !drained && healthy
   for _, s := range edges { store.AssignZone(ctx, s.ID, zone.ID) } // idempotent upsert
   store.BumpZoneConfigVersion(ctx, zone.ID)
   ```
2. **On new edge added** (the add-server flow): **auto-assign all existing active zones** to the new edge (so a freshly added PoP serves the whole catalog without manual wiring). Idempotent; bump affected zones' `config_version`. This pairs with the Phase-3 auto-DNS-registration of the new edge.
3. **On drain/undrain/delete of an edge:** keep `server_zones` consistent (delete cascades already remove rows on edge delete; drain shouldn't delete assignments — drain is DNS-level, the vhost can stay). Document the intended behavior.
4. **Override still available:** the manual `POST /servers/{id}/zones` and an unassign endpoint remain for fine control. Add an optional `assign_all` (default true) to the create API so a future customer-portal flow could opt out.

**Gate 1:** create a throwaway zone via API → it appears in `server_zones` for **all 3** online edges automatically (no manual step) → each edge's pull-config payload now includes it → `config_version` bumped.

---

## PART 2 — Auto-DNS for tenant hostnames (wildcard CNAME)
1. **Reconciler ensures the wildcard CNAME exists:** `*.cdn.a2zjav.com → cdn.a2zjav.com`, created/maintained in Bunny via the existing `internal/dns` client, **tagged** with a recognizable `brisk:` comment (e.g. `brisk:wildcard:cdn`). Create-if-missing, self-healing, idempotent. This single record makes **every** `<tenant>.cdn.a2zjav.com` resolve to the geo-routed edge set — no per-zone DNS writes.
2. **Safety/guardrails (carry from Phase-3 Step-1):** never clobber non-Brisk records; only manage the `brisk:`-tagged wildcard; respect Bunny rate limits (backoff on 429, retry 5xx); don't touch `cdn.a2zjav.com` or the edge A-records.
3. **Document the alternative** (don't build unless needed): per-zone CNAME `<tenant>.cdn.a2zjav.com → cdn.a2zjav.com` created on assign / removed on delete — more granular (lets you pull one tenant's DNS) but more writes. The wildcard is the default.
4. **Note:** an explicit per-name record always overrides the wildcard in DNS, so future custom per-tenant routing still works.

**Gate 2:** `dig +short testmainak.cdn.a2zjav.com @1.1.1.1` returns the CNAME → the geo-routed edge IP(s). A brand-new random tenant hostname resolves immediately with no extra DNS action.

---

## PART 3 — Auto-TLS: issue + manage the `*.cdn.a2zjav.com` wildcard
Extend the **existing** lego v4 + Bunny DNS-01 machinery (Phase-3.7 Step-2) to manage a **second** wildcard alongside `*.a2zjav.com`.

1. **Issue `*.cdn.a2zjav.com`** (+ optionally the SAN `cdn.a2zjav.com` is already covered by `*.a2zjav.com`, so the new cert is just `*.cdn.a2zjav.com`). DNS-01 TXT lands at `_acme-challenge.cdn.a2zjav.com` via the Bunny provider (same key, never logged). **Additive then verify:** obtain the new cert, distribute it, and only rely on it for tenant vhosts — `cdn.a2zjav.com` keeps using `*.a2zjav.com`. On issuance failure, tenant zones simply stay un-served (don't touch the live cert); alarm + retry/backoff.
2. **Fan to edges** over the existing config-pull cert channel; store at `/etc/brisk/tls/_wildcard.cdn.a2zjav.com/` (or your cert-dir convention).
3. **Agent picks the covering cert per `server_name`:** in `server.tmpl`, select `ssl_certificate` by longest-suffix match — `cdn.a2zjav.com` → `*.a2zjav.com`; `*.cdn.a2zjav.com` hosts → `*.cdn.a2zjav.com`. Make this a small helper so adding per-host SNI certs later (Phase-4 Step-2 custom domains) is trivial.
4. **Auto-renew both wildcards** (ARI / ~30 days remaining, zero-downtime `reload`, keep old cert on failure) — extend the existing renewal loop to cover the new cert; surface issuer/expiry/last-renewed for **both** in the dashboard.
5. **`tls_mode` semantics:** for Brisk-subdomain zones, `tls_mode=letsencrypt` now means "covered by the managed `*.cdn.a2zjav.com` wildcard" (no per-host issuance). Document this; per-host issuance remains the custom-domain path (Step 2, not built here).

**Gate 3:** `echo | openssl s_client -connect 104.248.231.144:443 -servername testmainak.cdn.a2zjav.com | openssl x509 -noout -subject -ext subjectAltName` → presents `*.cdn.a2zjav.com`, **verify return code 0** (name matches). Renewal dry-run succeeds for both wildcards; on simulated failure the existing cert is kept.

---

## PART 4 — Fix zone-delete teardown (two coupled bugs)
1. **Stop purge-tracking cascade loss.** Migration: change `purge_jobs.zone_id` FK from `ON DELETE CASCADE` to **`ON DELETE SET NULL`** and **store the zone hostname (text) on the purge_jobs row** at creation, so the purge record + its hostname survive the zone deletion. (Alt: create the purge job with `zone_id = NULL` like purge-all and carry the hostname.) The NATS publish already fires; this just preserves history/observability.
2. **Make the vhost drop on last-zone delete without weakening the empty-config guard.** Add an **explicit zone-removal path**: on `DELETE /zones/{id}`, the control plane signals the agent to **remove that specific zone's vhost** (e.g. include `removed_zone_ids` in the pull-config delta, or emit a `zone.removed` event on the existing NATS channel) **and** bump `config_version`. The agent removes the named vhost from its current set and re-renders the remainder.
   - **Invariant (keep the guard's protection):** the ≥1-zone guard continues to **reject empty/garbage/failed fetches** (the NXDOMAIN-outage protection). What changes: an **authenticated, version-bumped config that has explicitly removed its last zone** is allowed to render to **`default_server` only** (no tenant vhosts) — because teardown is driven by *explicit removal*, not by *"the list happens to be empty."* Do **not** simply allow any empty config through.
   - After teardown, the deleted hostname → `default_server` clean 404/444 (no stale HITs, no 200), verified per edge.
3. **Durability:** removal replays on a reconnecting edge (JetStream / next pull) — a deleted zone never resumes serving.
4. **Keep the type-the-hostname guard** for in-rotation/live zones, and **add a louder warning when deleting the edge's last/only zone or a production zone** (this is exactly how `cdn.a2zjav.com` got nuked). Audit who/when as today.

**Gate 4:** create a throwaway zone (now auto-assigned + auto-DNS + auto-TLS, serving 200 on all 3) → delete it → within ~20–30s **all 3 edges**: vhost gone (hostname → default_server/404, no stale HIT) **and** cache purged **and** a `purge_jobs` row survives with the hostname. Repeat where it's the edge's **last** zone → vhost still drops, but a *failed/empty fetch* is still rejected (guard intact). `cdn.a2zjav.com` unaffected throughout.

---

## PART 5 — Dashboard surfacing (make the automation visible)
- **Zone create UI:** state that the zone will be **auto-assigned to all online edges** + **auto-DNS + auto-TLS**; after create, show the assigned edges and a propagation hint (~15s pull + DNS TTL). Show the zone's CDN hostname prominently with copy-to-clipboard.
- **Zone detail:** assigned edges list (auto vs manual), DNS status (covered by wildcard), TLS status (covered by `*.cdn.a2zjav.com`, with the wildcard's issuer/expiry).
- **DNS/TLS area:** show **both** managed wildcards (`*.a2zjav.com`, `*.cdn.a2zjav.com`) — issuer, expiry, last-renewed — and the managed wildcard CNAME.
- **Delete dialog:** keep type-the-hostname; add the **last-zone / production** warning from Part 4.
- Voltage palette, dark-first; skeleton/empty/error; honest propagation language.

**Gate 5:** the running dashboard (rebuilt + redeployed, new build hash, verified in-browser) shows auto-assign + DNS + TLS state for zones and both wildcards; delete dialog shows the new warning.

---

## PART 6 — End-to-end automatic verification (re-onboard zone 12 + a fresh zone)
1. **Re-onboard zone 12** (`testmainak.cdn.a2zjav.com`): un-quarantine it (re-enable / re-create through the normal flow). With Parts 1–3 live it should **auto-assign to all 3 edges → auto-resolve (wildcard CNAME) → auto-TLS (`*.cdn.a2zjav.com`) → render a vhost on every edge → serve 200 with valid TLS**, all with no manual steps. (Origin is `https://test.mainakghosh.com`, same Cloudflare-proxied origin as the live zone — proxied-origin handling applies.)
2. **Fresh-zone smoke test:** create a brand-new throwaway zone via the dashboard → confirm it goes fully live automatically (assign + DNS + TLS + 200) within the pull interval + DNS TTL → then delete it → confirm clean teardown (Part 4). This proves the user can now "onboard my own sites as zones" with zero manual wiring.
3. **Live-site regression check:** `cdn.a2zjav.com` 200/HIT/`Server: Brisk`/valid TLS on all 3 edges, byte-for-byte unchanged.

---

## Acceptance tests (Step 4.7 definition of done)
```bash
# GATE 0 — live zone restored + solid (see Part 0). MUST pass before anything else.

# AUTO-ASSIGN
# A1) POST a new zone -> server_zones has rows for ALL 3 online edges automatically; config_version bumped
# A2) Add a (test) edge -> all existing active zones auto-assigned to it

# AUTO-DNS
# A3) dig +short <new>.cdn.a2zjav.com @1.1.1.1 -> CNAME -> geo-routed edge IP(s); brand-new hostname resolves with no extra action
# A4) cdn.a2zjav.com + edge A-set untouched; only the brisk:-tagged wildcard CNAME is managed

# AUTO-TLS
# A5) openssl s_client -servername <new>.cdn.a2zjav.com -> *.cdn.a2zjav.com cert, verify code 0
# A6) renew dry-run OK for BOTH wildcards; on simulated failure old cert kept; issuer/expiry shown in dashboard

# SERVING
# A7) curl -ks https://<new>.cdn.a2zjav.com/ -> 200, Server: Brisk, correct origin content, HIT after warm, on all 3 edges
# A8) cache isolation: identical paths on two tenants cache under different $host keys; cross-tenant purge doesn't bleed

# DELETE TEARDOWN
# A9) Delete a test zone -> within ~20-30s all 3 edges: vhost gone (default_server/404, no stale HIT) + cache purged + purge_jobs row survives with hostname
# A10) Last-zone delete still tears the vhost down; an empty/failed fetch is still rejected by the guard (no accidental wipe)
# A11) type-the-hostname + last-zone/production warning on delete

# LIVE SITE
# A12) cdn.a2zjav.com -> 200/HIT/Server: Brisk/valid TLS on all 3, byte-for-byte unchanged throughout
# A13) dashboard rebuilt + redeployed (new hash, verified in browser); build/vet/tsc clean
```
**Done when:** the live zone is restored onto managed config (GATE 0), and **creating a zone makes it go live automatically — assigned to all edges, DNS-resolvable, TLS-covered, serving 200 — with zero manual steps**, deleting a zone (even the last one) cleanly tears down its vhost + purges its cache + preserves the purge record without weakening the empty-config guard, both managed wildcards auto-renew, the dashboard surfaces it all, and `cdn.a2zjav.com` is untouched throughout.

---

## Pitfalls (do not skip)
1. **PART 0 IS NON-NEGOTIABLE FIRST.** The live site is surviving on a stuck empty-config guard; restore `cdn.a2zjav.com` onto managed, re-renderable config before any change that could trigger a render. Quarantine the half-broken zone 12 first so it can't fail renders.
2. **Don't weaken the empty-config guard.** Teardown must be driven by **explicit per-zone removal**, not by accepting empty lists. The guard still rejects failed/garbage fetches (the NXDOMAIN-outage lesson).
3. **`purge_jobs` FK fix is a migration** — `ON DELETE SET NULL` + store hostname; verify the cascade no longer eats the row.
4. **Two wildcards, pick the covering cert per server_name** — `cdn.a2zjav.com` keeps `*.a2zjav.com`; tenants use `*.cdn.a2zjav.com`. The `*` wildcard CNAME/cert must NOT shadow the bare `cdn` label.
5. **TLS migration is additive-then-verify** — issue `*.cdn.a2zjav.com` alongside the working `*.a2zjav.com`; never drop the live cert; keep old cert on renewal failure.
6. **Bunny safety** — only manage `brisk:`-tagged records; never clobber the live edge A-set or `cdn.a2zjav.com`; respect rate limits; key never logged/committed.
7. **One edge at a time, rollback ready** — keep `nginx.conf.brisk-bak` + working certs per edge; verify each before the next; `cdn.a2zjav.com` never drops.
8. **Auto-assign respects rotation** — skip drained/offline edges; idempotent; drain ≠ unassign.
9. **Carry forward ALL per-zone behavior** — Cloudflare-proxied origin handling, `user www-data`, WP caching, SWR+lock, HSTS, branded headers, video/Brotli — applied to each generated vhost.
10. **Stay on lego v4** — v5.2.2 exists but a major bump is a separate, scoped task; don't pull it in here.

## Next — Step 4.8 (do NOT start) — custom-domain CNAMEs + per-domain SNI certs (the paying-customer gateway)
Let a customer point **their own** domain at a Brisk CNAME, verify ownership, then **auto-issue a per-domain cert** (extend this lego machinery from wildcards to per-host SNI) and serve it. The covering-cert helper + additive-cert flow built here are the hooks. Wait for the user's go-ahead and a Step 4.8 prompt.
