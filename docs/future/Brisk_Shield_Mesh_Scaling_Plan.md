# Brisk — Origin Shield + WireGuard Mesh Scaling Plan (FUTURE)

> **Status: PLANNING / NOT BUILT YET.** This is a forward-looking design doc for when
> Brisk grows from 3 PoPs to ~15–20. Nothing here is live. When you start building,
> turn each "Step" into gated, off-by-default work (same discipline as the rest of Brisk)
> and verify on one edge before fleet rollout. Keep the golden rule: never drop
> `cdn.a2zjav.com`; never leave a live hostname with zero in-rotation PoPs.

**Author context:** decided 2026-06-13 after researching WireGuard topologies + CDN
shield practice. Origin shield WILL be used. Fleet will grow to 15–20 PoPs.

---

## ⚑ Near-term vs Future — read this first

This doc mixes two timelines. Don't confuse them:

- **NEAR-TERM (standard, safe, do when ready — still gated + careful rollout):**
  - **Step 1 — nginx upstream keepalive** (§2). Standard practice in every professional
    CDN/reverse proxy; graceful reload, no downtime. The free latency win.
  - **Step 2 — 2 shields** (§3). Normal shield/tiered-cache hardening.
  - These are **not** experimental. They follow the usual discipline: gated, `nginx -t`,
    one edge at a time NY→DE→BLR, keep `cdn.a2zjav.com` byte-identical, rollback ready.

- **FUTURE (build later, at ~8–10+ PoPs):**
  - **Step 3 — WireGuard hub-and-spoke overlay** (§4). For **security + origin lockdown**,
    NOT speed. This is the "plan now, build later" piece.

> Plain version: keepalive + 2 shields = near-term, standard, safe. WireGuard mesh =
> future, security-focused. WireGuard is built *on top of* the first two, never instead.

---

## 0. TL;DR — what to do, in order

1. **NOW (free, biggest latency win, NO WireGuard):** add an nginx `upstream { … keepalive N; }`
   pool for the **edge→shield** leg. Today the templates set the HTTP/1.1 prerequisites but
   have **no keepalive pool**, so every cache MISS re-handshakes (~270 ms wasted/miss).
2. **As you grow:** run **2 shields** (near-origin + backup), not one. Keep the public-`:443`
   fallback so shield-down ≠ CDN-down.
3. **At ~8–10 PoPs:** add **WireGuard as a hub-and-spoke overlay** — spokes = edges,
   hub(s) = shield(s). For **security + origin lockdown**, NOT for speed.
4. **Never** hand-manage 20 WireGuard configs. Distribute keys + peers through the
   **brisk-control config-pull channel you already have** (same path that ships certs).
5. **Skip full mesh entirely** — with origin shield there is **no edge→edge traffic**, so
   full mesh buys nothing and explodes to 190 links at 20 PoPs.

---

## 1. The core realization: origin shield IS hub-and-spoke

Origin shield routes **all** edge→origin traffic through one (or a few) shield PoP(s).
Edges talk to the **shield**, never to each other. So the traffic pattern is *already*
hub-and-spoke, with the **shield as the hub**. That means:

- The "full-mesh detour" problem does **not** apply to us (we have no edge↔edge traffic).
- Hub-and-spoke is the correct topology — and it's the one that scales cleanly.
- AWS Well-Architected explicitly says *"prefer hub-and-spoke over many-to-many mesh."*

### Topology comparison

```
FULL MESH (WRONG for us — every PoP peers with every other)
        NY ───── FRA
         \  ╲   ╱  /
          \  ╲ ╱  /          20 PoPs = 190 tunnels, 19 peers/box,
           \  ╳  /           "N dashboards, no shared state". DON'T.
          BLR ─── SG ...

HUB-AND-SPOKE ON THE SHIELD (RIGHT for us)
   NY ──┐
   FRA ─┤
   BLR ─┼──▶  SHIELD(s) ──(public :443)──▶ ORIGIN
   SG ──┤        ▲
   ... ─┘        └─ each edge has ONE peer: the shield
                    20 PoPs = 20 links (or 40 with 2 shields)
```

### Why hub-and-spoke wins at scale

| PoPs | Full mesh (edge↔edge) | Hub-and-spoke (edge↔shield) |
|------|-----------------------|------------------------------|
| 3    | 3 links               | 3 links                      |
| 10   | 45 links              | 10 links                     |
| 20   | **190 links**         | **20 links** (40 w/ 2 shields)|

---

## 2. Step 1 (DO FIRST) — nginx upstream keepalive for edge→shield

**This is the real latency win, and it needs NO WireGuard.**

### What's there today
- `brisk-agent/nginx/templates/nginx.conf.tmpl` — `keepalive_timeout 65` /
  `keepalive_requests 10000` = **client-side** keepalive (visitor↔edge) only.
- `brisk-agent/nginx/templates/server.tmpl` — every proxy location sets
  `proxy_http_version 1.1` + `proxy_set_header Connection ""` (the *prerequisites*),
  BUT there is **no `upstream { … keepalive N; }` block**, and origin uses
  `proxy_pass …://$brisk_origin` (a variable → fresh DNS resolve → fresh connection).
- Net: **edge→shield and edge→origin connections are NOT pooled.** Every MISS pays
  full TCP + TLS handshake.

### The change (sketch — wire into `briskupstream` template + nginx.go)
```nginx
# Per shield host:port the edge talks to, declare a pooled upstream:
upstream brisk_shield_<zoneOrShieldId> {
    server <shield-host>:443;
    keepalive 32;              # pool size; tune to concurrency
    keepalive_timeout 60s;
    keepalive_requests 1000;
}

# In the proxy location (shield path), point proxy_pass at the named upstream
# (NOT a variable) so connections actually pool:
location / {
    proxy_http_version 1.1;
    proxy_set_header Connection "";
    proxy_pass https://brisk_shield_<id>;   # named upstream = pooled
    # ...existing cache/headers/fallback...
}
```

**Caveat:** a named `upstream` with a literal `server` pins the IP at reload (no per-request
resolve). That's fine for a fixed shield; render/reload on shield change (the agent already
reloads on config change). The **origin** leg can stay variable-based (it's behind the
shield now, hit rarely). Keep the existing `proxy_next_upstream` fallback to origin.

### Expected win
- **Per MISS through the shield:** removes 2 handshake RTTs ≈ **270 ms saved** (FRA→BLR
  example) / **420 ms** (NY→BLR). HITs unchanged (most traffic). See §6 table.

---

## 3. Step 2 — run 2 shields, not 1

- One shield = single point for offload; if it dies, all edges fall back to origin at once.
- Run **2 regional shields** (e.g. near-origin India + a backup region). Cloudflare's
  "Smart Tiered Cache" does this automatically (picks best upper-tier).
- Brisk already has per-(server,zone) shield selection (`shieldUpstreamFor`,
  `setZoneShield`, `role=shield`). Extend selection to **prefer nearest healthy shield**,
  fall back to the other shield, then to origin.
- **Always keep the public-`:443` fallback** → shield-down ≠ CDN-down.

### Hybrid shield+PoP toggle (decided 2026-06-13)
On a small/budget fleet you can't afford to pull a box from public rotation just to make it
a shield. Add a per-server **"also serve as public PoP"** flag so `role=shield` can keep its
public A-record AND act as parent. Loop-guard already exists (`X-Brisk-Shield`
self-detection) so a local request on the shield-PoP serves locally instead of looping.

---

## 4. Step 3 — WireGuard hub-and-spoke overlay (at ~8–10 PoPs)

**Build this for SECURITY + origin lockdown, NOT speed** (keepalive already captured the
latency). WireGuard rides the same public internet underneath → ~0 extra speed, ~1–2 ms
overhead.

### Topology
- **Spokes = all edges. Hub(s) = the shield(s).** Each edge peers ONLY with the shield(s).
- Private subnet e.g. `10.77.0.0/24`; shields `10.77.0.1` / `10.77.0.2`; edges `10.77.0.N`.
- Edge→shield proxy_pass targets the shield's **private wg IP** when mesh enabled for a zone.
- `PersistentKeepalive = 25` on every peer (prevents idle-timeout / NAT-drop).

### Data-plane sketch
```
 Visitor (public :443) ── unchanged ──▶ nearest edge PoP
                                            │ cache MISS
                                            ▼
   edge ──(wg0, private 10.77.0.x, encrypted)──▶ SHIELD ──(public :443)──▶ ORIGIN
            │
            └─ if wg peer DOWN ▶ public :443 to shield/origin (nginx `backup` upstream)
```

### Code touch-points (future)
- **bootstrap:** `EnsureWireGuard` — idempotent install `wireguard-tools`, write
  `/etc/wireguard/wg0.conf`, gated off by default.
- **key/peer distribution:** brisk-control ships this edge's private key + peer list
  (public keys + endpoints) over the **existing config-pull channel** (same as certs).
  Self-hosted, on our own code → satisfies golden rule #1 (no third-party in runtime).
- **nginx:** shield upstream `server <shield-private-ip>:443;` with a second
  `server <shield-public-ip>:443 backup;` so a dead tunnel auto-falls-back.
- **origin lockdown:** origin/shield firewall → accept only from `10.77.0.0/24` (replaces /
  augments the `origin_pull_secret` header).
- **runtime independence:** mesh is kernel-level; once configured it runs even if control
  plane is down. Mesh is an optimization, never a dependency.

### Why NOT raw per-box WireGuard at 20 PoPs
Research is blunt: past 2 servers, raw WireGuard = "N independent dashboards, no shared
state." Two options:
1. **(preferred) brisk-control distributes keys/peers** via config-pull — keeps it
   self-hosted and on our code.
2. self-hosted manager (headscale / Netmaker) — adds a runtime dependency; only if we don't
   want to build key distribution.

### When peers go down (design for it)
Causes: remote box reboot, public-network outage, firewall/UDP-port block, key mismatch,
NAT/IP change, idle timeout (no `PersistentKeepalive`). **Mitigation = the public-`:443`
fallback above.** Mesh down ≠ CDN down.

---

## 5. Step 4 — full mesh? NO.

With origin shield there is **no edge→edge traffic**, so full mesh adds links and management
overhead for zero benefit. Use hub-and-spoke on the shield. (Revisit ONLY if a future
feature needs edge↔edge, e.g. peer cache fill — not planned.)

---

## 6. Honest win / loss

### Latency (time-to-first-byte on a cache MISS through shield; origin in India)
| Path | Without keepalive (cold) | With keepalive **or** mesh (warm) |
|------|--------------------------|------------------------------------|
| FRA → shield (BLR, ~135 ms leg) | ~405 ms | **~137 ms** |
| NY → shield (BLR, ~210 ms leg)  | ~630 ms | **~212 ms** |
| Cache HIT (most traffic)        | ~5–20 ms | ~5–20 ms (unchanged) |

- The ~135/210 ms "data travel" line is **physics** — WireGuard cannot reduce it.
- Warm-connection saving comes from **keepalive**; WireGuard adds ~0 more speed.

### What each layer actually buys
| Layer | Latency | Origin load | Security | Mgmt cost |
|-------|---------|-------------|----------|-----------|
| nginx upstream keepalive | **−270–420 ms/miss** | — | — | tiny (config) |
| 2 shields (offload)      | small | **10–20× less origin load at 20 PoPs** | — | low |
| WireGuard hub-spoke      | ~0 (−1–2 ms) | — | **private net + origin lockdown** | medium (key distro) |

### Big picture at 15–20 PoPs
- **Biggest real win = the shield's origin offload** (20 edges → 1–2 shields hitting origin).
- **Latency win = keepalive** (do it now, free).
- **WireGuard = security/manageability**, plus a foundation for Argo-style smart-relay later.

---

## 7. How the big CDNs do it (reference)
- **Private physical fiber** (Google B4, Meta, MS, AWS) — real faster paths; we can't replicate.
- **Private peering at IXPs** — direct cables into ISPs, skip public transit.
- **BGP Anycast** (Cloudflare/Fastly) — one IP from all PoPs; we use DNS geo-routing instead.
- **Smart routing** (Cloudflare Argo "virtual backbone", ~30% avg) — pick better intermediate hop.
- **Caches inside ISPs** (Netflix Open Connect).
- **Tiered cache / origin shield** (Cloudflare Tiered, Fastly shielding, CloudFront Origin
  Shield) — exactly what we implement.

WireGuard ≈ private encrypted link over the *public* internet. It gives the privacy/security
part cheaply; it cannot give fiber or peering. So our wins come from **shield offload +
keepalive**, with WireGuard as the security layer.

---

## 8. Sources
- AWS Well-Architected — prefer hub-and-spoke over mesh:
  https://docs.aws.amazon.com/wellarchitected/2025-02-25/framework/rel_planning_network_topology_prefer_hub_and_spoke.html
- WireGuard topologies (Pro Custodibus):
  https://www.procustodibus.com/blog/2020/10/wireguard-topologies/
- WireGuard at scale — operations (dev.to):
  https://dev.to/dminglv/wireguard-at-scale-setup-is-solved-operations-arent-2aoi
- Tailscale — mesh VPN topology: https://tailscale.com/learn/understanding-mesh-vpns
- Cloudflare Tiered Cache: https://developers.cloudflare.com/cache/how-to/tiered-cache/
- KeyCDN — Origin Shield: https://www.keycdn.com/support/origin-shield
- Fastly — how shielding improves performance:
  https://www.fastly.com/blog/let-the-edge-work-for-you-how-shielding-improves-performance
- Cloudflare Argo Smart Routing:
  https://www.cloudflare.com/application-services/products/argo-smart-routing/

---

## 9. Build checklist (when you start)
- [ ] Step 1: nginx upstream keepalive for edge→shield (gated render + nginx -t + 1-edge verify)
- [ ] Measure miss latency before/after (prove the ~270 ms)
- [ ] Step 2: 2nd shield + nearest-healthy-shield selection + hybrid shield+PoP toggle
- [ ] Step 3: `EnsureWireGuard` bootstrap (gated off) + key/peer distribution via config-pull
- [ ] Step 3: shield upstream private-IP + public-IP `backup` fallback
- [ ] Step 3: origin lockdown to `10.77.0.0/24`
- [ ] Chaos test: kill a wg peer → confirm public fallback, no dropped requests
- [ ] Keep `cdn.a2zjav.com` byte-identical throughout every rollout
