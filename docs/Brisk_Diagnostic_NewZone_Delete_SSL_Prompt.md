# Brisk CDN — DIAGNOSTIC PROMPT (read-only): New-Zone Not Serving + Delete-Purge + Auto-Assign + SSL

**For Claude Code.** Context: `CLAUDE.md` + `docs/Brisk_Phase1_Build_Spec.md` + all Phase-2/3 + Phase-3.7 + Phase-4 prompts + the **3-in-1 prompt** (`Brisk_Dashboard_Delete_Cache_Prompt.md` — Logs/Security un-SOON, zone-delete purge + vhost teardown with type-the-hostname guard, Cache Settings panel) + `docs/Control_Plane_Ops.md`. **State:** control plane v18 on the laptop; 3 live edges `US-NY-prod01` (104.248.231.144), `EU-FRA-prod01` (188.245.225.172), `BLR1-01` (139.59.78.21) serving the live zone `cdn.a2zjav.com` → origin `test.mainakghosh.com` (Cloudflare-proxied). Multi-tenant routing = one nginx `server` block **per zone assigned to that edge** (Phase-4 Step 1). DNS reconciler manages **edge A records under `cdn.a2zjav.com`** (online=enabled, drained/off=disabled-but-kept, deleted=removed). Wildcard TLS `*.a2zjav.com` via lego + Bunny DNS-01, centrally managed and fanned to edges.

> **⛔ THIS IS A DIAGNOSTIC-ONLY PROMPT. DO NOT FIX ANYTHING.** Read code, query the DB, inspect runtime state, run **safe read-only** `curl`/`dig`/`openssl`/`psql` checks. **Change nothing:** no DB writes, no DNS create/update/delete, no cert issuance, no `nginx -s reload`, no zone create/delete, no agent restarts. **`cdn.a2zjav.com` must stay up and untouched.** Produce the structured report at the bottom and stop. The fix prompt comes *after* the report.

---

## What the user is seeing (the 4 issues to investigate)
The user just **deleted an old test zone** and **created a new test zone** (≈ "test minor"; the control plane auto-generated its CDN hostname). Reported symptoms:

1. **Delete didn't purge.** After deleting the old zone, content is **still being served / still cached** — the user specifically says the cache hasn't cleared. The 3-in-1 Part 2 was supposed to make delete fan out a whole-zone purge + remove the vhost across all PoPs in ~20–30s.
2. **New zone's CDN hostname doesn't work.** The auto-generated `cdn_hostname` for the new zone does not serve.
3. **Server assignment isn't automatic.** The user expects a new zone to be **auto-assigned to all available edges**; right now it seems they must assign servers manually (or it isn't assigned at all).
4. **SSL doesn't work** on the new zone's hostname (likely downstream of #2/#3, but verify independently — there may be a real cert-coverage gap).

**Likely chain to confirm or refute** (hypotheses — prove with evidence, don't assume): #3 (no auto-assign) → new zone has **no `server` block on any edge** → #2 (hostname returns default_server/404 or NXDOMAIN) → #4 (no vhost to terminate TLS, and/or the wildcard `*.a2zjav.com` does **not** cover a 2nd-level hostname like `x.cdn.a2zjav.com`). #1 is separate: confirm whether stale content is **Brisk edge cache**, **Cloudflare**, or **browser**, and whether the delete actually published a NATS purge + removed the vhost.

---

## SECTION A — Ground truth inventory (run first; everything else references this)
Capture the real state. **Report exact table/column names, IDs, and values you find** (don't paraphrase).

1. **Zones.** From the control-plane DB (the Dockerized Postgres/Timescale on the laptop), dump the zones table for: the **live** zone (`cdn.a2zjav.com`), the **new** zone (the "test minor" one), and any **recently deleted** zone if a soft-delete/tombstone exists. For each: `id`, `cdn_hostname`, `origin_url`, upstream-host setting, `status`, `config_version`, `account_id`, `created_at`, `deleted_at` (if soft-delete). Example (adapt names — first inspect the schema):
   ```bash
   # find the DB container + creds from compose/.env, then:
   psql ... -c "\d zones"          # show the real schema first
   psql ... -c "SELECT id, cdn_hostname, origin_url, status, config_version, account_id, created_at FROM zones ORDER BY created_at DESC;"
   ```
   **Report the new zone's exact `cdn_hostname` and how many labels deep it is** (e.g. `testminor.cdn.a2zjav.com` = under `cdn.a2zjav.com`, vs `testminor.a2zjav.com` = under apex). This single fact drives both the DNS and the SSL findings.

2. **Servers/edges.** Dump `servers`: `id`, name, region, IP, `status`, drained flag, health/rotation state. Confirm the 3 known edges are online/in-rotation.

3. **Zone↔server assignment model.** Find how zones map to edges. Is there a join table (`zone_servers` / `zone_assignments` / similar)? Or a column on `zones`? Or are all zones implicitly served by all edges? Show the real schema + the rows for the new zone:
   ```bash
   psql ... -c "\dt"                                   # list tables; find the assignment table
   psql ... -c "\d <assignment_table>"
   psql ... -c "SELECT * FROM <assignment_table> WHERE zone_id = '<new_zone_id>';"
   ```
   **Report: is the new zone assigned to any edge? To how many? Which?**

4. **Wildcard cert SANs (ground truth for SSL).** On **one** edge, read the managed cert and print its Subject + SANs (read-only):
   ```bash
   # certs live at /etc/brisk/tls/<domain>/ per conventions
   ssh <edge> "ls -la /etc/brisk/tls/ && for f in /etc/brisk/tls/*/fullchain.pem /etc/brisk/tls/*/cert.pem; do echo \"== \$f ==\"; openssl x509 -in \$f -noout -subject -ext subjectAltName 2>/dev/null; done"
   ```
   **Report the exact SAN list** (e.g. `DNS:a2zjav.com, DNS:*.a2zjav.com`). Note: `*.a2zjav.com` covers `cdn.a2zjav.com` but **NOT** `anything.cdn.a2zjav.com` (wildcards match exactly one label). Flag if the new hostname is deeper than the wildcard covers.

5. **Bunny DNS records.** List the live DNS records via the control-plane DNS API (read-only) and/or Bunny `GET`:
   ```bash
   curl -s localhost:18080/api/v1/dns/records   # adapt port/path; the internal DNS list endpoint
   ```
   **Report: which names have records** — specifically, is there an A/CNAME record set (or wildcard) that resolves the **new zone's hostname**? Or only records for `cdn.a2zjav.com` (the edge set)?

---

## SECTION B — Issue #3: is server assignment automatic on zone create? (do this before #2 — it's upstream)
1. **Trace the zone-create handler** in `brisk-control` (the `POST /api/v1/zones` path). Read the code. Answer precisely:
   - On create, does it **auto-populate the assignment table with all online edges**? Or does it leave the zone unassigned until a manual assign call?
   - Is there *any* auto-assign logic anywhere (reconciler, hook, default)? `grep` for it:
     ```bash
     grep -rin "assign" --include=*.go internal/ cmd/ | grep -iv test
     grep -rin "zone_servers\|zone_assign\|AssignZone\|all.*edges\|online.*servers" --include=*.go .
     ```
2. **Confirm the consequence:** with the new zone unassigned, does the **pull-config** payload for each edge include the new zone? Check what `GET /pull-config` (or equivalent) returns for one edge — does the new zone's `server` block data appear?
   ```bash
   # find the pull-config endpoint + how the agent authenticates, then fetch what an edge would get
   curl -s <pull-config-url-for-an-edge>   # look for the new zone in the returned zones list
   ```
3. **Report:** Is auto-assign **implemented but broken**, or **never implemented** (a missing feature)? Quote the relevant handler lines. State plainly whether the new zone is currently in any edge's config payload.

---

## SECTION C — Issue #2: why the new zone's CDN hostname doesn't serve
Walk the request path end-to-end for the new hostname and report **where it breaks**.

1. **DNS resolution.** Does the new hostname resolve at all?
   ```bash
   dig +short <new_cdn_hostname>
   dig +short <new_cdn_hostname> @1.1.1.1
   ```
   Expected if working: the 3 edge IPs (or a CNAME to the edge set). **Report NXDOMAIN vs resolves-to-edges vs resolves-elsewhere.** If NXDOMAIN → DNS for new-zone hostnames isn't being created (tie back to A.5 / the reconciler scope).

2. **Edge vhost presence.** On **each** edge, is there an nginx `server` block whose `server_name` is the new hostname? (read-only — do **not** reload)
   ```bash
   for e in US-NY EU-FRA BLR1; do echo "== $e =="; ssh $e "grep -rl '<new_cdn_hostname>' /etc/nginx/ /etc/brisk/ 2>/dev/null; nginx -T 2>/dev/null | grep -A2 'server_name <new_cdn_hostname>'"; done
   ```
   **Report: does any edge have a vhost for the new hostname?** (Expected per the #3 hypothesis: none.)

3. **HTTP/HTTPS probe from outside.** Hit the hostname directly against each edge IP (bypassing DNS) to see the real response:
   ```bash
   for ip in 104.248.231.144 188.245.225.172 139.59.78.21; do
     echo "== $ip =="
     curl -ksI --resolve <new_cdn_hostname>:443:$ip https://<new_cdn_hostname>/ | head -20
   done
   ```
   Classify the result per edge: **TLS handshake fails** (cert/SNI issue → Section D), **default_server 404/444** (no vhost → #3), **502/504** (vhost exists but origin/upstream-host wrong), or **200** (works). **Report the exact status + `Server:` header + any `X-Brisk-*` headers.**

4. **Report the break point** for #2: DNS, vhost/assignment, TLS, or origin — with the evidence above.

---

## SECTION D — Issue #4: SSL on the new hostname
1. **Coverage check.** Compare the new hostname (A.1) against the cert SANs (A.4). Does `*.a2zjav.com` actually cover it? (It does **not** if the hostname has an extra label under `cdn`, e.g. `x.cdn.a2zjav.com`.) **State the verdict explicitly.**
2. **Live TLS probe** (read-only) against one edge IP with SNI = the new hostname:
   ```bash
   echo | openssl s_client -connect 104.248.231.144:443 -servername <new_cdn_hostname> 2>/dev/null | openssl x509 -noout -subject -ext subjectAltName
   ```
   Report which cert the edge serves for that SNI and whether the name validates (`Verify return code`). If the edge has no vhost for the SNI, note what cert (if any) it falls back to.
3. **Report root cause for #4:** is it (a) no vhost so TLS never gets configured for the name, (b) wildcard depth mismatch (`*.a2zjav.com` can't cover `*.cdn.a2zjav.com`), (c) the new hostname needs a per-host/SNI cert (Phase-4 Step-2 territory, not yet built), or some combination. Be specific.

---

## SECTION E — Issue #1: did zone-delete actually purge + tear down?
1. **Trace the delete handler** (`DELETE /api/v1/zones/{id}` in `brisk-control`). Confirm, from the code, that on delete it: (a) publishes a **whole-zone purge** over NATS, (b) bumps `config_version` / signals removal so agents drop the vhost, (c) writes an audit row. Quote the lines. If any of the three is missing or guarded out, that's the bug.
   ```bash
   grep -rin "DELETE\|DeleteZone\|purge.*zone\|PublishPurge\|nats" --include=*.go internal/ cmd/ | grep -i zone
   ```
2. **Audit trail.** Did the recent delete actually run the purge path?
   ```bash
   psql ... -c "SELECT * FROM <audit_table> ORDER BY created_at DESC LIMIT 20;"
   ```
   Look for the delete + purge entries for the old zone.
3. **NATS.** Was a purge message published/consumed? Check the JetStream stream the purge uses (read-only — list/inspect, don't purge the stream):
   ```bash
   # adapt to the real stream/subject names from the purge code
   nats stream info <PURGE_STREAM> 2>/dev/null
   nats stream view <PURGE_STREAM> --last 20 2>/dev/null   # recent messages, if available
   ```
4. **Is content actually still being served, and from where?** Identify the **exact hostname** the user still sees content on (the deleted zone's hostname? or `cdn.a2zjav.com` itself?). Then probe each edge and read the cache-status header to locate the cache layer:
   ```bash
   for ip in 104.248.231.144 188.245.225.172 139.59.78.21; do
     echo "== $ip =="
     curl -ksI --resolve <still-serving-hostname>:443:$ip https://<still-serving-hostname>/ | egrep -i 'server:|x-brisk-cache|x-brisk-edge|cf-cache-status|age:'
   done
   ```
   - `X-Brisk-Cache: HIT` + `Server: Brisk` → **Brisk edge cache** didn't purge (real bug — the delete purge didn't land here).
   - `cf-cache-status: HIT` / Cloudflare headers → it's **Cloudflare** in front, not Brisk (the user may be looking at the wrong layer).
   - No edge headers, but browser shows it → **browser cache** (hard-refresh test).
5. **Vhost teardown.** On each edge, is the deleted zone's `server` block **gone** from the live nginx config? (read-only)
   ```bash
   for e in US-NY EU-FRA BLR1; do echo "== $e =="; ssh $e "nginx -T 2>/dev/null | grep -c 'server_name <deleted_hostname>'"; done   # expect 0 if teardown worked
   ```
6. **Report:** which of {purge published, purge consumed per edge, vhost removed per edge, edge cache files deleted} happened and which didn't — and the cache layer the user is actually seeing.

---

## SECTION F — Synthesis (write this last)
For **each** of the 4 issues, report: **(a) root cause** (with the specific evidence/line/row that proves it), **(b) whether it's a bug vs a missing feature**, and **(c) the smallest fix** you'd recommend — **but do not implement it.** Then give a one-paragraph **fix-order recommendation** (e.g. auto-assign first since #2/#4 depend on it; DNS-for-new-hostnames; wildcard-depth/SNI cert decision; delete-purge fix).

---

## Hard safety rules (repeat)
- **Read-only only.** No DB writes, no DNS changes, no cert issuance, no `nginx -s reload`, no zone create/delete, no agent/service restarts, no NATS purge/publish.
- **`cdn.a2zjav.com` stays up and byte-for-byte untouched** throughout.
- Secrets (Bunny key, DB creds, admin tokens) **never printed** in the report — refer to them by name only.
- If a step needs a value you don't have (e.g. the new zone id/hostname), get it from Section A first; if still unknown, say so rather than guessing.

---

## REPORT TEMPLATE (fill this in and send back — keep it tight)
```
== A. INVENTORY ==
New zone: id=…  cdn_hostname=…  labels-under=…(cdn|apex)  origin_url=…  status=…  config_version=…
Old/deleted zone: id=…  hostname=…  deleted_at=…
Servers: NY=…(status)  FRA=…  BLR=…
Assignment model: <table/column or "implicit-all">  | new zone assigned to: <list / NONE>
Cert SANs (one edge): …
DNS records present for new hostname?: YES(→…)/NO  | what resolves cdn.a2zjav.com: …

== B. ISSUE 3 (auto-assign) ==
Auto-assign on create: implemented? Y/N  | broken or never-built: …
New zone in any edge's pull-config payload?: Y/N
Evidence (handler lines): …

== C. ISSUE 2 (new host not serving) ==
dig <new host>: …(NXDOMAIN/edges/other)
Vhost on edges: NY=…/FRA=…/BLR=…
curl per edge IP (status / Server / X-Brisk-*): …
Break point: DNS | vhost(assign) | TLS | origin

== D. ISSUE 4 (SSL) ==
Wildcard covers new host? Y/N (why)
openssl SNI probe result: …
Root cause: …

== E. ISSUE 1 (delete purge) ==
Delete handler does: purge-publish=Y/N  config bump/removal=Y/N  audit=Y/N
Audit rows found: …
NATS purge published/consumed: …
Still-serving hostname=…  cache layer = Brisk-edge | Cloudflare | browser  (header evidence: …)
Vhost removed per edge: NY=…/FRA=…/BLR=…

== F. SYNTHESIS ==
#1 root: …  (bug/feature)  fix: …
#2 root: …  (bug/feature)  fix: …
#3 root: …  (bug/feature)  fix: …
#4 root: …  (bug/feature)  fix: …
Recommended fix order: …
```

## Next (do NOT start)
After the user pastes this report back, write the **fix prompt** (auto-assign on zone-create → new-hostname DNS → SSL coverage decision (wildcard `*.cdn.a2zjav.com` vs per-host SNI cert) → delete-purge/teardown fix), in the normal one-step-at-a-time format. **Diagnose and report only for now.**
