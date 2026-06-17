# Brisk — Hybrid Shield+PoP + edge→shield Keepalive — BUILD SPEC

> **Status: READY TO BUILD — not started.** This lives in `docs/` (the active build queue),
> not `docs/future/`. It is the exact build plan for one coherent feature: the
> **hybrid shield+PoP** role state **plus** the **edge→shield nginx keepalive** pool.
> They ship together because keepalive only matters once a shield tier exists.
> Architecture/why lives in
> [future/Brisk_Shield_Mesh_Scaling_Plan.md](./future/Brisk_Shield_Mesh_Scaling_Plan.md);
> this doc is the *how* (steps + acceptance + rollout).

**Decided 2026-06-13 (locked):**
- **Sequence:** spec first (this) → gated implement → careful one-edge-at-a-time rollout.
- **Scope:** FULL hybrid (serve_public role state + DNS + dashboard toggle) + keepalive, as one unit.
- **Interim:** **DO NOT pool edge→origin.** Keepalive is edge→shield ONLY (between our own
  PoPs, where we control both ends). It ships dark (present but inert) until a hybrid-shield
  box is enabled — so the live fleet stays byte-identical until you deliberately flip a box.
- **Origin-leg keepalive** is split out as a deferred option →
  [future/Brisk_OriginLeg_Keepalive_Optional.md](./future/Brisk_OriginLeg_Keepalive_Optional.md).

> **⚑ Live-zone safety target (READ):** the live zone set has changed — the old
> `cdn.a2zjav.com` is **no longer serving**. Different zones are configured now. Before any
> rollout, **confirm the hostnames currently serving on the live fleet** and treat THOSE as
> the byte-identical target. Throughout this doc, "live production zones" = whatever is
> actually live at build time. Golden rule still holds: never drop a live hostname; never
> leave a live hostname with zero in-rotation PoPs.

---

## 0. Goal & non-goals

**Goal:** let one box be **both** a public PoP **and** the origin shield (parent cache),
and make the **edge→shield** connection reuse pooled keepalive connections (saving the
~270–420 ms TCP+TLS handshake per cache MISS on the PoP-to-PoP hop).

**Non-goals (explicitly OUT for v1):**
- **No edge→origin keepalive.** Origin leg stays variable `proxy_pass $brisk_origin`. The
  optional future toggle is documented separately →
  [future/Brisk_OriginLeg_Keepalive_Optional.md](./future/Brisk_OriginLeg_Keepalive_Optional.md).
- No WireGuard (separate future doc).
- No change to any zone that doesn't enable a shield. Default config stays byte-identical.

---

## 1. The five layers (build order)

### Layer 1 — DB / migration  (`brisk-control`)
- **Migration 00021** (next free number — verify against the migrations folder before writing):
  add `servers.serve_public BOOLEAN NOT NULL DEFAULT true`.
  - For `role=shield`: `serve_public=true` ⇒ **hybrid** (keep public DNS + act as parent);
    `serve_public=false` ⇒ today's behavior (shield only, excluded from DNS).
  - For `role=edge`: flag ignored (edges are always public).
- Store: extend `Server` struct + scans/inserts/updates with `ServePublic`.
- **Gate 1:** migration applies cleanly; existing rows default `serve_public=true`. Verify no
  pure shield is live first; if one exists, backfill it `false` in the same migration.

### Layer 2 — DNS reconciler  (`brisk-control/internal/dns`)
- Today shields are **excluded** from public A-sets.
- Change: include a server in public DNS when `role=edge` **OR** (`role=shield` AND
  `serve_public=true`). Exclude only pure shields (`role=shield` AND `serve_public=false`).
- Apply to both apex `cdn` set and per-zone Smart A-sets.
- **Gate 2:** hybrid-shield box shows **in** the A-set (`dig`); pure shield is **absent**.
  No change for edge-only fleets.

### Layer 3 — Agent render  (`brisk-agent/nginx`)
> **Plain English: this is the PoP-to-PoP speed-up (e.g. NY/FRA → BLR).** The "edge→shield
> keepalive" below IS the warm connection between our own PoPs — every edge that fetches a
> miss through the shield reuses an open connection instead of re-handshaking. That's the
> ~270–420 ms/miss saving. The `keepalive 32` directive turns it on. The keepalive lives on
> the **caller** (the edge); the shield is the destination. See
> [future/Brisk_Shield_Mesh_Scaling_Plan.md](./future/Brisk_Shield_Mesh_Scaling_Plan.md).

- **Named upstream + keepalive** for the shield leg. Replace the shield-path `proxy_pass`
  (currently `proxy_pass https://{{ .ShieldHostPort }}`) with a named upstream:
  ```nginx
  upstream brisk_shield_<id> {
      server <shield-host>:443;
      keepalive 32;
      keepalive_timeout 60s;
      keepalive_requests 1000;
  }
  ```
  proxy_pass → `https://brisk_shield_<id>`, keep `proxy_http_version 1.1` +
  `proxy_set_header Connection ""` (already present), keep the `proxy_next_upstream`
  fallback to origin.
- **Loop-guard for hybrid:** when the box IS the selected shield for a zone (self), it must
  **serve locally**, not proxy to itself. Reuse the existing `X-Brisk-Shield` /
  `shieldUpstreamFor` self-detection — if shield target == this edge, render the
  direct-origin path (no shield hop).
- **Origin leg unchanged:** still `proxy_pass {{ .OriginScheme }}://$brisk_origin` with the
  resolver (no keepalive). Honors "not origin".
- **Gate 3:** `nginx -t` clean for: (a) edge-only zone (byte-identical to today),
  (b) zone with remote hybrid-shield (named upstream present), (c) the shield box itself
  (local serve, no self-loop).

### Layer 4 — Dashboard  (`brisk-dashboard`)
- Server detail / role control: add **"Also serve as a public PoP"** switch, visible only
  when `role=shield`. Wire to a `serve_public` field on the set-role endpoint.
- Origin Shield card copy: clarify "Network default shield" can now be a hybrid box.
- **Gate 4:** toggling the switch round-trips (`GET` reflects it); build passes.

### Layer 5 — Rollout (live, gated)
- Build ONE agent binary; back up `brisk-agent.prev` + `brisk.conf.bak` on each edge.
- One edge at a time (NY → DE/FRA → BLR, or whatever the current edge order is): drain →
  deploy → `nginx -t` → reload → verify → undrain. **Every live production zone** must stay
  **200 / HIT / Server: Brisk / byte-identical** the whole time, served by ≥2 edges throughout.
- Because no zone enables a shield yet, **every edge should render byte-identical config**
  after this deploy (keepalive ships dark). Prove it (sha256 of each live zone's rendered
  vhost unchanged).
- Keep per-edge one-command rollback.

---

## 2. Acceptance tests (local Docker first, then live)

Local `docker` harness (mirror the existing shield/lua-lab style):

1. **No-shield parity:** zone with no shield → rendered config for that vhost is
   **byte-identical** to pre-change (sha256 match). ← the critical safety test.
2. **Hybrid in DNS:** mark box S `role=shield, serve_public=true` → reconciler includes S
   in the A-set; `serve_public=false` → S absent.
3. **Keepalive present:** zone with remote shield → rendered config has
   `upstream brisk_shield_* { … keepalive 32; }` and proxy_pass points at it (not a variable).
4. **Connection reuse proven:** drive N requests through the shield; confirm upstream
   connections are reused (shield access log shows far fewer new connections than requests,
   or conn-count stays flat). Measure miss TTFB before/after → expect ~handshake-RTT drop.
5. **Self-loop guard:** request hitting the hybrid box that is its own shield serves locally
   (no self-proxy, no infinite loop, correct cache status).
6. **Fallback:** kill the shield → edge falls back to origin (existing `proxy_next_upstream`),
   no 5xx storm.
7. **Origin untouched:** origin leg still uses variable proxy_pass (no keepalive pool for origin).

Live (post-rollout): every live production zone 200/HIT/byte-identical at every step;
`/healthz` ok; TLS 1.3.

---

## 3. Rollback
- Per edge: restore `brisk-agent.prev` + `brisk.conf.bak`, `nginx -t`, reload.
- DB: migration 00021 is additive (one defaulted column); down-migration drops it. The flag
  defaults `true` but is inert until a box is set `role=shield`.

---

## 4. Risks & mitigations
| Risk | Mitigation |
|------|------------|
| Named upstream pins shield IP (loses re-resolve) | Shield IPs are stable (our own boxes); agent reloads on config change. Origin leg stays variable. |
| Existing pure shield flips to hybrid via default `true` | Verify none live; backfill `false` in the migration. |
| Self-proxy loop on hybrid box | Reuse proven `X-Brisk-Shield` self-detection; acceptance test #5. |
| Live config drift | Byte-identical sha256 proof (test #1) per live zone before any reload; gated one-edge-at-a-time. |

---

## 5. Definition of done
- [ ] Migration 00021 + store `serve_public`
- [ ] DNS reconciler includes hybrid shields, excludes pure shields
- [ ] Agent: named-upstream keepalive (shield leg) + self-loop guard; origin leg unchanged
- [ ] Dashboard "Also serve as a public PoP" toggle
- [ ] All 7 local acceptance tests pass; no-shield parity sha256 match
- [ ] Gated one-edge-at-a-time rollout; every live production zone byte-identical throughout
- [ ] Per-edge rollback verified
