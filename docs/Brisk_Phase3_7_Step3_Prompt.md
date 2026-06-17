# Brisk CDN — Phase 3.7 / Step 3 Build Prompt (Admin Auth + Deploy-Readiness) — closes Phase 3.7

**For Claude Code.** Context: `CLAUDE.md` + `docs/Brisk_Phase1_Build_Spec.md` + all Phase‑2/3 prompts + `docs/Control_Plane_Ops.md` + `dashboard-reference/`. **Phase 3.7 Steps 1–2 are complete:** all 3 live edges run the real `brisk-agent` (nginx.org 1.30.2, `Server: Brisk` + Brotli + video restored) against the **laptop control plane over autossh tunnels**; purge + stats fan‑out verified on the real fleet; **wildcard `*.a2zjav.com` TLS is now control‑plane‑managed via lego Bunny DNS‑01** (issued centrally, fanned to edges, auto‑renew). `cdn.a2zjav.com` serves the live WP site. **The control plane API is currently OPEN (no auth) — safe only because it's tunnel‑private; it cannot be exposed publicly until this step is done.** The dashboard's API client already has a single **`authHeader()` seam** (built since Phase 2 Step 6.1) waiting for real auth, and `accounts.role` (admin|customer) has existed in the schema since Phase 2.

> **Read `CLAUDE.md`, `docs/Control_Plane_Ops.md`, and the Phase‑2 Step‑1/Step‑2 prompts (accounts schema + agent token auth) first.** This is **Step 3 of 3 in Phase 3.7 — the final step.** It secures the control plane and makes it deploy‑ready. **No customer portal build** (that's Phase 5) — just the auth foundation it will slot into. Pass the acceptance tests; after this, **Phase 3.7 is complete.**

## Step 3.7.3 goal (one line)
Put **real admin authentication** in front of the dashboard + control‑plane API (so it can be safely exposed), wire it through the existing **`authHeader()` seam**, build it on a **tenant‑aware RBAC foundation** (admin now, customer portal later) without breaking the running agents, and document the **one‑step laptop→public‑VPS cutover**.

## ✅ Test locally — but don't break the live fleet
Auth goes on the **dashboard ↔ control‑plane** (human/UI) path. **The agent ↔ control‑plane path already has its own token auth** (Phase‑2 Step‑2: per‑agent SHA‑256 tokens) — **do not disturb it**; the 3 live agents must keep heartbeating/pulling/purging throughout. Test admin login locally; verify agents are unaffected.

---

## The auth model (build to this — researched best practice)
Two distinct caller types, two mechanisms, one shared identity/role core:

1. **Dashboard UI (human, same‑origin web app):** use a **session cookie** — `HttpOnly`, `Secure`, `SameSite`, short TTL with refresh/rotation. For a same‑domain SPA→API this is the recommended pattern (cookie not readable by JS → XSS‑resistant; pair with CSRF protection). Login endpoint verifies admin credentials (password hashed with **bcrypt/argon2** — NOT the fast SHA‑256 used for high‑entropy agent tokens; human passwords need a slow KDF), sets the session, and the SPA calls the API with the cookie.
2. **Programmatic/API access (scripts, future integrations):** **bearer token** in `Authorization: Bearer <token>` — opaque admin API tokens (created in the dashboard, hashed at rest, revocable). This is what power users/automation use; keep it separate from the agent tokens.
3. **Always over HTTPS/TLS** (every method is insecure without it). Short token lifetimes; rate‑limit the login endpoint (brute‑force/credential‑stuffing protection); never store secrets client‑side.

> The dashboard's existing **`authHeader()`** is the single seam — wire session/bearer through it so nothing else in the client changes (the whole client was built to add auth in one place).

## Part 1 — Identity + tenant‑aware RBAC core (the foundation that must be right now)
The single most important design rule from multi‑tenant SaaS practice: **every authorization decision is tenant‑aware** — you don't just check "is this user an admin?", you check "is this user allowed to act on *this* account's resources?". Build the core so the customer portal (Phase 5) slots in without a refactor:
- Use the existing **`accounts`** table + **`role`** (`admin` | `customer`). Add auth fields as needed: `password_hash` (argon2/bcrypt), `email`, timestamps; an `admin_api_tokens` table (hashed, revocable, like the agent‑token pattern but separate).
- A **middleware** that resolves the caller → an **identity context** `{account_id, role}` on every request (from session cookie or bearer token).
- **Authorization helper** anchored on the boring, consistent unit: **actor (account + role) → action → resource (account‑scoped)**. For now: `admin` ⇒ full access; `customer` ⇒ only resources where `resource.account_id == caller.account_id`. Implement the **tenant‑scoping check now even though only admin exists**, so the portal can't later leak across tenants (a missing scope check is one bug away from a data leak).
- Make it **debuggable/predictable**: a single chokepoint for "can actor do action on resource", not ad‑hoc checks scattered per handler.

## Part 2 — Protect the control‑plane API
- Put the auth middleware in front of **all admin/dashboard routes** (`/api/v1/servers`, `/zones`, `/stats`, `/overview`, `/purge*`, `/dns*`, `/health*`, drain, etc.). Unauthenticated → **401**; authenticated‑but‑not‑permitted → **403**.
- **Leave the agent routes on their existing token auth** (`/api/v1/agent/*` — config pull, heartbeat, stats, purge ack). Two separate authenticators (human vs agent), cleanly separated. **Verify the 3 live agents keep working.**
- Keep the **NATS** path as‑is (it's tunnel‑private; note for the public‑deploy step that NATS must stay private / authenticated when the control plane goes public).
- Add **login / logout / refresh** endpoints + admin **token create/revoke**. Seed the first admin account safely (env‑provided bootstrap credential or a one‑time CLI `create-admin`, never a hardcoded default password).

## Part 3 — Dashboard login UX
- A **login screen** (Voltage design) → posts credentials → session established → app loads. Logout clears the session. Handle **401 → redirect to login**, session expiry → re‑auth, all through the `authHeader()`/client layer (one place).
- An **admin settings** area (extend the Phase‑2 Settings stub): change password, manage **admin API tokens** (create shows once / revoke), and a placeholder for "users" the portal will grow into.
- Keep it role‑aware: the UI reads the identity context; today everything is `admin`, but gate components on role so the customer view (Phase 5) is a natural narrowing, not a rewrite.

## Part 4 — Deploy‑readiness + the one‑step cutover doc
- Confirm the **control plane is never openly bound without auth**: today tunnel‑private + now auth‑gated, so the public move is safe. Document the **laptop→public‑VPS cutover** in `docs/Control_Plane_Ops.md`:
  - The **2‑URL change** agents need (`BRISK_CONTROL_URL` / `BRISK_NATS_URL`) from Step 1.
  - Bring‑up on a public VPS (control plane + TimescaleDB + NATS in Docker), TLS on the control‑plane API, NATS secured (auth/TLS, not open), admin auth required, firewall.
  - A checklist: secrets via env, DB backup/restore, the lego TLS continuity, rollback to the laptop tunnels if needed.
  - Note this is the *plan*; the actual public deploy is the user's call later (they want to keep iterating locally for now).
- **Security sweep:** login rate‑limited, passwords argon2/bcrypt, tokens hashed at rest + revocable, cookies `HttpOnly`/`Secure`/`SameSite` + CSRF, no secrets in logs/repo, HTTPS everywhere.

---

## Acceptance tests (Step 3.7.3 definition of done — closes Phase 3.7)
```bash
docker compose up --build -d
# 1) Unauthed admin API -> 401
curl -s -o /dev/null -w '%{http_code}' localhost:8080/api/v1/servers            # 401
# 2) Login (seeded admin) -> session cookie set; password verified via argon2/bcrypt (not SHA-256)
# 3) Authed dashboard call works via the session cookie (through authHeader seam); logout -> 401 again
# 4) Admin bearer token: create in dashboard (shown once) -> Authorization: Bearer works -> revoke -> 401
# 5) Tenant scoping enforced NOW: a (test) customer-role account can only see its own account_id's resources;
#    admin sees all; a customer hitting another account's resource -> 403 (proves the portal-safe foundation)
# 6) AGENTS UNAFFECTED: the 3 live agents keep heartbeating + pulling config + receiving purges over the tunnels
#    (their /agent/* token auth path is untouched) -> cdn.a2zjav.com stays up
# 7) Login rate-limited (rapid bad logins throttled/locked); no user enumeration in error messages
# 8) Cookies HttpOnly+Secure+SameSite; CSRF protection on state-changing requests; HTTPS assumed
# 9) Bootstrap admin created safely (env/CLI, no hardcoded default password); seeding documented
# 10) Control_Plane_Ops.md has the laptop->public-VPS cutover runbook (2-URL change, TLS, NATS-secured, auth-required, rollback)
npm run build      # dashboard type-check + prod build pass
```
**Done when:** the dashboard + control‑plane API are behind **real admin auth** (session cookie for the UI, revocable bearer tokens for API, argon2/bcrypt passwords, rate‑limited login), wired through the existing **`authHeader()` seam**, built on a **tenant‑aware RBAC core** that already enforces account scoping (so the Phase‑5 customer portal slots in safely), with the **agent token path untouched** (live fleet unaffected) and the **laptop→public‑VPS cutover documented** — completing Phase 3.7. The control plane is now safe to expose whenever the user chooses to deploy it.

---

## Pitfalls (do not skip)
1. **Don't touch agent auth** — `/agent/*` keeps its Phase‑2 per‑agent token auth; the 3 live agents must keep working. Two separate authenticators (human vs agent).
2. **Human passwords need a slow KDF** — argon2/bcrypt for admin passwords, NOT the fast SHA‑256 used for high‑entropy agent tokens (different threat model).
3. **Tenant scoping NOW, even with only admin** — implement `resource.account_id == caller.account_id` checks today so the customer portal can't leak cross‑tenant later; a missing scope check is one bug from a data leak.
4. **Single authorization chokepoint** — actor→action→resource in one place, not scattered per‑handler checks.
5. **Session cookies done right** — `HttpOnly`+`Secure`+`SameSite`, short TTL + refresh, CSRF protection; tokens hashed at rest + revocable; nothing secret in localStorage.
6. **Wire through `authHeader()`** — one seam; don't scatter auth across the client.
7. **Rate‑limit login + safe bootstrap** — throttle brute force, no hardcoded default admin password, no user enumeration.
8. **Public‑deploy note** — when the control plane later goes public, **NATS must not be left open** and the API needs TLS; capture in the runbook (don't deploy publicly in this step).
9. **HTTPS everywhere** — all auth is insecure without TLS.
10. **Scope** — admin auth + RBAC foundation + cutover docs only. Customer portal = Phase 5; multi‑tenant host routing + custom domains = Phase 4.

## After Step 3.7.3 — Phase 3.7 is DONE ✅ → on to Phase 4
The control plane is **self‑driving and secured**: real agents over reachable transport, managed TLS, fan‑out verified, and now admin‑authed with a tenant‑aware RBAC core + a documented path to public deployment.

**Phase 4 (next — re‑scoped per the user's multi‑tenant requirement, do NOT start):**
1. **Multi‑tenant host‑based origin routing** — one `server` block per zone, edge selects origin by the requested `Host` header (so adding a new customer site doesn't collide with `cdn.a2zjav.com`'s origin) — the thing that lets Brisk host many sites.
2. **Custom‑domain CNAMEs + auto‑TLS** — customers point their own domain at Brisk; per‑domain certs via the lego machinery from Step 3.7.2 (the gateway to selling).
3. **Origin shield** (mid‑tier cache — prompt already drafted).
4. **WAF / rate‑limiting.**
5. **Lua/OpenResty edge logic** (+ custom cache‑rule enforcement).
6. **Hardening + cleanup sweep** (Phase‑2/3 backlog: `PUT /rules/{id}`, `GET /zones/{id}/servers`, network‑aggregate `/stats`, status‑code/geo/top‑paths/latency, real logs API, etc.).

Wait for the user's go‑ahead and a Phase 4 plan/Step‑1 prompt.
