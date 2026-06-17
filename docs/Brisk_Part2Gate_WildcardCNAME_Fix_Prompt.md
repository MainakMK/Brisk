# Brisk CDN — Part 2 Gate Fix: land the wildcard CNAME `*.cdn.a2zjav.com` in Bunny

**For Claude Code.** Continues `docs/Brisk_Phase4_Step7_AutoOnboard_Prompt.md` Part 2. **State:** GATE 0 green (zone 6 restored/managed/cfg 8, all 3 edges; live site 200/HIT); tunnels on key auth; Part 1 auto-assign live; Part 2 reconciler code wired in and running. **Blocker:** `EnsureWildcardCNAME` runs but the record `*.cdn.a2zjav.com → cdn.a2zjav.com` is **not landing in Bunny** (confirmed absent at the authoritative NS). The error is **swallowed (best-effort)**, so the cause is hidden. This is a small, DNS-only fix — **no agent rollout, no cert work, no live-serving changes.**

> **Confirmed from Bunny docs:** wildcard records ARE supported (a `*.cdn` record synthesizes responses for any `<x>.cdn.a2zjav.com` with no explicit record). Bunny's `Name` field is the **relative** label (e.g. `cdn`), not the FQDN. CNAME is a distinct record type (numeric enum **2**: A=0, AAAA=1, **CNAME=2**, TXT=3, …). Monitoring/Smart routing apply to A/AAAA sets; a CNAME just points at the geo-routed `cdn` A-set and inherits it.

> **Prime suspects (in order):**
> 1. **The hand-rolled `internal/dns` client mis-encodes the record type** — it was built for Smart **A** records, so it may be sending the wildcard as **Type A (0)** with a hostname value (`cdn.a2zjav.com`), which Bunny rejects ("A record needs an IP"). It must send **Type CNAME (2)**.
> 2. **Name format** — sending the FQDN `*.cdn.a2zjav.com` instead of the relative `*.cdn`.
> 3. **Value format** — trailing dot / FQDN mismatch vs. what Bunny expects.

> **Safety:** read/modify only the `brisk:`-tagged wildcard; never touch the live `cdn.a2zjav.com` A-set or any non-Brisk record; API key never logged. The `*.cdn` wildcard must NOT shadow the bare `cdn` label (different names — verify).

---

## STEP 1 — Un-swallow the error (do this first; it makes the rest obvious)
In `EnsureWildcardCNAME` and the `internal/dns` client's `AddRecord`/HTTP path, add a log line that captures the **full Bunny request and response** on failure (and on success, at debug):
- request: method, URL, and the JSON body **with the API key redacted**
- response: HTTP status + raw body
Stop swallowing: return/log the error with context (`"EnsureWildcardCNAME: bunny add-record failed: status=%d body=%s"`). Rebuild + redeploy `brisk-control`.

## STEP 2 — Capture the real failure
Trigger the reconcile (restart the loop, or hit the reconcile/admin endpoint) and read the new log line.
```bash
docker compose -f brisk-control/docker-compose.yml logs --since=5m brisk-control | grep -i -A3 'wildcard\|EnsureWildcardCNAME\|add-record'
```
Record the exact status + body. That body names the cause (bad type, bad name, duplicate, etc.).

## STEP 3 — Compare against a known-good record
The reconciler already manages the `cdn` A-set successfully, so mirror its exact field formats. Fetch the live records and inspect how the working `cdn` record is stored:
```bash
# use the control plane's own DNS client/creds; do not print the API key
curl -s -H "AccessKey: $BUNNY_API_KEY" "https://api.bunny.net/dnszone/$ZONE_ID/records" \
  | jq '.[] | {Id, Type, Name, Value, Ttl}'    # find the working "cdn" A records -> note Name format ("cdn", relative) and Type
```
Confirm: working A records use **relative Name** (`cdn`) and **Type 0**. The wildcard CNAME must therefore use Name=`*.cdn`, **Type 2 (CNAME)**, Value=`cdn.a2zjav.com` (match the working records' trailing-dot convention).

## STEP 4 — Fix the client/reconciler to emit a correct CNAME
Apply whichever the captured error + comparison proves:
- **Type:** ensure the client maps CNAME → Bunny **Type 2**, not defaulting to A(0). If the hand-rolled client only ever set A/Smart fields, add proper CNAME encoding (Type 2; no IP/Smart fields; just Name + Value + Ttl).
- **Name:** relative `*.cdn` (strip the zone suffix the way the A-set path does).
- **Value:** `cdn.a2zjav.com` in Bunny's expected form (match the working records — typically no trailing dot).
- **Ttl:** match the zone's existing record TTL convention.
- Keep the `brisk:wildcard:cdn` tag/comment, create-if-missing, never-clobber, idempotent.
Rebuild + redeploy.

## STEP 5 — Verify it landed (authoritative + public)
```bash
# 1) record now exists in Bunny:
curl -s -H "AccessKey: $BUNNY_API_KEY" "https://api.bunny.net/dnszone/$ZONE_ID/records" \
  | jq '.[] | select(.Name=="*.cdn") | {Type, Name, Value, Ttl}'   # expect Type CNAME, Value cdn.a2zjav.com

# 2) resolves at the authoritative NS (any random tenant label):
dig +short verify-$(date +%s).cdn.a2zjav.com CNAME @kiki.bunny.net
dig +short verify-$(date +%s).cdn.a2zjav.com A     @kiki.bunny.net   # should chase CNAME -> the geo-routed edge IP(s)

# 3) resolves via a public resolver (allow for propagation/TTL):
dig +short testmainak.cdn.a2zjav.com @1.1.1.1

# 4) live zone unaffected — the bare label must NOT be shadowed by *.cdn:
dig +short cdn.a2zjav.com A @kiki.bunny.net    # still the edge A-set, unchanged
```

## Acceptance (Part 2 gate)
```
- Bunny API now returns the *.cdn CNAME record (Type CNAME/2, Value cdn.a2zjav.com), brisk-tagged
- a brand-new random <x>.cdn.a2zjav.com resolves (CNAME -> cdn.a2zjav.com -> geo-routed edge IPs) at kiki.bunny.net
- cdn.a2zjav.com A-set unchanged; no non-Brisk record touched
- EnsureWildcardCNAME logs success; the error is no longer swallowed (failures now surface)
- reconciler idempotent: a second run makes no duplicate and no churn
```

## Pitfalls
1. **Surface the error before guessing** — Step 1 first; the Bunny response body is the answer.
2. **CNAME is Type 2, not A(0)** — the most likely bug given the A-focused client; don't send Smart/IP fields on a CNAME.
3. **Relative Name** (`*.cdn`), not FQDN — mirror the working `cdn` record exactly.
4. **Wildcard ≠ apex** — `*.cdn` must not collide with or shadow `cdn`; verify the live zone still resolves to its A-set.
5. **Never-clobber + idempotent** — only manage the `brisk:wildcard:cdn` record; re-runs cause no duplicates.
6. API key never logged or committed.

## Next (after this lands) — Parts 3 → 6 as a dedicated live-rollout session
Part 3 (auto-TLS `*.cdn.a2zjav.com` via lego/Bunny DNS-01 + agent covering-cert selection) and Part 4 (delete-teardown + `purge_jobs` FK migration + removal signal) require a **new agent rolled to all 3 edges** (drain → deploy → verify → undrain, one box at a time, rollback ready), then Part 5 (dashboard) and Part 6 (e2e: re-onboard zone 12 + fresh-zone create/delete). Do NOT start these here — they get their own focused prompt with the full rollout runbook. Land the wildcard CNAME first so Part 6 can verify resolution end-to-end.
