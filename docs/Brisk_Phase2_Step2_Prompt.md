# Brisk CDN — Phase 2 / Step 2 Build Prompt (Auth + Add-Server Provisioning)

**For Claude Code.** Context in the repo: `CLAUDE.md` + `docs/Brisk_Phase1_Build_Spec.md` + `docs/Brisk_Phase2_Step1_Prompt.md` and the earlier prompts. **Phase 2 Step 1 is complete** — `brisk-control` (Go + chi + pgx + TimescaleDB in Docker) is up with the full schema (servers, zones, cache_rules, accounts, agent_tokens, stats hypertable), server/zone CRUD, `config_version` bumping, and the `/api/v1/agent/config` 501 stub. The Phase‑1 `brisk-agent` is live on a real VPS with stub interfaces (`config.Source`, `purge.Purger`, `stats.Reporter`, `client.ControlPlane`).

> **Read `CLAUDE.md`, `docs/Brisk_Phase1_Build_Spec.md`, and `docs/Brisk_Phase2_Step1_Prompt.md` first.** This is **Step 2 of 7 in Phase 2**. Build only what's in scope, commit in pieces, pass the acceptance tests, and stop before Step 3. This is a **larger step** (auth + remote SSH provisioning + agent‑side auth wiring) — expect multiple sub‑sessions.

## Step 2 goal (one line)
Make the control plane able to **securely onboard a server end‑to‑end**: admin adds a server (IP + SSH creds) → control plane generates a **per‑agent API token**, **SSHes in and provisions** the agent (installs/bootstraps it with the token), and the agent then **authenticates back** to the control plane. All agent‑facing endpoints are protected by **bearer‑token auth**, designed so it can be swapped to **mTLS** later without a rewrite.

---

## Part 1 — The token system (auth foundation)

### Token generation
- Generate with `crypto/rand`: **32 random bytes (256 bits)**, base64url‑encoded, with an identifiable **prefix**: format `brisk_<base64url>`. (≥128 bits entropy is the floor; 256 is comfortable.)
- A prefix (e.g. `brisk_`) makes tokens identifiable and lets secret‑scanning tools catch leaks.

### Token storage — hash with **SHA‑256**, NOT bcrypt/argon2
This is the important, often‑misunderstood part: **use a fast hash (SHA‑256) for API tokens, not bcrypt/argon2.** Reason: API tokens have **high entropy** (unlike human passwords), so brute‑forcing the hash is computationally impossible regardless of hash speed — and SHA‑256 is **fast**, which matters because you validate a token on **every request**. (bcrypt/argon2's deliberate slowness is for low‑entropy passwords; it would just add latency here.)
- Store: an **indexed prefix** (e.g. first ~12 chars) for fast DB lookup + the **SHA‑256 hash** of the full token. **Never store the plaintext token.**
- **Show the token only once**, at creation. After that only the hash exists.
- **Never log tokens** — log the prefix / token ID only.

```go
// generate
raw := make([]byte, 32); _, _ = rand.Read(raw)
token := "brisk_" + base64.RawURLEncoding.EncodeToString(raw)
prefix := token[:14]                                  // indexed lookup key
sum := sha256.Sum256([]byte(token)); hash := hex.EncodeToString(sum[:])
// store {prefix, hash, server_id}; return `token` to caller ONCE
```

### Verification (on every agent request)
Extract `Authorization: Bearer <token>` → compute prefix → `SELECT ... WHERE token_prefix = $1 AND revoked_at IS NULL` → SHA‑256 the presented token → **constant‑time compare** (`crypto/subtle.ConstantTimeCompare`) against the stored hash → attach `server_id` to request context. Reject with `401` on missing/invalid/revoked. Use the `Authorization: Bearer` header (never query strings — they leak into logs).

### Revocation & rotation
- `revoked_at` on `agent_tokens` (already in schema) — revoked tokens fail auth.
- `POST /api/v1/servers/{id}/token/rotate` → issue a new token (returned once), revoke the old after the agent picks up the new one.

---

## Part 2 — Auth middleware, mTLS‑ready

Define an interface so the auth mechanism is swappable:
```go
type Authenticator interface {
    // returns the authenticated server's ID, or an error
    Authenticate(r *http.Request) (serverID int64, err error)
}
```
- **Now:** `TokenAuthenticator` (the bearer‑token logic above).
- **Later (Phase 3+):** `MТLSAuthenticator` that reads the client certificate from `r.TLS.PeerCertificates` and maps it to a server — a drop‑in replacement, no handler changes.
Wire it as chi middleware applied to the `/api/v1/agent/*` route group. Admin/dashboard routes get their own auth later (Step 6) — for now leave them open locally but structure the router so admin auth slots in.

---

## Part 3 — Add‑server provisioning flow (the headline feature)

This is the "click Add Server → it sets itself up" magic. Use **`golang.org/x/crypto/ssh`** (+ `github.com/pkg/sftp` for file copy).

### Extended `POST /api/v1/servers`
Accepts: `name, region, ip, ssh_user, ssh_port (default 22), capacity_mbps`, and **SSH credentials** — either `ssh_password` or `ssh_private_key`. Flow:
1. Create the server row (`status = pending`), generate its **agent token** (store hash, return once in the response).
2. Kick off **provisioning** (synchronous is fine for Step 2; make it a background job later). Set `status = provisioning`.

### Provisioning steps (the control plane → server over SSH)
1. **Dial** `ssh.Dial("tcp", ip:port, cfg)` with `ssh.Password(...)` or `ssh.PublicKeys(signer)`.
2. **Host key — Trust On First Use (TOFU), not `InsecureIgnoreHostKey`.** On first connect, capture the server's host key, **store it** on the server row, and verify against it on every later connect. (`InsecureIgnoreHostKey()` is explicitly *not* for production — it allows MITM.)
3. **Install the control plane's own SSH key:** the control plane has its own generated keypair; during this first (password) login, append its **public key** to the server's `authorized_keys`. After this, all future ops use **key auth** — so the **user's password is used once and never stored.** (Matches your plan: log in, then stop depending on the password.)
4. **Copy the agent:** SFTP the `brisk-agent` binary to `/usr/local/bin/`, and write `/etc/brisk/agent.yaml` containing `control_plane_url`, `edge_id`, and the **agent token** (file perms `600`).
5. **Bootstrap:** run `brisk-agent --bootstrap` remotely (idempotent — safe even though the VPS already has it from Phase 1). Stream stdout/stderr into a provisioning log.
6. **Optional hardening** (your plan): after the control‑plane key works, disable SSH password auth / root password login. Make this a flag (`harden: true`), off by default for the first test.
7. When the agent checks in (Part 4), set `status = online`, `provisioned_at = now()`.

### Endpoints
```
POST /api/v1/servers                      # create + provision (returns token ONCE)
POST /api/v1/servers/{id}/reprovision     # re-run provisioning
POST /api/v1/servers/{id}/token/rotate    # rotate agent token
GET  /api/v1/servers/{id}/provision-log   # provisioning output (for the dashboard later)
```

### Schema additions (`migrations/00002_*.sql`)
```sql
-- +goose Up
ALTER TABLE servers ADD COLUMN ssh_user      TEXT;
ALTER TABLE servers ADD COLUMN ssh_port      INTEGER NOT NULL DEFAULT 22;
ALTER TABLE servers ADD COLUMN host_key      TEXT;            -- TOFU-captured SSH host key
ALTER TABLE servers ADD COLUMN provisioned_at TIMESTAMPTZ;
ALTER TABLE agent_tokens ADD COLUMN token_prefix TEXT;        -- indexed lookup
CREATE INDEX idx_agent_tokens_prefix ON agent_tokens(token_prefix) WHERE revoked_at IS NULL;
CREATE TABLE provision_logs (
  id         BIGSERIAL PRIMARY KEY,
  server_id  BIGINT NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
  ts         TIMESTAMPTZ NOT NULL DEFAULT now(),
  level      TEXT NOT NULL DEFAULT 'info',
  message    TEXT NOT NULL
);
-- +goose Down
DROP TABLE IF EXISTS provision_logs;
ALTER TABLE agent_tokens DROP COLUMN token_prefix;
ALTER TABLE servers DROP COLUMN provisioned_at, DROP COLUMN host_key, DROP COLUMN ssh_port, DROP COLUMN ssh_user;
```
**Never persist the SSH password** — it's used only during the provisioning call, then discarded. (If you must support re‑provision without re‑entering it, rely on the installed control‑plane key, not a stored password.)

---

## Part 4 — Agent‑side auth wiring (in the `brisk-agent` repo)

- Agent config (`agent.yaml`) gains: `control_plane_url`, `agent_token` (or path to a `600` token file).
- Implement enough of `client.ControlPlane` to send an **authenticated heartbeat**: `POST /api/v1/agent/heartbeat` with `Authorization: Bearer <token>`, body `{edge_id, agent_version, nginx_version}`. On success the control plane sets the server `online` + `last_seen`.
- **Config pull stays the stub** (`GET /api/v1/agent/config` still returns 501 — that's Step 3). Heartbeat is only to prove auth works end‑to‑end now.
- The agent must keep working in **standalone/local mode** too (if `control_plane_url` is empty), so Phase‑1 behavior is preserved.

---

## Acceptance tests (Step 2 definition of done)
```bash
# Auth gate works
curl -s -o /dev/null -w '%{http_code}\n' localhost:8080/api/v1/agent/heartbeat            # 401 (no token)
curl -s -o /dev/null -w '%{http_code}\n' -H 'Authorization: Bearer brisk_bogus' \
  localhost:8080/api/v1/agent/heartbeat                                                   # 401 (bad token)

# Add + provision the real VPS (user supplies creds at runtime; not stored)
curl -s -X POST localhost:8080/api/v1/servers -H 'Content-Type: application/json' -d '{
  "name":"del-edge-1","region":"IN-DEL","ip":"<VPS_IP>","ssh_user":"root",
  "ssh_password":"<PW>","capacity_mbps":1000
}'
# -> 201 with an agent token shown ONCE; provisioning runs over SSH
curl -s localhost:8080/api/v1/servers/1/provision-log     # streamed bootstrap output
curl -s localhost:8080/api/v1/servers/1 | grep status     # provisioning -> online after agent heartbeat

# Authenticated heartbeat from the agent succeeds (run on/against the VPS agent)
#   agent uses its stored token -> 200, server.last_seen updates

# Token in DB is a HASH, not plaintext
docker compose exec timescaledb psql -U brisk -d brisk -c "SELECT token_prefix, left(token_hash,12) FROM agent_tokens;"  # prefix + hash, no plaintext

# Rotation revokes the old token
curl -s -X POST localhost:8080/api/v1/servers/1/token/rotate    # new token; old now 401s
```
**Done when:** adding a server issues a one‑time token, the control plane **SSHes in and provisions the agent**, the agent **heartbeats with bearer auth** and flips the server to `online`, unauthenticated/invalid requests get `401`, tokens are stored only as **SHA‑256 hashes** (with an indexed prefix), rotation revokes the old token, and the agent still runs standalone when no control plane is configured.

---

## Pitfalls (do not skip)
1. **SHA‑256 for tokens, not bcrypt/argon2** — fast verification on every request; security comes from the token's entropy, not hash slowness. (bcrypt/argon2 are for passwords.)
2. **Constant‑time compare** the hashes (`subtle.ConstantTimeCompare`) to avoid timing attacks.
3. **Never store plaintext tokens; never log them** — log the prefix only. Show the token once.
4. **TOFU host keys, not `InsecureIgnoreHostKey`** — capture + pin the host key; ignoring it allows MITM during provisioning.
5. **Don't persist the SSH password** — use it once, install the control‑plane's own key, then use key auth. Optional hardening disables password/root login after.
6. **Control‑plane ↔ agent must be HTTPS in production** (tokens travel in the header). Local Docker can be http; document that the deployed control plane needs TLS.
7. **Idempotent provisioning** — re‑running bootstrap on the already‑provisioned VPS must be safe.
8. **Preserve standalone mode** — empty `control_plane_url` ⇒ agent behaves exactly like Phase 1.
9. **mTLS‑ready** — keep auth behind the `Authenticator` interface so the client‑cert swap is contained.

## Forward hooks (ready, not built)
- `GET /api/v1/agent/config` stays **501** but now sits behind token auth — Step 3 implements the real pull (returns the server's zones + `config_version`).
- Stats ingest endpoint (Step 4) will reuse the same token‑auth middleware.
- `Authenticator` interface ⇒ mTLS implementation later.

## Next — Step 3 (do NOT start)
Agent **pull‑config**: implement `GET /api/v1/agent/config` (return the zones assigned to the calling server + `config_version`), and flip the agent's `config.Source` from local YAML to pulling from the control plane, saving a **local last‑known‑good** copy (so dashboard down ≠ edge down). Wait for the user's go‑ahead and a Step 3 prompt.
