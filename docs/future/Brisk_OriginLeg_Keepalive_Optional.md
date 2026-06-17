# Brisk — Optional: Origin-Leg Keepalive (shield → origin) — FUTURE / NOT NOW

> **Status: FUTURE / NOT NOW.** This is a deliberately-deferred option, split out of the
> main build spec so the active plan stays clean. The active feature
> (Hybrid Shield+PoP + edge→shield keepalive) lives in
> `docs/Brisk_HybridShield_Keepalive_BuildSpec.md`. This doc is the "maybe later" idea.

**Question raised 2026-06-13:** *"Can we add keepalive on the shield → origin leg too?"*
**Answer: yes, technically — but keep it OFF by default. Build only if/when wanted.**

---

## Why it's OFF by default

- **Keepalive's safe benefit is between OUR OWN PoPs** (edge → shield), because we control
  both ends — stable IPs we manage. The **origin is often NOT ours** (customer servers), so
  pooling that leg is a different risk profile.
- **Open-source nginx constraint:** to pool a connection you need a named
  `upstream { server <ip>; keepalive N; }`, which **pins the origin IP** at reload. Today the
  origin uses a *variable* `proxy_pass $brisk_origin` + resolver that **re-resolves DNS every
  request** (`brisk-agent/nginx/templates/server.tmpl`). Safe re-resolution on a pooled
  upstream needs **nginx Plus** (paid). So on open-source: pooled origin = pinned IP = breaks
  if the origin IP changes, until the next reload.
- **The benefit is small:** the shield→origin hop is hit **rarely** — the shield caches once
  and serves all edges from cache. So pooling it adds risk to speed up a ~1%-of-traffic leg.
- **Multi-tenant caveat:** customer/third-party origins change IPs (failover, their own CDN,
  dynamic DNS). Variable + resolver is the safe default for arbitrary origins.

## Trade-off

| | Pooled origin (flag ON) | Variable origin (default) |
|---|---|---|
| Speed on shield's own miss | slightly faster | slightly slower |
| If origin IP changes | breaks until reload ⚠️ | auto-follows ✅ |
| Good for | your OWN stable-IP origin | any origin (incl. customer) |

## The opt-in design (build later if wanted)

- A **per-zone** `origin_keepalive` flag, **default false**.
- When `true` (only sensible for *your own* stable-IP origin), render the origin as a named
  `upstream { server <origin>; keepalive 16; }`. Customer zones stay on the safe variable path.
- **Acceptance:** a zone with the flag OFF must render **byte-identical** to today.
- Gated + careful rollout, same discipline as the main spec.

## Decision

Leave OUT of the v1 hybrid+keepalive build. Revisit only for our own stable origin if
profiling ever shows the shield→origin leg is a real bottleneck (unlikely, given it's rarely
hit). See `docs/Brisk_HybridShield_Keepalive_BuildSpec.md` for the active plan.
