# Brisk CDN — Phase 3.7 / Step 2 Build Prompt (Live Fan-out + Agent-Owned TLS + Edge Rebuild)

**For Claude Code.** Context: `CLAUDE.md` + `docs/Brisk_Phase1_Build_Spec.md` + all Phase‑2/3 prompts + `docs/Control_Plane_Ops.md` + `docs/Brisk_Phase3_Runbook.md` + `dashboard-reference/`. **Phase 3.7 Step 1 is complete:** all 3 live edges (`US-NY-prod01` 104.248.231.144, `EU-FRA-prod01` 188.245.225.172, `BLR1-01` 139.59.78.21) run the **real `brisk-agent`** against the **laptop control plane over Dockerized autossh reverse tunnels** (control API `:18080`, NATS `:14222`), with **real heartbeats** (keepalive hack removed), **live config pull** (verified: a `brotli_level` change bumped `config_version` 1→2, all 3 re‑pulled in ~25s), the **reconciler all‑offline guard** (the NXDOMAIN outage fixed + can't recur), and resilience proven (control plane down → edges keep serving → reconnect <30s). `cdn.a2zjav.com` serves the live WP site (origin `test.mainakghosh.com`, Cloudflare‑proxied) at 200 HIT from the nearest PoP.

> **Read `CLAUDE.md`, `docs/Control_Plane_Ops.md`, and the Phase‑2 Step‑4 (stats) + Step‑5 (NATS purge) + Phase‑1 (Brotli/headers/video) prompts first.** This is **Step 2 of 3 in Phase 3.7**. Test against the **live fleet**. Roll edge changes **one box at a time** with rollback ready; keep `cdn.a2zjav.com` up. Pass the acceptance tests, stop before Step 3.

## Step 3.7.2 goal (one line)
Finish productionizing the edges: **rebuild them on the nginx.org build** to restore **`Server: Brisk` + Brotli + the video slice module** (which the Ubuntu distro build couldn't run), **verify purge + stats fan‑out work on the real 3‑edge fleet**, and **move wildcard DNS‑01 TLS issuance/renewal ownership into the agent** (replacing the hand‑set acme.sh).

## ⚠️ Live fleet — one edge at a time, rollback ready
Every change here touches live edges serving real traffic. Roll the nginx rebuild + TLS migration **one edge at a time** (suggest US‑NY → EU‑FRA → BLR1‑01), verify each serves identically before the next, keep the per‑edge `nginx.conf.brisk-bak` (and the working acme.sh certs) as rollback. Never leave `cdn.a2zjav.com` with a broken edge.

---

## Gap analysis vs the Step‑1 report (what Step 2 must absorb — read first)
Step 1's report surfaced carry‑overs beyond the original "fan‑out + TLS" plan. Step 2 closes **all** of these:
1. **`Server: Brisk`, Brotli, and the video slice module are currently OFF on the live edges.** Ubuntu's distro nginx is linked with `-Bsymbolic-functions`, which **segfaults the `headers-more` module**, so Step 1 fell back to core `add_header` (Server stayed `nginx`) and these modules were left out. **Fix = rebuild on the official nginx.org build / a clean build that runs the dynamic modules** → restores `Server: Brisk`, Brotli, and slice video. (Part 1.)
2. **TLS is still hand‑set (acme.sh DNS‑01 wildcard).** Move issuance/renewal **into the agent**. (Part 3.)
3. **Purge + stats fan‑out were only proven on local stand‑ins** — must now be verified on the **real 3‑edge fleet** over the tunnels. (Part 2.)
4. **The Cloudflare‑proxied origin handling** (Step 1's `resolver` + variable `proxy_pass` fix for `test.mainakghosh.com`) and the **`user www-data;` cache‑ownership fix** must **survive the rebuild** — carry them into the new template. (Part 1.)

## Part 1 — Edge rebuild on nginx.org (restore Server: Brisk + Brotli + video)
- Rebuild nginx on each edge from the **official nginx.org build** (the Phase‑1 spec's intended build) instead of the Ubuntu distro package, so the dynamic modules load **without the `-Bsymbolic-functions` segfault**: **`headers-more`** (→ `Server: Brisk`, `X-Brisk-*`), **`ngx_brotli`** (comp 5 dynamic / 11 static), and the **slice module** (1 MB video slicing + request coalescing) — all per the Phase‑1 build spec.
- The agent's `server.tmpl` re‑enables these (drop the Step‑1 `add_header`/no‑brotli/no‑slice fallback). **Carry forward** the Step‑1 fixes: `user www-data;` (cache ownership), the **Cloudflare‑proxied origin** handling (`resolver` + variable `proxy_pass`, IPv6‑aware), wildcard TLS+HSTS+80→443, static 30d cookie‑strip, HTML 10m logged‑in bypass, stale‑while‑revalidate + cache‑lock, branded headers, origin `test.mainakghosh.com`.
- **Pin the build + ABI‑lock the modules** to the nginx version (rebuild on upgrade — same discipline as Phase 1). `bootstrap`/agent owns the version stamp.
- Verify per edge: `nginx -t`, reload, then confirm **`Server: Brisk`**, **`Content-Encoding: br`** on compressible assets, **video slices** (206 + `X-Brisk-Cache`), the 150KB test image still HIT, TLS intact — **identical or better** than before, site never down.

## Part 2 — Verify purge + stats fan‑out on the REAL fleet
Now that real agents run over the tunnels, prove the two fan‑out paths the Step‑1 report left unverified on live edges:
- **Purge fan‑out (NATS, Phase‑2 Step 5):** issue a purge (URL / prefix / zone) from the dashboard/API → confirm **all 3 edges** drop the object (each independently goes MISS → re‑fetch). Test sliced‑video prefix purge clears slices on all 3. **Durability:** drop one edge's tunnel, purge, restore the tunnel → that edge applies the missed purge on reconnect (JetStream replay). Measure fan‑out latency over the tunnels.
- **Stats fan‑out (Phase‑2 Step 4):** confirm **all 3 edges ship stats** to the laptop control plane over the tunnels → Overview + Analytics show **per‑PoP** and **aggregate** data for the real fleet (the "All PoPs" merge reflects 3 real edges, not stand‑ins). Verify hit‑ratio/bandwidth/req‑s are real per edge.
- Confirm none of this regresses heartbeats/config‑pull and that tunnel drops degrade gracefully (stats buffer + flush on reconnect, per Phase‑2 Step 4).

## Part 3 — Move wildcard TLS into the agent (DNS‑01 via lego)
Replace the hand‑set **acme.sh** with **agent‑owned** issuance/renewal of the wildcard `*.a2zjav.com` (+ apex) cert using **`lego`** (`github.com/go-acme/lego/v4`) — the standard Go ACME library: single static binary / importable Go module, **MIT‑licensed** (clean for a commercial product), 90+ DNS providers, full issue/renew/revoke, **ARI** support for CA‑suggested renewal timing, and **DNS‑01 is the only challenge that does wildcards**.
- **DNS‑01 via the Bunny provider:** lego has a built‑in **Bunny** DNS provider (uses the Bunny API key from the existing `internal/dns` config) — it creates the `_acme-challenge` TXT record, waits for propagation, the CA validates, then cleans up. Reuse the **same Bunny key** Brisk already holds (keep it out of logs/repo).
- **Where it runs (decide + document):** cleanest is the **agent on each edge** owns its cert (renews locally, reloads nginx on rotation) **OR** the **control plane** issues centrally and pushes certs to edges via the config‑pull channel. Given multi‑tenant + custom domains are coming in Phase 4, **prefer the control‑plane‑issues‑centrally model** (one place owns ACME, edges receive certs) — but if per‑edge is simpler now, do that and note the Phase‑4 migration. Document the choice.
- **Renewal:** automatic, well before expiry (use ARI / renew at ~30 days remaining), with retry/backoff and **zero‑downtime reload** (nginx `reload`, not restart). On failure, **keep the existing cert** and alarm — never drop TLS.
- **Migration safety:** issue the new agent‑owned cert **alongside** the working acme.sh cert, switch nginx to it, verify TLS, **then** retire acme.sh. If issuance fails, the acme.sh cert stays in place (rollback).
- Surface cert status (issuer, expiry, last‑renewed) in the dashboard DNS/Servers area for visibility.

## Part 4 — Docs
Update `docs/Control_Plane_Ops.md`: the nginx.org build + module ABI‑lock, the verified fan‑out behavior on the real fleet, and the agent‑owned TLS model (issue/renew flow, where certs live, renewal cadence, rollback). Note the Phase‑4 hook: this TLS machinery will extend to **per‑customer custom‑domain certs**.

---

## Acceptance tests (Step 3.7.2 definition of done — live fleet)
```bash
# EDGE REBUILD (one box at a time)
# 1) Each edge on the nginx.org build: Server: Brisk restored; headers-more loads without segfault (no worker crashes in logs)
curl -sI https://cdn.a2zjav.com | grep -i '^server:'            # Server: Brisk
# 2) Brotli on: compressible asset returns Content-Encoding: br
curl -sI -H 'Accept-Encoding: br' https://cdn.a2zjav.com/<asset.css> | grep -i content-encoding
# 3) Video slice module on: ranged video request -> 206, slices cached (X-Brisk-Cache), coalesced
# 4) Carry-forward fixes intact: 150KB image HIT, Cloudflare-proxied origin works, no 500s (www-data), TLS valid
# 5) Site identical-or-better throughout; per-edge rollback (nginx.conf.brisk-bak) ready

# PURGE + STATS FAN-OUT (real fleet)
# 6) Purge from dashboard -> ALL 3 edges go MISS (verify each independently); sliced video cleared on all 3
# 7) Purge durability: drop one edge's tunnel -> purge -> restore tunnel -> that edge applies missed purge (JetStream replay)
# 8) Stats fan-out: all 3 edges ship stats -> Overview/Analytics show real per-PoP + aggregate (3 real edges)

# AGENT-OWNED TLS (lego DNS-01)
# 9) Agent/control-plane issues wildcard *.a2zjav.com via lego + Bunny DNS-01 (TXT created -> validated -> cleaned up)
#    -> nginx switches to the new cert -> TLS valid -> acme.sh retired (only after success)
# 10) Renewal path works (force/dry-run a renew): new cert obtained, zero-downtime reload, expiry updated; on failure old cert kept
# 11) Bunny API key never in logs/repo; cert status (issuer/expiry) shown in dashboard
# 12) Live-site safety: cdn.a2zjav.com served correctly through the entire step
```
**Done when:** all 3 live edges run the **nginx.org build** with **`Server: Brisk` + Brotli + video slicing restored** (carrying forward the Step‑1 origin/cache fixes), **purge and stats fan‑out are verified working across the real 3‑edge fleet** (incl. JetStream purge replay after a tunnel drop), and **wildcard TLS is agent/control‑plane‑owned via lego DNS‑01** with automatic zero‑downtime renewal and acme.sh retired — all while `cdn.a2zjav.com` stays up, rolled out one edge at a time with rollback ready.

---

## Pitfalls (do not skip)
1. **nginx.org build, not the Ubuntu distro** — the distro's `-Bsymbolic-functions` segfaults `headers-more`; the clean build is what gets `Server: Brisk` + dynamic modules working. ABI‑lock modules to the version.
2. **Carry forward Step‑1 fixes** — `user www-data;`, Cloudflare‑proxied origin (`resolver` + variable `proxy_pass`, IPv6), wildcard TLS/HSTS/redirect, WP caching rules. The rebuild must not regress them.
3. **One edge at a time + rollback** — keep `nginx.conf.brisk-bak` and the working acme.sh cert per edge; verify each before the next; never break the live set.
4. **TLS migration is additive‑then‑switch** — issue the new lego cert alongside acme.sh, switch + verify, *then* retire acme.sh; on issuance failure keep the old cert (never drop TLS).
5. **Renewal must be automatic + zero‑downtime** — renew well before expiry (ARI/~30d), nginx `reload` not restart, retry/backoff, alarm on failure.
6. **Reuse the Bunny key safely** — same key as `internal/dns`; never logged or committed; lego's Bunny provider creates/cleans the `_acme-challenge` TXT.
7. **Verify fan‑out per edge, not globally** — purge/stats must be confirmed on each of the 3 edges independently, over the tunnels; test the tunnel‑drop replay case.
8. **Don't expose the control plane publicly** — still tunnel‑only; admin auth is Step 3.
9. **Scope** — edge rebuild + fan‑out verification + agent‑owned TLS only. Admin auth + laptop→public cutover docs = Step 3. Multi‑tenant host‑based origin routing + custom domains = Phase 4.

## Next — Step 3.7.3 (do NOT start) — admin auth + deploy‑readiness (closes Phase 3.7)
Wire the dashboard/control‑plane `authHeader()` seam + admin tokens (mandatory before any public exposure), confirm the control plane is never openly bound, and document the **one‑step laptop→public‑VPS cutover** (the 2‑URL change + bring‑up). After Step 3.7.3, Phase 3.7 is complete and Phase 4 (multi‑tenant routing + custom domains first, then origin shield / WAF / Lua) begins. Wait for the user's go‑ahead and a Step 3.7.3 prompt.
