# Brisk CDN — Phase 4 / Step 5 Build Prompt (Lua Edge Logic + Custom Cache-Rule Enforcement)

**For Claude Code.** Context: `CLAUDE.md` + `docs/Brisk_Phase1_Build_Spec.md` + all Phase‑2/3/3.7 prompts + the Phase‑4 Step‑1/2/3/4 prompts + `docs/Control_Plane_Ops.md` + `dashboard-reference/`. **Phase 4 Steps 1–4 are complete:** multi‑tenant host routing (one `server` block per zone, per‑zone origin), custom domains + per‑domain auto‑TLS (SNI), per‑zone origin shield, and per‑zone WAF + rate limiting (in‑agent **OWASP Coraza**, pure‑Go, CRS v4 + nginx‑native `limit_req`, via an `auth_request`/loopback `/inspect` hook). Edges run **nginx.org 1.30.2** with dynamic modules `headers-more` + `ngx_brotli` + slice; `brisk-agent`/`brisk-control` are **Go**; config reaches edges via **config‑pull + `config_version`**; `$host` cache isolation + branded `X-Brisk-*` headers throughout. **Long‑standing backlog item:** the dashboard has let users author **custom cache rules** since Phase‑2 Step 6.3 (priority‑ordered condition→action: path/extension/regex → override‑TTL / bypass / force‑download / redirect), and the agent **carries them in pulled config — but the fixed nginx template doesn't enforce them at the edge yet.** This step fixes that.

> **Read `CLAUDE.md`, the Phase‑2 Step‑3 (pull‑config) + Step‑6.3 (cache rules) prompts, the Phase‑3.7 Step‑2 (nginx.org build/modules) prompt, and the Phase‑4 Step‑4 (WAF integration) prompt first.** This is **Step 5 of Phase 4 — programmable edge logic.** Test locally in Docker. Pass the acceptance tests, stop before Step 6 (hardening + cleanup).

## Step 4.5 goal (one line)
Add a **Lua programmable‑edge layer** to the edges and use it to **enforce the per‑zone custom cache rules** the dashboard has stored since Phase 2 (override‑TTL / bypass / force‑download / redirect), plus **request/response header transforms** — per zone, driven by pulled config, with no regressions.

## ✅ Test locally in Docker
`brisk-control` + edges + an origin. Author cache rules + header transforms per zone, prove they're enforced at the edge (TTL changed, cache bypassed, redirect applied, headers added/removed), and that other zones + existing behavior are unaffected. No VPS needed.

---

## How Lua-in-Nginx works (build to this — researched)
The **`lua-nginx-module`** (the OpenResty project's module) embeds **LuaJIT** into Nginx, turning it from static‑config proxy into a **programmable platform**: custom logic runs *inside Nginx's event loop* (non‑blocking, high‑concurrency, near‑C speed via LuaJIT). It hooks Nginx's **request phases** so code runs at exactly the right moment:
- **`rewrite_by_lua` / `access_by_lua`** (early): inspect/modify the request, change routing, set variables, decide cache behavior, reject requests — runs *before* the cache lookup/upstream.
- **`header_filter_by_lua`** (response headers): add/remove/rewrite response headers.
- **`body_filter_by_lua`** (response body): transform the body if needed (use sparingly — expensive).
- **`balancer_by_lua`** (upstream selection) for dynamic routing.
Best practice: keep Lua in **separate `.lua` files** (not inline in nginx.conf), use **`lua_shared_dict`** for cross‑worker state, and keep logic shallow/non‑blocking (cosockets if any network calls — but avoid network calls in the hot path).

### The build decision (important — you're on nginx.org, not OpenResty)
Your edges run the **nginx.org build** with hand‑built dynamic modules. **OpenResty is a *different* Nginx distribution** (Nginx + LuaJIT + Lua libs bundled). Two clean options — **evaluate and pick, document why**:
- **(A) Add `lua-nginx-module` (+ `lua-resty-core`, LuaJIT) as a dynamic module to the existing nginx.org build** — keeps your current nginx.org + `headers-more`/`brotli`/`slice`/Coraza stack intact, just adds Lua. Preferred if it builds cleanly and ABI‑locks like your other modules.
- **(B) Switch the edge to OpenResty** — batteries‑included Lua, but you'd re‑validate that `headers-more`/`brotli`/`slice` + the Coraza hook + managed‑TLS + multi‑tenant template all still work on the OpenResty build.
Pick **(A) if the dynamic module builds cleanly** (least disruption to everything you've built); fall back to **(B)** only if needed. ABI‑lock to the nginx version like the other modules; roll out **one edge at a time** with rollback, live zone byte‑identical.

## Part 1 — Cache-rule enforcement (close the backlog item)
The `cache_rules` already exist per zone (priority‑ordered `match (path_prefix|extension|regex) → action (override_cache_ttl|bypass_cache|force_download|redirect) [+ value]`) and are pulled by the agent. Enforce them at the edge via Lua, in **priority order, first match wins** (mirroring how the dashboard presents them):
- **override_cache_ttl** → set the effective cache TTL for matching requests (e.g. via `ngx.var` controlling `proxy_cache_valid` behavior / a per‑request TTL variable the cache respects). Honor the existing Phase‑1 defaults when no rule matches.
- **bypass_cache** → skip cache for matching requests (set `proxy_cache_bypass`/`no_cache` semantics) → always fetch fresh.
- **force_download** → add `Content-Disposition: attachment` on matching responses (header_filter phase).
- **redirect** → return a 301/302 to the target for matching requests (access/rewrite phase, before upstream).
- Rules are **per zone** (read this zone's rules from pulled config), **ordered by priority**, **first match wins**, and must compose correctly with the Phase‑1 built‑in caching (video slice, static/HTML defaults) — a rule overrides, absence falls back to defaults.
- Make the rule data available to Lua efficiently: render the zone's rules into a form Lua reads (a per‑zone Lua table / JSON in a config file / `lua_shared_dict`), refreshed on config pull (no per‑request disk reads; reload on `config_version` change).

## Part 2 — Request/response header transforms (new per-zone capability)
A general **header‑transform** capability per zone (the programmable‑edge selling point), authored in the dashboard, enforced in Lua:
- **Request headers** (rewrite phase): add/remove/override headers sent upstream (e.g. add a custom header, strip a header).
- **Response headers** (header_filter phase): add/remove/override headers sent to the client (e.g. security headers, custom `X-` headers, CORS tweaks) — coexisting with the branded `X-Brisk-*` and `headers-more`.
- Per‑zone, ordered, condition‑optional (apply always or on a path/method match).
- Keep it **safe**: a deny‑list of headers Brisk manages itself (don't let a tenant clobber `X-Brisk-*`, break TLS/HSTS, or spoof internal headers); validate inputs.

## Part 3 — The edge Lua framework (clean + safe)
- A small, well‑structured **Lua library** the agent ships to edges: `cache_rules.lua`, `header_transforms.lua`, a `dispatch.lua` that runs per zone in the right phases. Loaded once (`init_by_lua`), per‑request logic shallow.
- **Per‑zone config → Lua:** the agent renders each zone's rules/transforms into data Lua reads (refreshed on config pull); the `server` block wires the `*_by_lua_file` hooks. Multi‑tenant isolation: a zone only ever sees its own rules.
- **Performance:** LuaJIT is fast, but keep the hot path lean — no blocking I/O, no per‑request network calls, no heavy regex on every request (precompile/anchor). Don't add measurable latency to cache HITs.
- **Safety / fail‑open:** a Lua error must **not** take the request/zone down — wrap in `pcall`, on error **fall back to default behavior** (serve normally) and log. A broken rule shouldn't blackhole a tenant.
- **No regressions:** Coraza WAF hook, origin shield routing, custom‑domain SNI TLS, slice video, Brotli, `default_server` health probe, `$host` cache key — all must keep working with Lua in the pipeline. (Mind phase ordering: WAF/access checks vs cache‑rule Lua vs proxy.)

## Part 4 — Dashboard (wire the existing + new editors to real enforcement)
- **Cache Rules** (already in the Zones UI from Phase‑2 Step 6.3): now that they're **actually enforced**, update the UI copy — drop the "stored but not yet enforced at edge" honesty note; show that rules now apply at the edge (~poll‑interval propagation). Keep the priority‑ordered editor.
- **Header Transforms:** a new per‑zone editor (request/response, add/remove/override, optional condition), with the managed‑header deny‑list enforced in the UI too.
- Honest hints: ~15s propagation (config pull); first‑match‑wins ordering; managed headers can't be overridden.
- Role‑aware (customer portal later exposes a tenant's own rules/transforms).

## Part 5 — Safety + ops
- Live‑site safety: rendering the live zones with the Lua layer must keep them **byte‑identical in behavior** (their existing caching unchanged unless a rule says otherwise); roll out one edge at a time, rollback ready. `cdn.a2zjav.com` unaffected.
- A cache‑rule/transform change is a `config_version` bump → edges re‑pull → reload (zero‑downtime). 
- Document in `docs/Control_Plane_Ops.md`: the build choice (lua‑module vs OpenResty) + ABI lock, the Lua framework + phase ordering (WAF → cache‑rule Lua → cache → upstream → header transforms), the fail‑open policy, the managed‑header deny‑list, and the Phase‑5 hook (customer portal exposes these per tenant).

---

## Acceptance tests (Step 4.5 definition of done — local Docker)
```bash
docker compose up --build -d        # control + edge(s) + origin
# 1) Lua layer active on the nginx.org build (module loads; nginx -t clean; headers-more/brotli/slice/Coraza all still work)
# 2) override_cache_ttl: a rule (extension css -> TTL 30d) -> matching responses get the longer TTL; non-matching unaffected
# 3) bypass_cache: a rule (path_prefix /api/ -> bypass) -> /api/ always MISS/fresh (X-Brisk-Cache reflects bypass); other paths cache
# 4) force_download: a rule (extension pdf -> force_download) -> matching responses carry Content-Disposition: attachment
# 5) redirect: a rule (path /old -> redirect /new 301) -> 301 to /new before upstream
# 6) Priority/first-match: overlapping rules apply in priority order, first match wins (matches dashboard order)
# 7) Header transforms: add a request header upstream + add/remove a response header to the client; managed X-Brisk-*/HSTS can't be clobbered (deny-list)
# 8) Per-zone isolation: zone A's rules/transforms don't affect zone B
# 9) Fail-open: a deliberately broken rule -> request still served normally (pcall fallback) + logged; zone not blackholed
# 10) No regressions + perf: cache HIT latency not measurably worse; WAF/shield/SNI-TLS/slice/brotli/healthz all intact
# 11) Propagation: edit a rule -> config_version bump -> edge re-pulls -> new behavior within the poll interval
# 12) Live-site safety: cdn.a2zjav.com byte-identical behavior throughout; rollout one edge at a time with rollback
```
**Done when:** the edges run a **Lua programmable layer** that **enforces the per‑zone custom cache rules** (override‑TTL / bypass / force‑download / redirect, priority‑ordered first‑match) and **request/response header transforms**, all driven by pulled config, isolated per zone, fail‑open on error, with **no regressions** to WAF/shield/TLS/caching/health and no measurable hot‑path latency — closing the long‑standing "rules stored but not enforced" backlog item, verified locally with the live zone unaffected.

---

## Pitfalls (do not skip)
1. **Build choice** — prefer adding `lua-nginx-module` (+lua‑resty‑core/LuaJIT) as a **dynamic module to the existing nginx.org build** (keeps headers‑more/brotli/slice/Coraza/TLS intact); only switch to OpenResty if the module won't build cleanly. ABI‑lock to the nginx version; roll out one edge at a time.
2. **Phase ordering matters** — WAF/access checks, then cache‑rule Lua (rewrite/access), then cache lookup/upstream, then header transforms (header_filter). Get the order right or rules fight the WAF/cache.
3. **First‑match‑wins, priority‑ordered, per zone** — mirror the dashboard's ordering exactly; a zone only sees its own rules.
4. **Fall back to Phase‑1 defaults** when no rule matches — don't break built‑in video/static/HTML caching; rules override, absence = defaults.
5. **Fail‑open on Lua error** — `pcall` everything; a broken rule serves normally + logs, never blackholes a tenant.
6. **Keep the hot path lean** — no blocking I/O / per‑request network calls / heavy regex; don't add latency to cache HITs; load rules into Lua tables/shared dict refreshed on config pull, not per‑request disk reads.
7. **Managed‑header deny‑list** — tenants can't clobber `X-Brisk-*`, HSTS/TLS, or internal headers via transforms; validate inputs.
8. **No regressions** — Coraza WAF, origin shield, custom‑domain SNI TLS, slice/brotli, `$host` cache key, `default_server` health probe must all keep working with Lua in the pipeline.
9. **Reuse config‑pull** — rules/transforms via `config_version`; reload on change; no new transport.
10. **Live‑site safety + scope** — live zones byte‑identical unless a rule says otherwise; one edge at a time + rollback. This step = cache‑rule enforcement + header transforms via Lua. Broader edge compute/serverless = future; hardening + cleanup = Step 6.

## Next — Step 4.6 (do NOT start) — hardening + cleanup sweep (closes Phase 4)
The final Phase‑4 step: fold in the carried Phase‑2/3 backlog (`PUT /rules/{id}` + bulk reorder, `GET /zones/{id}/servers`, network‑aggregate `/stats`, status‑code/geo/top‑paths/latency + **origin‑tier counters for the shield offload metric**, a real **logs API** to replace the Logs placeholder), a **security/perf audit** (the whole multi‑tenant + WAF + TLS surface), and docs polish. After Step 4.6, Phase 4 is complete and **Phase 5 (customer portal + billing)** begins. Wait for the user's go‑ahead and a Step 4.6 prompt.
