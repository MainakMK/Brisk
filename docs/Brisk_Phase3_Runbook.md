# Brisk CDN — Multi-PoP Runbook & Phase 3 Close-out

Operational guide for running Brisk as a multi-PoP CDN: add a region, drain for
maintenance, read the rotation/health badges, fail over, and **cut the live domain
over to the multi-PoP set with a one-step rollback**.

> Status: control plane + dashboard + full DNS routing stack are built and
> validated against test zone `a2zjav.com`. The live domain (`mainakghosh.com`)
> has **not** been cut over yet — that is the gated production action in
> Phase 3 Step 6, Part 5 (see "Live cutover" below). It requires real regional
> edges (user-provisioned) and an explicit go-ahead at cutover time.

---

## 1. Architecture recap (what's running)

- **Edges (data plane):** bare-metal/VPS, Nginx cache + Go `brisk-agent`. Each
  edge serves the Phase-1 stack (HLS slicing, branded headers, Brotli, TLS,
  request coalescing) and answers a cheap `GET /healthz` (200).
- **Control plane:** Go `brisk-control` + Postgres/TimescaleDB + NATS JetStream.
  Owns the `servers` table (source of truth), the Bunny DNS reconciler, the
  health checker, purge fan-out, config versioning, and stats ingest.
- **DNS:** Bunny DNS. The `cdn.<zone>` record set is a **geo/latency Smart-Record
  set** — one A record per edge, tagged `brisk:server:<edge_id>`, at the edge's
  region coordinates (geographic) or nearest Bunny region (latency).
- **Dashboard:** React/Voltage. Servers (rotation/health badges + drain), Regions
  (bulk drain), Overview (routing health + events), DNS (full cdn set + lock).

**Effective rotation precedence:** `drain > health > heartbeat`.
- **drained** (operator) → out, even if healthy.
- **unhealthy** (failed external probes) → out (unless all-down guard).
- **offline** (no fresh heartbeat) → out.
- otherwise → **in rotation** (A record enabled).

---

## 2. Add a region (onboard a new edge)

You provision the VPS; Brisk onboards it over SSH and auto-registers it in DNS.

1. **Provision a VPS** in the target region (DigitalOcean/Hetzner/etc.). Note its
   public IP, SSH user, and a password or key (used **once**, never stored).
2. **Dashboard → Servers → Add server.** Fill: name, **region** (use a code the
   region map knows — e.g. `IN-DEL`, `US-IL`, `EU-FRA`, `SG`; see
   `brisk-control/internal/dns/regions.go`), IP, SSH user/port, password or key.
   Watch the live provisioning log stream.
3. The agent installs (Nginx + agent + TLS + Phase-1 stack), registers, pulls
   config, heartbeats, ships stats.
4. **Phase-1 acceptance on the new edge BEFORE it matters for routing:** confirm
   it serves the zone over HTTPS with cache `HIT`/`MISS` (`X-Brisk-Cache`), video
   slices (206), and branded headers (`Server: Brisk`, `X-Brisk-Edge`).
5. When the edge goes **online** (heartbeat fresh + health probes pass), the
   reconciler **auto-adds its A record** to the `cdn.<zone>` set at its region's
   location, short TTL. Verify in the Bunny dashboard and via `dig`/DoH.

**Region not in the map?** Add an entry to `RegionMap` in
`internal/dns/regions.go` (lat/long + Bunny latency-zone + label) and rebuild the
control plane. Unmapped regions still serve (as a plain member of the set) but are
not geo-weighted; the dashboard flags them "unmapped".

**Capacity weighting:** a higher-capacity box can carry more load — set its
`routing_weight` higher (Server detail → Per-PoP configuration → Smart routing,
or `POST /servers/{id}/routing {"weight":N}`). Weight splits load *within a
location group*; it does not override geography across regions.

---

## 3. Drain for maintenance

Drain pulls a PoP out of rotation (record `Disabled=true`) while the box keeps
serving in-flight requests — the "disabled-but-kept" state. New traffic reroutes
to nearby PoPs over the TTL; existing HLS viewers ride segment-retry.

- **One PoP:** Server tile or detail → **Drain** (optional reason) → **Resume**
  when done. (`POST /servers/{id}/drain` / `/undrain`.)
- **Whole region:** Servers → Regions panel → **Drain** the region → **Resume**.
  (`POST /regions/{region}/drain` / `/undrain`.)
- **Guard:** draining the **last in-rotation PoP** (or a region that empties the
  pool) triggers a strong confirm ("This empties the rotation pool"). Only force
  it for a deliberate full-maintenance window.
- **Drain ≠ delete ≠ unhealthy.** Drained shows amber "Draining (maintenance)";
  a health failure shows red "Unhealthy"; both disable the record but for
  different reasons (the dashboard labels which).
- **Undrain returns to health-governed rotation** — a still-sick box stays out as
  "Unhealthy", it is not force-enabled.

---

## 4. Rotation / health badge legend

| Badge | Meaning | DNS record |
|---|---|---|
| **In rotation** (green) | serving — heartbeat fresh, healthy, not drained | enabled |
| **Draining** (amber) | operator drained for maintenance; box still up | disabled-but-kept |
| **Unhealthy** (red) | failing external health probes | disabled (auto) |
| **Offline** (muted) | no fresh heartbeat (agent not checking in) | disabled |

Overview → "Routing health" rolls these up (in-rotation/total, draining,
unhealthy, offline) per region; "Routing events" streams drains/failovers/
recoveries from the DNS audit trail.

---

## 5. Failover characteristics (honest)

```
detection ≈ health_interval × fail_threshold   (10s × 2 ≈ 20s)
failover  ≈ detection + TTL                     (~20s + ~15s ≈ ~30-35s typical)
```

- **~30s is a TYPICAL target, not a guarantee.** Some resolvers cache past the
  record TTL; a subset of users take longer. Measured ~32s end-to-end on the test
  zone (kill → dropped from a real resolver) in Step 4.
- **Recovery is asymmetric:** an edge needs `rise_threshold` (3) consecutive
  successes (~30s) before it re-enters — no flapping.
- **In-flight viewers** (mid-video when a PoP dies) recover via the HLS player's
  segment retry + re-resolution, not DNS. DNS protects new requests.
- **All-down guard:** if every online edge is unhealthy, none are pulled (Bunny
  returns all) — a checker-side blip never black-holes the CDN.
- **Sub-second / guaranteed failover needs Anycast** (own IP space + BGP) — a
  Phase-4+ item, explicitly out of scope here.

Per-PoP tuning: Server detail → Per-PoP configuration → Health checks
(interval / fail / rise; blank = inherit network default), or
`POST /servers/{id}/health`.

---

## 6. LIVE CUTOVER procedure (Part 5 — the production change)

> ⚠️ Production. Do this only when you can watch the dashboard, after staging is
> green, with the rollback ready. **Never leave the live hostname with zero
> in-rotation PoPs.**

**Pre-flight (must all be green on staging / `a2zjav.com` first):**
- [ ] 2–3 real edges online in distinct regions; each passes Phase-1 on its own.
- [ ] Auto-DNS: each edge in the `cdn.<staging>` set; `dig`/DoH+ECS from different
      regions returns the nearest PoP.
- [ ] Failover: kill an edge → ~32s → region's users get next-nearest → restore.
- [ ] Drain: drain a PoP and a region → reroute → resume.
- [ ] Purge / config / stats fan out to **every** edge (verified per-edge).
- [ ] BLR1-01 is in the set and healthy.

**Cutover steps (live `cdn.mainakghosh.com`):**
1. Set the control plane's zone/record env to the **live** zone + the live CDN
   record name, keep **`BRISK_DNS_TTL=15`** (short, for fast rollback).
2. Let the reconciler build the multi-PoP smart set on the live zone (BLR1-01 +
   new edges), all enabled, geo-routed. Verify in the Bunny dashboard.
3. **Repoint the live hostname** to the `cdn.<live-zone>` smart set (CNAME the
   public hostname to the cdn record, or move the apex/sub onto the set) instead
   of the single BLR1-01 A record.
4. **Watch (15–30 min):** site serves from nearest PoP, stays up, cache `HIT`,
   video plays; health/stats green in the dashboard; no error spike.

**One-step rollback (have this ready BEFORE cutover):**
- **Fastest:** in the dashboard, **drain every new edge** (or the whole new
  region) → traffic collapses back onto BLR1-01 within the TTL (~15s). BLR1-01
  stays enabled the whole time. No deletes, no lock fight.
- **Or:** repoint the live hostname back to BLR1-01's single A record.
- The **deletion-protection lock** + **disabled-but-kept** design make both safe:
  rollback never deletes a record; it disables the new ones and leaves BLR1-01.
- After rollback, investigate, fix, re-validate on staging, retry.

**Rollback trigger:** any of — error-rate spike, a region serving stale/empty,
cache not warming, video stalls, or health flapping on the new edges.

---

## 7. Fan-out checks (verify per-edge, not just globally)

- **Purge** (`/purge` or `/zones/{id}/purge`): one purge fans out over NATS to
  **every** edge serving the zone. Verify each edge independently goes `MISS` on
  the purged object; for sliced video, the prefix purge clears slices on all PoPs.
- **Config** (zone/cache-rule change): bumps `config_version`; **every** assigned
  edge re-pulls within its poll interval and converges. Verify each edge's served
  behavior changed.
- **Stats:** each edge ships stats; Overview/Analytics show **per-PoP** and the
  **"All PoPs"** aggregate. Confirm the merge reflects the real fleet.

---

## 8. Phase 3 close-out — what shipped

Brisk is a **true distributed CDN** (pending the live cutover in §6):
- Multi-region edges auto-registered into geo/latency DNS from `servers.region`.
- Nearest-PoP routing (verified via DoH+ECS across regions on the test zone).
- ~32s self-driven health failover, per-PoP configurable, all-down guarded.
- Dashboard-driven drain/maintenance (per-PoP + per-region) with last-PoP guard.
- Purge + config + stats fan out across the fleet.
- Deletion-protection lock with time-delayed unlock.
- All self-hosted: Go control plane + agent, Brisk's Voltage dashboard.

---

## 9. Backlog (carried forward — Phase 4 / cleanup)

**Phase-2/3 cleanup (deferred, still tracked):**
- `PUT /rules/{id}` + bulk rule reorder; `GET /zones/{id}/servers`.
- Network-aggregate `/stats`; status-code / geo / top-paths / latency breakdowns.
- Real Logs API (the dashboard Logs page is an honest placeholder).
- Edge enforcement of custom cache rules end-to-end.
- **Admin auth for the dashboard** (currently open locally; `authHeader()` hook
  is the single slot-in point).
- Latency-zone codes: confirm each `RegionMap` latency-zone against Bunny's
  published list (Bunny stores them verbatim, no validation).

**Phase 4 (preview — do NOT start):**
- Security + productization: WAF / rate-limiting, origin shield (mid-tier cache),
  Lua/OpenResty edge logic, customer custom-domain CNAMEs.
- **Anycast** (own IP space + BGP) → guaranteed sub-second failover beyond DNS TTL.
- Customer Portal on the role-aware API (`accounts.role`) — the commercial layer.

---

## 10. Quick command reference

```bash
# rotation state for one server (in_rotation + reason)
curl -s localhost:8080/api/v1/servers/{id}/rotation

# drain / resume a PoP
curl -s -X POST localhost:8080/api/v1/servers/{id}/drain  -d '{"reason":"kernel upgrade"}'
curl -s -X POST localhost:8080/api/v1/servers/{id}/undrain

# drain / resume a whole region (force to override the empties-pool guard)
curl -s -X POST localhost:8080/api/v1/regions/EU-FRA/drain   -d '{"force":false}'
curl -s -X POST localhost:8080/api/v1/regions/EU-FRA/undrain

# health + routing snapshots
curl -s localhost:8080/api/v1/health/status
curl -s localhost:8080/api/v1/dns/routing
curl -s localhost:8080/api/v1/dns/reconcile/preview   # dry-run: pending changes

# resolve the cdn set from a region (DoH + EDNS client-subnet)
curl -s "https://dns.google/resolve?name=cdn.<zone>&type=A&edns_client_subnet=1.6.0.0/16"
```
