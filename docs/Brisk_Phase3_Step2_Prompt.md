# Brisk CDN — Phase 3 / Step 2 Build Prompt (Auto‑Register Server IP → DNS)

**For Claude Code.** Context in the repo: `CLAUDE.md` + `docs/Brisk_Phase1_Build_Spec.md` + all Phase‑2 prompts + `docs/Brisk_Phase3_Step1_Prompt.md` + `dashboard-reference/`. **Phases 1 & 2 + Phase 3 Step 1 are complete:** a live edge serves `brisk.mainakghosh.com`; the admin dashboard manages servers/zones/rules/analytics/purge; and `brisk-control` now has a **Bunny DNS client** (`internal/dns`) with authenticated CRUD over A records (Smart‑Record + monitor fields modeled), `brisk:` comment tagging, rate‑limit/retry handling, and a **deletion‑protection lock** (records can't be deleted without a time‑delayed unlock). Verified against test zone `a2zjav.com` (zone_id 807319); the live domain was never touched.

> **Read `CLAUDE.md`, `docs/Brisk_Phase3_Step1_Prompt.md`, and the Phase‑2 prompts first.** This is **Step 2 of 6 in Phase 3**. Build **auto‑registration of a server's IP into DNS, driven by the server lifecycle** — no geo‑routing logic (Step 3), no health‑based failover (Step 4), no routing UI beyond status (Step 5). Pass the acceptance tests, stop before Step 3.

## Step 3.2 goal (one line)
Make DNS **follow the `servers` table automatically**: when a server is online it has an enabled A record in its region's set; when it's off/drained the record is **kept but disabled** (out of rotation); when the server is **deleted** the record is **removed automatically** — all idempotent, reconciled, and respecting the lock for *ad‑hoc* deletes only.

## ✅ Test locally + against the test zone
`brisk-control` runs locally in Docker against the **test zone `a2zjav.com`** (not the live domain). The user supplies the Bunny API key + test zone (already configured from Step 1). Verify with `curl` + `dig`.

---

## The lifecycle → DNS mapping (this is the whole step — build exactly this)
Decided with the user. The **`servers` table is the source of truth**; DNS converges to match it. Only `brisk:`‑tagged records are ever touched.

| Server state | DNS action | Why |
|---|---|---|
| **online** (heartbeating) | **Add or enable** its A record in the region's set (`Disabled=false`) | edge joins the routing pool |
| **off / offline / disabled / drained** (maintenance, or heartbeat stale) | **Keep the record, set `Disabled=true`** (out of rotation) — do NOT delete | the server may come back; restoring it = flip `Disabled=false`, no re‑create, no propagation churn |
| **deleted** (server removed from Brisk) | **Remove the DNS record automatically** | the server is gone for good; its routing record should go too |

**Key user decision baked in:**
- **Off ≠ delete.** A turned‑off/drained server keeps its DNS record (just disabled). Don't remove records for transient downtime.
- **Delete = auto‑remove.** Deleting the *server* in the dashboard is an explicit, authorized action, so it removes the DNS record as part of that single operation — **this server‑lifecycle delete bypasses the time‑delay lock** (the user already authorized it by deleting the server).
- **The lock still guards ad‑hoc DNS deletes.** Deleting a record directly via the DNS section (not via server deletion) still requires the time‑delayed unlock from Step 1. The lock exists to prevent *accidental/manual* changes, not to block intentional server‑lifecycle changes.

## Part 1 — Short TTL on routing records (set now for Steps 3–4)
Create/enable these A records with a **short TTL (default 60s; configurable 30–120s)**. Why now: DNS failover speed ≈ detection time + TTL, so the routing records need a low TTL from the start for Steps 3–4 to fail over fast. Don't go below ~30s (raises query volume/cost without real benefit). Make it a config value (`BRISK_DNS_TTL`, default 60).

## Part 2 — The reconciler (`internal/dns` + a sync service)
A **reconcile function** that takes the current `servers` (with `region`, `ip`, `status`, `edge_id`) and converges the zone's `brisk:`‑tagged records to match the table:
- For each **online** server → ensure an A record exists with `Name` = the region's routing name (see Part 3), `Value` = server IP, `Ttl` = short, `Disabled=false`, `Comment` = `brisk:server:<edge_id>`. Create if missing, enable if disabled, update if IP changed.
- For each **off/drained** server → ensure its record exists but `Disabled=true`.
- For each record tagged `brisk:server:<edge_id>` whose server **no longer exists** → that's an orphan from a deleted server → remove it (lifecycle delete path).
- **Idempotent:** running reconcile repeatedly makes no spurious changes (match on the `brisk:server:<edge_id>` comment + name+value).
- **Never touch non‑Brisk records.** Only records with the `brisk:` tag are in scope.
- **Dry‑run mode** that returns the diff (planned adds/enables/disables/removes) without applying — useful for the UI/log and safety.

**When reconcile runs:**
- On the relevant **lifecycle events**: server comes online (heartbeat flips `status`→online), server disabled/drained, server deleted. Trigger a targeted reconcile for that server (and/or a full reconcile).
- Plus a **periodic safety reconcile** (e.g. every few minutes) to catch drift (a record manually changed, a missed event). Keep it gentle (respect rate limits; only act on real diffs).

## Part 3 — Region → routing record name
- Each server has a `region` (e.g. `IN-DEL`, `US-IL`). For Step 2, all of a region's edges go into a **record set under one routing name** (e.g. the CDN hostname `cdn.<zone>` or a per‑region name). Keep it simple: **one shared routing name** (the CDN hostname) holds the **set** of all online edge IPs across regions — this becomes the geo record set in Step 3. (Step 3 sets the per‑record Smart‑Record geo/latency type; Step 2 just gets the right IPs into the set, enabled/disabled correctly.)
- Store the mapping clearly (which record id corresponds to which `edge_id`) — rely on the `brisk:server:<edge_id>` comment as the durable link, not just the record id.

## Part 4 — Wire into the server lifecycle (hook existing flows)
- **Add‑server / online:** when an added server's first heartbeat flips it to `online` (Phase‑2 Step‑2 behavior), call reconcile → its IP enters the set enabled.
- **Disable / drain:** a `status` change to disabled/drained → reconcile → record set `Disabled=true` (kept).
- **Delete server:** `DELETE /api/v1/servers/{id}` → also remove its `brisk:server:<edge_id>` DNS record (lifecycle delete, lock‑bypassed for this authorized path). Confirm in the API/UI that deleting a server also pulls it from DNS.
- Record DNS actions in a small **audit/log** (which record added/enabled/disabled/removed, when, by which event) so the dashboard (Step 5) and humans can see what happened. Reuse a `dns_*` log table or the existing pattern.

## Part 5 — Endpoints (thin; full routing UI is Step 5)
```
POST /api/v1/dns/reconcile            # run reconcile now (optional ?dry_run=true -> returns the diff)
GET  /api/v1/dns/reconcile/preview    # dry-run diff: planned adds/enables/disables/removes
GET  /api/v1/dns/records              # (from Step 1) now shows brisk:server:<edge_id> records with enabled/disabled state
```
The existing server endpoints gain the DNS side effects above. Keep new surface minimal.

---

## Acceptance tests (Step 3.2 definition of done — test zone + dig)
```bash
docker compose up --build -d        # brisk-control + test zone configured
# 1) Online server -> enabled A record in the set
#    (add a server / bring one online) -> GET /dns/records shows brisk:server:<edge_id>, Disabled=false, TTL 60
dig +short cdn.a2zjav.com           # includes that server's IP
# 2) Drain/disable the server -> record kept but disabled
#    set status disabled -> reconcile -> record still present, Disabled=true
dig +short cdn.a2zjav.com           # that IP no longer returned (out of rotation), but record still exists in Bunny
# 3) Bring it back online -> record re-enabled (no re-create)
dig +short cdn.a2zjav.com           # IP returns
# 4) Delete the server -> record auto-removed (lifecycle delete, lock bypassed for this path)
curl -s -X DELETE localhost:8080/api/v1/servers/<id>
curl -s localhost:8080/api/v1/dns/records       # the brisk:server:<edge_id> record is gone
# 5) Ad-hoc DNS delete still locked: deleting a record via the DNS section requires the time-delayed unlock (Step 1) -> 423 without unlock
# 6) Idempotency: POST /dns/reconcile twice -> second run makes no changes (empty diff)
# 7) Safety: a non-Brisk record in the zone is never modified/removed by reconcile or by server delete
# 8) Drift: manually disable a brisk record in Bunny for an online server -> periodic reconcile re-enables it
# 9) Multiple servers: 2 online edges -> both IPs in the set; drain one -> only the other returned by dig
```
**Done when:** DNS automatically tracks the `servers` table — **online = enabled record, off/drained = disabled‑but‑kept, deleted = auto‑removed** — with short‑TTL records, an idempotent reconciler, drift correction, a dry‑run preview, lock‑bypass only for authorized server‑lifecycle deletes (ad‑hoc DNS deletes still locked), and zero impact on non‑Brisk records — all verified against the test zone via `dig`.

---

## Pitfalls (do not skip)
1. **Off ≠ delete** — transient downtime/drain keeps the record (`Disabled=true`); only a *server delete* removes it. Don't churn records on every heartbeat blip.
2. **`servers` table is the source of truth** — DNS converges to it; never the reverse.
3. **Only `brisk:`‑tagged records** — reconcile/delete must never touch manual records; match on `brisk:server:<edge_id>`.
4. **Lock semantics** — server‑lifecycle delete bypasses the lock (authorized); ad‑hoc DNS record delete still requires the time‑delayed unlock. Don't blanket‑bypass the lock.
5. **Short TTL now** (60s default) so Steps 3–4 fail over fast; don't leave it at a long default; don't go below ~30s.
6. **Idempotent + drift‑safe** — repeated reconcile = no spurious changes; periodic reconcile fixes manual drift gently (respect rate limits, only on real diffs).
7. **Debounce flapping** — a server flapping online/offline shouldn't hammer the Bunny API; debounce/coalesce reconciles and back off on 429.
8. **Don't implement routing/failover here** — Step 2 only gets the right IPs enabled/disabled in the set. Geo Smart‑Record type = Step 3; health‑based auto‑failover = Step 4.
9. **Heartbeat‑stale handling** — decide a clear rule (e.g. stale > N seconds → treat as offline → disable record), consistent with the dashboard's offline derivation.
10. **Test zone only** — live `mainakghosh.com` cutover is a deliberate later step.

## Important context for later steps (so expectations are set)
- **Failover speed is TTL‑bound, not instant.** When a PoP dies, detection (Bunny monitors ~every 30s) + the record TTL governs how fast traffic moves — roughly **1–2 minutes** with a 60s TTL. This is normal for DNS‑based CDNs.
- **In‑flight viewers rely on the player, not DNS.** A user mid‑video whose PoP dies keeps the cached IP until their resolver's TTL expires; the **HLS player's segment retry/re‑resolution** is what recovers them (re‑requests → re‑resolves → healthy PoP). Truly seamless instant reroute needs **Anycast** (own IP space + BGP) — a far‑future/Phase‑4+ item, not now. **Step 4** will document this and recommend short TTL + a retry‑capable player.

## Next — Step 3.3 (do NOT start)
**GeoDNS routing:** set the **Smart‑Record type (geographic or latency)** on the records so a user is returned the nearest healthy edge, with region mapping driven by `servers.region`. Wait for the user's go‑ahead and a Step 3.3 prompt.
