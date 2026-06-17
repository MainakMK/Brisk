# Brisk CDN — Phase 3 / Step 1 Build Prompt (Bunny DNS Integration — Foundation)

**For Claude Code.** Context in the repo: `CLAUDE.md` + `docs/Brisk_Phase1_Build_Spec.md` + all Phase‑2 prompts + `dashboard-reference/`. **Phases 1 & 2 are complete:** a live edge serves `brisk.mainakghosh.com`; `brisk-control` (Go + chi + pgx + TimescaleDB + NATS) runs the API; the admin dashboard manages servers, zones, cache rules, analytics, and instant purge. The `servers` table already has a **`region`** field and the Add‑Server flow collects it. **Phase 3 makes Brisk multi‑PoP with smart DNS routing.**

> **Read `CLAUDE.md` and `docs/Brisk_Phase1_Build_Spec.md` first.** This is **Step 1 of 6 in Phase 3** — the **foundation**: bind `brisk-control` to the **Bunny DNS API** so it can authenticate, read the DNS zone, and create/update/delete records. **No auto‑registration, routing logic, or UI yet** (those are Steps 2–5). Prove the API binding works, then stop before Step 2.

## Step 3.1 goal (one line)
Add a **Bunny DNS client** to `brisk-control`: store the API key + zone config, and implement **authenticated CRUD** over DNS records (list / add / update / delete A records, including the Smart‑Record + monitoring fields), verified against a real Bunny DNS zone via `curl`/tests.

## ✅ Test locally + against real Bunny DNS
`brisk-control` runs locally in Docker. The Bunny DNS API is a real external service — the user will supply a **Bunny API key** and a **DNS zone** (a test subdomain is ideal, e.g. a throwaway zone, so we don't touch the live `mainakghosh.com` records yet). Ask the user for the API key + zone at runtime; **never hardcode or commit them.**

---

## How Bunny DNS works (the model — build to this)
- **API:** the Bunny **Core API** at `https://api.bunny.net`, authed with an **`AccessKey: <api-key>`** header (account API key, from the Bunny dashboard → Account). JSON over HTTPS.
- **Zones & records:** records live under a **DNS zone** identified by a numeric **`zoneId`**. Records are managed under `/dnszone/{zoneId}/records` (add = `PUT`, update = `POST .../records/{id}`, delete = `DELETE .../records/{id}`); the zone itself is read via `GET /dnszone/{zoneId}` (which returns its records). List zones via `GET /dnszone`.
- **Record fields** (A record): `Type` (0 = A, 1 = AAAA, 2 = CNAME, …), `Name` (subdomain, "" for apex), `Value` (the IP), `Ttl`, `Weight`, `Disabled` (bool), `Comment`, plus Smart‑Record fields **`SmartRoutingType`** and a geo/latency zone, and monitor fields **`MonitorType`/`MonitorStatus`**.
- **Record sets = the CDN feature:** multiple A records sharing the **same Name** form a set that supports **weighting**, **smart routing** (geographic or latency), and **health monitoring that auto‑removes unhealthy endpoints from responses**. This is what later steps use for nearest‑PoP routing + failover — but **Step 1 only needs solid CRUD**; we wire routing/monitoring semantics in Steps 3–4.
- **Go option:** the maintained **`github.com/libdns/bunny`** library wraps this API (auth via `AccessKey`, `GetRecords`/`AppendRecords`/`SetRecords`/`DeleteRecords`). You may use it **or** a small hand‑rolled client — choose based on whether libdns exposes the Smart‑Record/monitor fields we'll need in Steps 3–4 (if it abstracts them away, a thin direct client over `api.bunny.net` is cleaner for our needs). Document the choice.

## Part 1 — Config + secrets
Add to `brisk-control` config (env): `BUNNY_API_KEY`, `BUNNY_DNS_ZONE_ID` (or `BUNNY_DNS_ZONE` name to resolve to an id), and a default `BRISK_CDN_HOSTNAME`/record name for the CDN (e.g. `cdn` or the apex you'll route). Document in `.env.example`; **never commit real values**; keep the key out of logs (log only that DNS is configured, not the key).

## Part 2 — Bunny DNS client (`internal/dns/`)
A typed Go client with:
- `ListZones() / GetZone(zoneId)` — confirm connectivity + resolve a zone name → id.
- `ListRecords(zoneId)` — return existing records (typed structs).
- `AddRecord(zoneId, rec)` — create an A record (Name, Value=IP, Ttl, Weight, optional Smart‑Record/monitor fields, Comment).
- `UpdateRecord(zoneId, id, rec)` — update (e.g. toggle `Disabled`, change weight).
- `DeleteRecord(zoneId, id)`.
- Sensible **timeouts, retries with backoff on 5xx, and 429 handling** (the Bunny API is rate‑limited; respect `Retry‑After`/back off — don't hammer it). Typed errors. A `Ping()`/health method the control plane can call at startup to verify the key+zone.
- Tag Brisk‑managed records with a recognizable **`Comment`** (e.g. `brisk:server:<edge_id>`) so we can later tell ours apart from manual records and reconcile safely.

## Part 3 — Thin API surface (for testing now; UI is Step 5)
Behind the existing admin routing (open locally), expose minimal endpoints to exercise the client:
```
GET    /api/v1/dns/status                 # {configured: true, zone: "...", record_count: N} — verifies key+zone
GET    /api/v1/dns/records                # list records in the zone (typed)
POST   /api/v1/dns/records                # add an A record {name, value(ip), ttl, weight?, comment?}
DELETE /api/v1/dns/records/{id}           # delete
```
These are **plumbing to validate the binding** — Step 2 wires real auto‑registration to the add‑server flow, so keep this thin and clearly internal.

## Part 4 — Safety / reconciliation guardrails (design in now)
- **Don't clobber non‑Brisk records.** Only manage records Brisk created (matched by the `brisk:` comment tag / a known name pattern). Never bulk‑overwrite the whole zone.
- **Idempotency:** adding a record that already exists (same name+value) should detect + no‑op or update, not duplicate.
- **Dry‑run / preview** flag on destructive ops where feasible, so Step 2's automation can preview changes.
- **Use a test zone** for this step; the live `mainakghosh.com` records aren't touched until we deliberately cut over in a later step.

---

## Acceptance tests (Step 3.1 definition of done)
```bash
docker compose up --build -d        # brisk-control with BUNNY_API_KEY + zone configured
# 1) Connectivity: key + zone valid
curl -s localhost:8080/api/v1/dns/status        # {"configured":true,"zone":"<zone>","record_count":N}
# 2) List existing records
curl -s localhost:8080/api/v1/dns/records        # typed array from the real Bunny zone
# 3) Add a test A record
curl -s -X POST localhost:8080/api/v1/dns/records -H 'Content-Type: application/json' \
  -d '{"name":"brisk-test","value":"203.0.113.10","ttl":60,"comment":"brisk:test"}'
#    -> appears in the Bunny dashboard + in GET /dns/records
# 4) Verify via real DNS resolution (after propagation)
dig +short brisk-test.<your-zone>                # 203.0.113.10
# 5) Delete it
curl -s -X DELETE localhost:8080/api/v1/dns/records/<id>     # gone from Bunny + dig
# 6) Idempotency: re-adding the same record no-ops/updates (no duplicate)
# 7) Safety: non-Brisk records in the zone are listed but never modified/deleted by Brisk
# 8) Auth failure handled: a bad API key -> /dns/status reports not-configured/clear error (no crash, no key in logs)
```
**Done when:** `brisk-control` authenticates to Bunny DNS, lists the zone's records, and **adds/updates/deletes A records** (verified in the Bunny dashboard and via `dig`), with rate‑limit/retry handling, idempotency, the `brisk:` comment tagging, and guardrails that never touch non‑Brisk records — all against a **test zone**, with the API key kept out of logs and the repo.

---

## Pitfalls (do not skip)
1. **Secrets discipline** — `BUNNY_API_KEY` from env only; never logged, never committed; `.env` git‑ignored.
2. **Use a test zone** — don't touch live `mainakghosh.com` records in this step; cutover is deliberate and later.
3. **Respect rate limits** — the Bunny API is rate‑limited; back off on 429, retry 5xx with jitter, don't poll aggressively.
4. **Only manage Brisk's records** — tag with `brisk:` comments; never bulk‑overwrite or delete records Brisk didn't create.
5. **Idempotent writes** — re‑adding the same record must not create duplicates.
6. **A‑record `Type` is 0** in Bunny's numeric enum (AAAA=1, CNAME=2) — map carefully; don't assume string types.
7. **Smart‑Record/monitor fields exist but aren't wired yet** — capture them in the struct so Steps 3–4 can set them; don't implement routing/failover logic here.
8. **Verify with `dig`, not just the API** — confirm records actually resolve, accounting for TTL/propagation.
9. **Scope** — DNS client + thin test endpoints only. No auto‑registration (Step 2), routing (Step 3), monitoring (Step 4), or UI (Step 5).

## Next — Step 3.2 (do NOT start)
**Auto‑register server IP → DNS:** hook the add‑server/online flow so a new edge's IP is automatically added as a Smart A record in its region's set (and removed on disable/delete), tagged and reconciled via this client. Wait for the user's go‑ahead and a Step 3.2 prompt.
