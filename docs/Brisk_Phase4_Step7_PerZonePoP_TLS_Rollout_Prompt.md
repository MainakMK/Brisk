# Brisk CDN — Phase 4 / Step 7 (cont.): Per-Zone PoP Routing + Auto-TLS + Delete-Teardown + Dashboard + E2E

**For Claude Code.** Continues `docs/Brisk_Phase4_Step7_AutoOnboard_Prompt.md` (Parts 1–2 done) and `docs/Brisk_Part2Gate_WildcardCNAME_Fix_Prompt.md`. **State:** GATE 0 green (zone 6 `cdn.a2zjav.com` restored/managed/cfg 8, all 3 edges; live site 200/HIT); tunnels on **key auth**; **Part 1 auto-assign live** (`createZone` assigns all online+in-rotation edges, `assign_all` opt-out, bumps `config_version`); **Part 2 wildcard CNAME landed** (`*.cdn → cdn.a2zjav.com`, Bunny rec 17977097, tagged `brisk:wildcard:cdn`). Edges: `US-NY-prod01` 104.248.231.144 (id 19), `EU-FRA-prod01` 188.245.225.172 (id 20), `BLR1-01` 139.59.78.21 (id 3). Bunny zone `a2zjav.com` id 807319, authoritative NS `kiki.bunny.net`. Hand-rolled `internal/dns` client (over libdns) with first-class Smart-Record fields already manages the `cdn` Smart A-set.

> **New requirement that drives this session:** **per-zone PoP control.** The wildcard CNAME makes every tenant hostname share ONE A-set, so all tenants currently route to the same PoPs — you cannot disable BLR for `testmainak` while keeping all 3 for `testjim`. Fix = **per-zone Smart A-record sets** derived from `server_zones` (the single source of truth for both DNS and which edges serve). Parts 3–6 (TLS + agent rollout + e2e) then build on that.

> **⚠️ This session includes the live agent rollout to all 3 production edges.** Order: **Part 2.5 (DNS-only, no rollout) → verify → Part 3 + Part 4 share ONE new agent version → roll NY → FRA → BLR (drain → deploy → verify → undrain), rollback ready, `cdn.a2zjav.com` 200/HIT throughout → Part 5 dashboard → Part 6 e2e.** Each part has a gate; STOP and report at each gate. Do NOT begin the agent rollout until Part 2.5 is verified and you've taken a fresh control-plane DB dump + per-edge config/cert backup.

## Goal (one line)
Make routing **per-zone** (each hostname resolves + serves on exactly its assigned PoPs), auto-cover all tenant hostnames with TLS, fix zone-delete teardown, surface per-zone PoP toggles in the dashboard, and verify the whole thing end-to-end — with `cdn.a2zjav.com` untouched.

---

## Locked design (build to these)
- **`server_zones` is the single source of truth.** It already controls vhost rendering; now it also controls each zone's DNS A-set. Auto-assign (Part 1) defaults new zones to ALL online edges; disabling a PoP for a zone = unassigning that edge from that zone.
- **Per-zone Smart A-record sets** (authoritative routing): each zone hostname gets its own geo+health A-set = IPs of its assigned ∩ online ∩ in-rotation edges, mirroring the `cdn` set's Smart fields.
- **Wildcard CNAME `*.cdn` demoted to catch-all** (explicit per-zone A-set overrides it; keeps brand-new names resolvable in the brief pre-provision window).
- **`*.cdn.a2zjav.com` wildcard TLS cert** (lego v4 + Bunny DNS-01) covers all tenant hostnames regardless of their PoP set; fanned to all edges; agent picks the covering cert per `server_name`.
- **Explicit zone-removal teardown signal** (not "the list went empty") so last-zone deletes still drop the vhost without weakening the ≥1-zone guard.
- **One agent version** carries both covering-cert selection (Part 3) and the removal signal (Part 4) → one rollout.
- `cdn.a2zjav.com` stays byte-for-byte unchanged throughout.

---

## PART 2.5 — Per-zone Smart A-record DNS (control-plane + Bunny only; NO agent rollout)
This is the per-zone PoP mechanism. DNS-only, low risk.

1. **Reconciler builds a per-zone A-set.** For each active zone, maintain a Smart A-record set at the zone's relative Name (e.g. `testmainak.cdn`, `testjim.cdn`), containing one A record per **assigned ∩ online ∩ in-rotation** edge IP, with the **same geo/latency Smart fields + health monitoring** the `cdn` set uses, TTL matching the zone's convention, tagged `brisk:zone:<id>`. Derive membership from `server_zones` joined to `servers`.
2. **Keep it in sync** on: zone create (auto-assigned all → full set), manual assign/unassign (the PoP toggle), edge drain/undrain, edge health change, edge delete. Idempotent; never-clobber non-Brisk records; only manage `brisk:`-tagged records.
3. **Wire unassign → both layers.** Unassigning an edge from a zone must (a) bump that zone's `config_version` so the edge drops the vhost on next pull, AND (b) trigger the DNS reconcile so the edge IP leaves the zone's A-set. Assign does the inverse. (Assign/unassign endpoints already exist from Part 1's manual path.)
4. **Edge cases:** zone with zero assigned/online edges → remove its A-set entirely (NXDOMAIN, not mis-routed) and it has no vhost anywhere. A single-edge zone → single A record (still health-monitored). The `*.cdn` wildcard remains as catch-all but per-zone sets win.
5. **Do not touch** the `cdn.a2zjav.com` A-set or the `*.cdn` CNAME logic beyond demoting it to catch-all in the docs/comments.

### 🔒 Gate 2.5 (verify before any rollout)
```bash
# testjim assigned to all 3 -> 3-IP geo A-set:
dig +short testjim.cdn.a2zjav.com A @kiki.bunny.net        # expect NY/FRA/BLR per geo (set of 3)
# unassign BLR from testmainak via the API (the "disable India" action), then:
dig +short testmainak.cdn.a2zjav.com A @kiki.bunny.net     # expect ONLY NY + FRA (no 139.59.78.21)
# bare apex unchanged:
dig +short cdn.a2zjav.com A @kiki.bunny.net                # unchanged edge set
# Bunny shows per-zone sets tagged brisk:zone:<id>; reconcile idempotent (no churn on 2nd run)
```
Confirm `server_zones` ↔ DNS A-set match exactly for both zones. **STOP and report.**

---

## PART 3 — Auto-TLS `*.cdn.a2zjav.com` (part of the single agent rollout)
1. **Issue** `*.cdn.a2zjav.com` via the existing lego v4 + Bunny DNS-01 flow (TXT at `_acme-challenge.cdn.a2zjav.com`, same key, never logged). **Additive-then-verify:** obtain + distribute the new cert; `cdn.a2zjav.com` keeps `*.a2zjav.com`. On issuance failure, alarm + retry/backoff; never touch the live cert.
2. **Fan to edges** over the existing cert channel; store at your cert-dir convention (e.g. `/etc/brisk/tls/_wildcard.cdn.a2zjav.com/`).
3. **Agent covering-cert selection** in `server.tmpl`: pick `ssl_certificate` by longest-suffix match — `cdn.a2zjav.com` → `*.a2zjav.com`; `<tenant>.cdn.a2zjav.com` → `*.cdn.a2zjav.com`. Small helper so per-host SNI certs (Step 4.8 custom domains) drop in later. (Cert is independent of a zone's PoP set — a 2-PoP zone's vhosts on NY/FRA both carry the wildcard.)
4. **Auto-renew both wildcards** (ARI / ~30 days, zero-downtime reload, keep old cert on failure); surface issuer/expiry/last-renewed for both.

## PART 4 — Delete-teardown + `purge_jobs` FK (same agent version)
1. **`purge_jobs` FK migration:** `zone_id` → `ON DELETE SET NULL` and **store the zone hostname (text)** on the row at creation, so purge history survives zone deletion. (The NATS publish already fires.)
2. **Explicit zone-removal signal:** on `DELETE /zones/{id}`, the control plane tells agents to remove that specific zone's vhost (`removed_zone_ids` in the pull delta or a `zone.removed` NATS event) **and** bumps `config_version`. Agent removes the named vhost and re-renders the remainder.
   - **Invariant:** the ≥1-zone guard still rejects empty/garbage/failed fetches; only an **authenticated, version-bumped config that explicitly removed its last zone** is allowed to render to `default_server`. Teardown is driven by explicit removal, never by an empty list.
3. **Also remove the zone's per-zone DNS A-set** (Part 2.5) and purge its cache on delete. Deleted hostname → `default_server`/404, no stale HIT, on every edge it was on.
4. **Delete guards:** keep type-the-hostname; add a louder warning for the edge's **last/only** zone or a production zone (this is how `cdn.a2zjav.com` got nuked). Audit who/when.

### 🔒 AGENT ROLLOUT RUNBOOK (Parts 3+4 ship as ONE new agent version)
Pre: fresh control-plane DB dump + per-edge backup (`nginx.conf.brisk-bak`, current certs, agent binary). Then **one edge at a time, NY → FRA → BLR:**
1. **Drain** the edge (DNS reconciler marks its A disabled-but-kept; existing conns finish). Confirm it's out of rotation.
2. **Deploy** the new agent + new cert to that edge.
3. **Verify on that edge:** `nginx -t` passes; `cdn.a2zjav.com` 200/HIT with `*.a2zjav.com`; a tenant hostname (e.g. `testjim`) 200 with `*.cdn.a2zjav.com` (verify code 0); covering-cert selection correct; teardown signal removes a test vhost cleanly.
4. **Undrain**; confirm back in rotation and serving.
5. Only then proceed to the next edge. **Any failure → roll that edge back to the backup, keep it drained, STOP and report.** `cdn.a2zjav.com` must stay 200/HIT on the other edges throughout.

### 🔒 Gate 3+4 (after all 3 edges)
```bash
for ip in 104.248.231.144 188.245.225.172 139.59.78.21; do echo "== $ip =="; \
  curl -ksI --resolve cdn.a2zjav.com:443:$ip https://cdn.a2zjav.com/ | egrep -i 'HTTP/|server:|x-brisk-cache'; \
  echo | openssl s_client -connect $ip:443 -servername testjim.cdn.a2zjav.com 2>/dev/null | openssl x509 -noout -subject -ext subjectAltName; done
# cdn.a2zjav.com: 200/HIT/*.a2zjav.com on all 3 ; testjim: *.cdn.a2zjav.com, verify 0
# delete a throwaway zone -> within ~20-30s all assigned edges: vhost gone (default_server/404, no stale HIT) + cache purged + per-zone A-set removed + purge_jobs row survives w/ hostname
# last-zone delete still tears down; empty/failed fetch still rejected (guard intact)
```
**STOP and report.**

## PART 5 — Dashboard: per-zone PoP toggles + DNS/TLS status
- **Per-zone edge assignment toggles** — enable/disable each PoP (NY/FRA/BLR) for a zone; this is the UI for "disable India for `testmainak`." Show current assigned PoPs, with optimistic update + propagation hint (config pull + DNS TTL). Wire to the assign/unassign endpoints.
- **Zone detail:** assigned PoPs, per-zone DNS A-set (which IPs resolve), TLS status (covered by `*.cdn.a2zjav.com`).
- **DNS/TLS area:** both managed wildcards (`*.a2zjav.com`, `*.cdn.a2zjav.com`) issuer/expiry/last-renewed; the `*.cdn` catch-all CNAME.
- **Delete dialog:** type-the-hostname + last-zone/production warning.
- Voltage palette, dark-first; skeleton/empty/error; honest propagation language. Rebuild + redeploy (new hash, verify in browser).

**Gate 5:** toggling BLR off for `testmainak` in the UI → within propagation, BLR drops from its DNS A-set + its vhost; toggling on restores both. Both wildcards shown.

## PART 6 — End-to-end verification
1. **`testjim` (all PoPs):** resolves to all 3 (geo), serves 200 on all 3, valid `*.cdn.a2zjav.com` TLS.
2. **`testmainak` (BLR disabled):** resolves to NY+FRA only, an India-geo query lands on FRA (never BLR), no BLR vhost, valid TLS; re-enable BLR → back to 3.
3. **Fresh zone:** create via dashboard → auto-assigned all PoPs → resolves + serves + TLS automatically → toggle a PoP off/on → delete → clean teardown (vhost + cache + DNS + purge_jobs row).
4. **Re-onboard zone 12** `testmainak` properly (un-quarantine) through the automatic flow.
5. **Live-site regression:** `cdn.a2zjav.com` 200/HIT/`Server: Brisk`/valid TLS on all 3, unchanged.

---

## Acceptance (definition of done)
```
- PER-ZONE DNS: server_zones == each zone's A-set; testjim=3 PoPs, testmainak=2 (BLR off); apex unchanged; idempotent
- TLS: testjim/testmainak serve *.cdn.a2zjav.com (verify 0); cdn.a2zjav.com keeps *.a2zjav.com; both auto-renew; old cert kept on failure
- ROLLOUT: new agent on all 3 (drain->deploy->verify->undrain); cdn.a2zjav.com 200/HIT throughout; rollback proven available
- TEARDOWN: delete -> vhost gone + cache purged + per-zone A-set removed + purge_jobs row survives; last-zone safe; guard intact
- DASHBOARD: per-zone PoP toggles work end-to-end; both wildcards + DNS/TLS surfaced; delete warnings; redeployed + verified
- E2E: testjim all-PoP, testmainak India-disabled routes correctly; fresh-zone create/toggle/delete fully automatic; live zone unchanged
```

## Pitfalls
1. **`server_zones` drives BOTH layers** — every assign/unassign must update vhost (config_version) AND the per-zone DNS A-set; never let them drift.
2. **Per-zone A-set must mirror the `cdn` set's Smart fields** (geo/latency/health) — a plain A set loses geo routing.
3. **Wildcard CNAME is catch-all only** — explicit per-zone A-sets are authoritative; a zone restricted off a PoP must NOT be silently re-added by the wildcard (it isn't, since explicit beats wildcard — verify).
4. **TLS additive-then-verify** — never drop the live `*.a2zjav.com`; keep old cert on renewal failure; cert is independent of PoP set.
5. **Rollout one edge at a time, rollback ready** — drain before deploy, verify before undrain, `cdn.a2zjav.com` never drops; stop on any failure.
6. **Don't weaken the empty-config guard** — teardown via explicit removal only.
7. **Bunny safety** — only `brisk:`-tagged records; never the live A-set; respect rate limits (429 backoff); key never logged.
8. **Carry forward all per-zone behavior** — CF-proxied origin, `user www-data`, WP caching, SWR+lock, HSTS, branded headers, video/Brotli — on every generated vhost.

## Next — Step 4.8 (do NOT start) — custom-domain CNAMEs + per-domain SNI certs
Customers point their own domain at a Brisk CNAME → verify ownership → auto-issue a per-domain cert (extend this lego flow from wildcards to per-host SNI). The covering-cert helper built in Part 3 is the hook. Its own prompt later.
