# Brisk — Changing the CDN Base Domain (keep Bunny geo-DNS)

> **Portable runbook.** Plain Markdown so you can read it in any editor / on GitHub from
> any machine — no browser needed. A richer, infographic version is the sibling
> `Brisk_CDN_Domain_Migration_Runbook.html` (open in a browser). Both live in the repo
> (`docs/features/`), so they travel with the project to any laptop.

**Goal:** migrate the main CDN base domain `a2zjav.com` → a new domain (examples use
`newcdn.com`), **while still using Bunny Smart-DNS for geo-routing**.

**Bottom line:** this is a **CONFIG + DNS + CERT** migration, **not a code rewrite**. The
base domain is parameterized everywhere. Bunny's geo-DNS "brain" (Smart Records) is reused
as-is — the new domain's zone just lives in Bunny and the same reconciler writes the same
weighted/geo records; only the *names* change. You **dual-run** both domains (old stays
live) and retire the old once the new is proven → **zero downtime**.

---

## 1. Where `a2zjav.com` actually lives (5 places — all config/data)

| # | Place | Knob | Owner | What it does |
|---|-------|------|-------|--------------|
| 1 | Bunny DNS zone | `BUNNY_DNS_ZONE` / `BUNNY_DNS_ZONE_ID` | `brisk-control/.env` | the zone Bunny manages = where the geo Smart-Records are written |
| 2 | CDN record name | `BRISK_CDN_RECORD` (e.g. `cdn`) | `brisk-control/.env` | the label under the zone Brisk routes (`cdn` → `*.cdn.<zone>`) |
| 3 | Wildcard TLS cert | `BRISK_TLS_DOMAINS` (e.g. `*.cdn.a2zjav.com,*.a2zjav.com`) | `brisk-control/.env` | SANs of the managed wildcard cert; issued via Bunny DNS-01, fanned to edges |
| 4 | Dashboard suggestion | `VITE_CDN_BASE_DOMAIN` (`cdn.a2zjav.com`) | `brisk-dashboard/.env` | what a **new** zone suggests as its hostname — **cosmetic / UX only** |
| 5 | Each zone's hostname | `zones.cdn_hostname` (`x.cdn.a2zjav.com`) | **database** | the actual served name, stored per zone |

> Nothing here is hardcoded in Go/TS. Cert key (`BRISK_TLS_CERT_NAME`) and the Bunny API
> key (`BUNNY_API_KEY`, used for **both** geo-DNS and DNS-01) are the only other related env.

---

## 2. The request path — same geo-DNS brain, new names

```
TODAY
  client (x.cdn.a2zjav.com)
        │
        ▼
  Bunny Smart-DNS  ── geo + weight ──►  Edge NY / FRA / BLR  ──►  200, Server: Brisk
  (zone: a2zjav.com)                                              cert *.cdn.a2zjav.com

AFTER  (same 3 edges, same geo logic, new names)
  client (x.cdn.newcdn.com)
        │
        ▼
  Bunny Smart-DNS  ── SAME geo + weight ──►  Edge NY / FRA / BLR  ──►  200, Server: Brisk
  (zone: newcdn.com)                                                  cert *.cdn.newcdn.com
```

The three edges, the geo brain, and **all agent/edge logic are untouched**. Only the DNS
zone, the records, the cert SANs, and the hostnames change.

---

## 3. Migration pipeline (7 phases, dual-run window over 2–6)

```
[1 delegate zone]→[2 new cert]→[3 reconciler]→[4 dashboard]→[5 dual-host]→[6 tenants re-CNAME]→[7 verify+retire]
  Bunny/you        CP .env       CP .env        dash .env      data          tenants               gated
                  └─────────────────── DUAL-RUN: both domains serve, zero downtime ──────────────────┘
```

- **Order matters:** the cert (2) and new DNS records (3) must exist **before** any client
  is sent to the new name.
- **Steps 5–6 are gradual** — migrate zone by zone, tenant by tenant; both resolve during
  the overlap.
- **Golden rule:** never remove an old hostname (7) until the new one is verified serving
  on **≥2 edges**; never leave a live hostname with zero in-rotation PoPs.

---

## 4. Detailed steps

### Phase 1 — Delegate the new domain's DNS zone to Bunny  *(Bunny / you)*
In the Bunny dashboard add `newcdn.com` as a **DNS Zone**, then at your registrar point the
domain's nameservers to the Bunny nameservers it gives you. This single move lets Bunny do
**both** geo-DNS routing **and** answer the ACME DNS-01 TXT challenge for the cert. The
routing logic is unchanged — same Smart-Record engine, new zone.

```bash
# Verify delegation is live (can take minutes–hours to propagate):
dig NS newcdn.com +short        # should list Bunny's nameservers
```

### Phase 2 — Issue the new managed wildcard TLS cert + fan to edges  *(control plane .env)*
Append the new wildcard(s) to `BRISK_TLS_DOMAINS` — **keep the old ones during migration**:

```ini
# brisk-control/.env
BRISK_TLS_DOMAINS=*.cdn.newcdn.com,*.newcdn.com,*.cdn.a2zjav.com,*.a2zjav.com
BRISK_TLS_STAGING=true     # dry-run on LE staging first; flip to false at cutover
```

Restart the control plane → it issues the managed wildcard via Bunny DNS-01 and ships it to
all 3 edges over the agent-config channel. The agent already selects the **longest-suffix
covering cert** per zone, so once issued, both `a2zjav.com` and `newcdn.com` hostnames are
served.

```bash
# Confirm the cert covers the new wildcard:
openssl s_client -connect <edgeIP>:443 -servername x.cdn.newcdn.com </dev/null 2>/dev/null \
  | openssl x509 -noout -ext subjectAltName
```

### Phase 3 — Point the DNS reconciler at the new zone  *(control plane .env)*
```ini
# brisk-control/.env
BUNNY_DNS_ZONE=newcdn.com        # (or BUNNY_DNS_ZONE_ID for the numeric id)
BRISK_CDN_RECORD=cdn             # label under the zone Brisk routes
```

Restart the control plane. The DNS syncer creates the geo/weighted **Smart A-set** for
`*.cdn.newcdn.com` → your NY / FRA / BLR edges — same record shape as the old zone (same
weights, same Bunny health-monitor backstop if enabled).

```bash
dig x.cdn.newcdn.com +short      # from ≥2 regions → expect the nearest edge IP each
```

> **One zone at a time:** the reconciler manages the zone it's pointed at. To keep the
> *old* records resolving during overlap, just leave them in Bunny as static (they don't
> need active reconciliation to keep answering) and let the syncer own only the new zone.

### Phase 4 — Update the dashboard's suggested base domain  *(dashboard .env)*
```ini
# brisk-dashboard/.env
VITE_CDN_BASE_DOMAIN=cdn.newcdn.com
```
Rebuild/restart the dashboard. **Cosmetic only** — changes what a *new* zone suggests
(e.g. `my-site.cdn.newcdn.com`). Existing zones are unaffected by this value.

### Phase 5 — Give each live zone the new hostname (dual-host)  *(data)*
Each existing zone serves `x.cdn.a2zjav.com` (its stored `cdn_hostname`). **Add** the new
name beside the old rather than renaming in place — add `x.cdn.newcdn.com` via the zone's
**Domains** tab. It renders as its own SNI vhost on every serving edge using the new cert,
so both names answer for the same zone.

> **Do NOT** bulk-rewrite `cdn_hostname` out from under a tenant who has already CNAME'd to
> the old name — that breaks them until they re-point. Dual-host is the safe path.

### Phase 6 — Tenants re-point their CNAME  *(tenants)*
Anyone who CNAME'd their site to `x.cdn.a2zjav.com` updates it to `x.cdn.newcdn.com`. This
happens gradually — both resolve during the overlap. Tenants on their **own custom domains**
(e.g. `cdn.theirsite.com` added in the Domains tab) are **unaffected** — their ACME HTTP-01
certs are independent of the platform's base domain.

### Phase 7 — Verify, then retire the old  *(gated)*
Only once the new domain passes the checklist below: remove `*.cdn.a2zjav.com` /
`*.a2zjav.com` from `BRISK_TLS_DOMAINS` and re-issue (drops the old SANs), delete the old
Bunny records, and remove the old hostnames from zones.

> **Golden rule:** never remove an old hostname until the new is verified on **≥2 edges**,
> and never leave a live hostname with zero in-rotation PoPs. Confirm the currently-serving
> hostnames from the control plane first — derive them live, don't assume.

---

## 5. Verification checklist (before retiring the old)

| Check | How | Pass |
|-------|-----|------|
| New wildcard resolves geo-correctly | `dig x.cdn.newcdn.com` from ≥2 regions | nearest edge IP per region |
| Every edge serves the new host | `curl -k --resolve x.cdn.newcdn.com:443:<edgeIP> https://x.cdn.newcdn.com/` per edge | `200` + `Server: Brisk` on all 3 |
| Cert valid for the new wildcard | `openssl s_client -servername x.cdn.newcdn.com ...` | SAN covers `*.cdn.newcdn.com`, not expired, trusted (staging off) |
| Edge config still byte-clean | `nginx -t` on each edge; agent + nginx active | config test OK, both active |
| Health probe answers | `curl http://<edge>/healthz` | `200 ok` on all 3 (keeps them in DNS rotation) |
| Fleet healthy | control-plane servers list | 3/3 online + heartbeating, none drained/unhealthy |

---

## 6. Don't-forget gotchas

- **Management plane is separate.** If the control-plane / agent-tunnel hostname or the
  ACME-challenge proxy lives under `a2zjav.com`, that's its own cert + DNS record to
  migrate — independent of the customer-facing CDN data plane.
- **Apex SAN.** Only keep the bare `newcdn.com` in the cert if you actually serve the apex;
  otherwise the `*.cdn.newcdn.com` wildcard alone is enough.
- **Zone-create coverage is automatic.** The "is this hostname covered by a managed cert?"
  gate reads from the *issued* certs, so once Phase 2's wildcard exists, new zones under the
  new domain pass with **no code change**.
- **Staging first.** Leave `BRISK_TLS_STAGING=true` for a dry run (untrusted cert, but
  proves DNS-01 + fan-out works), then flip to `false` and re-issue for the real trusted
  cert at cutover.
- **TTL vs delegation.** `BRISK_DNS_TTL` is short (~15s) so new records propagate fast — but
  registrar NS-delegation (Phase 1) can take hours; do Phase 1 well ahead.
- **Bunny API key is shared.** The same `BUNNY_API_KEY` drives geo-DNS *and* DNS-01 — make
  sure the new zone is in the same Bunny account.

---

## 7. Rollback

Because it's a dual-run, rollback is trivial: **the old domain never stopped serving.** To
abort, stop migrating tenants and (optionally) remove the new `*.cdn.newcdn.com` records and
SANs again. Nothing about the old `a2zjav.com` path changes until Phase 7, so there's
nothing to restore. The edge safety nets still apply: agent runs `nginx -t` before any
reload, keeps last-known-good config, and never drops a live hostname.

---

*Brisk CDN · base-domain migration runbook · Bunny geo-DNS retained.
Sibling visual version: `Brisk_CDN_Domain_Migration_Runbook.html`.*
