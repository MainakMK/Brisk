# Brisk CDN — GATE 0 Unblock: migrate laptop→edge tunnels to SSH **key auth**, then finish GATE 0

**For Claude Code.** Continues `docs/Brisk_Phase4_Step7_AutoOnboard_Prompt.md` (Part 0). Context: Part-0 DB work is **done** — zone 6 `cdn.a2zjav.com` restored as a managed record (`config_version 7`), re-assigned to all 3 edges {NY 19 (104.248.231.144), FRA 20 (188.245.225.172), BLR 3 (139.59.78.21)}; zone 12 `testmainak.cdn.a2zjav.com` quarantined (0 edges); rollback dump at `backups/brisk-pre-part0-20260612.dump`. **Live site is up** (`cdn.a2zjav.com` → 200/HIT/`Server: Brisk` on all 3) but serving **frozen last-known-good** config — the edges **cannot pull** because the laptop→edge reverse-SSH tunnels fail auth on all 3 (`Permission denied (publickey,password)`), persisting >1h (not transient faillock). Almost certainly password auth was disabled fleet-wide or the tunnel passwords rotated.

> **Goal:** stop relying on passwords — move the tunnels to **SSH key auth** (more secure, immune to rotation/faillock; the tunnel entrypoint already supports `/root/.ssh/id_ed25519`), get all 3 edges pulling `/agent/config` again, then **finish GATE 0** and continue into Parts 1→6.

> **Safety:** The live site must stay up the whole time. With tunnels down nothing can push a bad config, so you're working against a *frozen-good* fleet — keep it that way until key auth works. Do **not** modify edge `sshd_config` or any edge service beyond appending an authorized key. Do **not** print private keys or passwords. One edge at a time.

---

## STEP A — Diagnose connectivity & find a working SSH path (read-only)
1. For each edge, record which auth methods sshd still offers:
   ```bash
   for ip in 104.248.231.144 188.245.225.172 139.59.78.21; do
     echo "== $ip =="
     ssh -v -o BatchMode=yes -o ConnectTimeout=8 -o StrictHostKeyChecking=accept-new root@$ip exit 2>&1 \
       | grep -i "authentications that can continue"
   done
   # "publickey" only  -> password auth is OFF fleet-wide (image/unattended-upgrade flipped it)
   # "publickey,password" -> password still offered, so the stored tunnel password was rotated/wrong
   ```
2. Determine whether a **working key path already exists** from this laptop (your personal key / ssh-agent) — this decides whether you can self-serve the pubkey install:
   ```bash
   for ip in 104.248.231.144 188.245.225.172 139.59.78.21; do
     echo -n "$ip working-key-login: "
     ssh -o BatchMode=yes -o ConnectTimeout=8 -o StrictHostKeyChecking=accept-new root@$ip 'echo OK' 2>/dev/null || echo "NO"
   done
   ```
   - If a host prints `OK` → you have a working path to install the new key on it (proceed Step B, Case A).
   - If a host prints `NO` and offers `publickey` only → you're locked out of that box from the CLI → **Case B** (human console needed; see Step E). Stop before touching that edge and report.
   - If host-key *changed* errors appear (edge was rebuilt), note it and `ssh-keygen -R <ip>` to clear the stale entry, then retry — but only if you're confident it's the same host (don't blindly trust a changed host key; flag it).

## STEP B — Generate a dedicated tunnel key & install the public key
1. Generate a fresh, dedicated key (do **not** reuse a personal key):
   ```bash
   cd brisk-control/tunnels
   test -f id_ed25519 || ssh-keygen -t ed25519 -f ./id_ed25519 -N "" -C "brisk-tunnel"
   chmod 600 ./id_ed25519; chmod 644 ./id_ed25519.pub
   grep -q '^id_ed25519$' .gitignore 2>/dev/null || printf 'id_ed25519\nid_ed25519.pub\n' >> .gitignore
   ```
2. **Case A (working path exists for the edge):** append the pubkey via the working login, key-only so it can't silently fall back to a password:
   ```bash
   PUB="$(cat ./id_ed25519.pub)"
   for ip in 104.248.231.144 188.245.225.172 139.59.78.21; do
     echo "== install pubkey on $ip =="
     ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new root@$ip \
       "umask 077; mkdir -p ~/.ssh && grep -qxF '$PUB' ~/.ssh/authorized_keys 2>/dev/null || echo '$PUB' >> ~/.ssh/authorized_keys && echo INSTALLED"
   done
   ```
   Verify the new key alone logs in:
   ```bash
   for ip in 104.248.231.144 188.245.225.172 139.59.78.21; do
     echo -n "$ip new-key login: "
     ssh -i ./id_ed25519 -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=accept-new root@$ip 'echo OK' 2>/dev/null || echo "FAIL"
   done
   ```
3. **Case B (no working path on an edge):** you cannot install the key over the network. **Skip that edge, do not retry-loop**, and surface it in Step E for the user's web-console action. Continue with any Case-A edges.

## STEP C — Switch the tunnels from password to key auth
1. Inspect the existing tunnel transport first (`tunnels/docker-compose.yml`, entrypoint/Dockerfile, any `autossh`/`ssh -R`/`sshpass` usage, and the `NY_PASS/DE_PASS/BLR_PASS` env in `tunnels/.env`). The entrypoint reportedly already supports `/root/.ssh/id_ed25519` — prefer flipping the existing key path over rewriting the transport.
2. Mount the key read-only into each tunnel service and select key auth:
   ```yaml
   # tunnels/docker-compose.yml — per tunnel service:
   volumes:
     - ./id_ed25519:/root/.ssh/id_ed25519:ro
   # ssh invocation should use:
   #   -i /root/.ssh/id_ed25519 -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new
   # and DROP the sshpass / *_PASS password path (retire NY_PASS/DE_PASS/BLR_PASS).
   ```
3. Retire the passwords: remove `NY_PASS/DE_PASS/BLR_PASS` from `.env` (and any sshpass call). Keep `.env` keyless. (Optional, do **not** do here: re-enabling/hardening edge sshd — out of scope.)

## STEP D — Bring tunnels up & confirm edges pull
```bash
docker compose -f brisk-control/tunnels/docker-compose.yml up -d
docker compose -f brisk-control/tunnels/docker-compose.yml logs --tail=80     # expect: tunnels established, NO "Permission denied"
# control plane should now see config pulls from all reachable edges:
docker compose -f brisk-control/docker-compose.yml logs --since=5m brisk-control | grep -i "/agent/config"   # expect 200/304 from each edge IP
```
Gate D: all reachable edges show established tunnels (no auth errors) and `/agent/config` hits.

## STEP E — If any edge is Case B (locked out): STOP and hand back to the user
For each locked-out edge, print **only the public key** and clear console instructions, then stop (don't loop):
```
Edge <name/ip> is locked out from the CLI (publickey-only, no working key). To finish:
  1. Open the DigitalOcean/Hetzner web recovery console for this box.
  2. Append this line to /root/.ssh/authorized_keys:
       <contents of brisk-control/tunnels/id_ed25519.pub>
  3. Tell me "key installed on <ip>" and I'll resume Steps C–D for it.
```
Do not attempt to brute-force, reset passwords, or modify the edge image.

## STEP F — Finish GATE 0 (once all 3 edges pull)
1. Bump `cdn.a2zjav.com` `config_version` (7 → 8) and confirm a **clean managed re-render** on each edge — `nginx -t` **passes**, reload succeeds, the vhost is now driven by the managed record (not stuck last-known-good).
2. Verify externally, per edge:
   ```bash
   for ip in 104.248.231.144 188.245.225.172 139.59.78.21; do
     echo "== $ip =="; curl -ksI --resolve cdn.a2zjav.com:443:$ip https://cdn.a2zjav.com/ \
       | egrep -i 'HTTP/|server:|x-brisk-cache|x-brisk-edge'
   done
   # Expect on all 3: HTTP 200, Server: Brisk, X-Brisk-Cache: HIT (warm), valid TLS (*.a2zjav.com)
   ```
**🔒 GATE 0 GREEN** = all 3 edges pulling + a `config_version` bump re-renders cleanly + `cdn.a2zjav.com` 200/HIT on all 3 from the managed config.

## STEP G — Continue with Parts 1→6
Once GATE 0 is green, proceed through `Brisk_Phase4_Step7_AutoOnboard_Prompt.md` **Parts 1→6 as written** — auto-assign → wildcard CNAME `*.cdn.a2zjav.com` DNS → `*.cdn.a2zjav.com` TLS → delete-teardown + `purge_jobs` FK migration → dashboard → end-to-end verify (re-onboards zone 12 + a fresh test zone). One edge at a time, rollback ready, run each part's acceptance gate, keep `cdn.a2zjav.com` 200/HIT throughout.

---

## Acceptance (this prompt)
```
- ssh -i tunnels/id_ed25519 -o IdentitiesOnly=yes root@<each edge> -> OK (key auth works on all 3)
- tunnels up with NO "Permission denied"; passwords retired from tunnels/.env; key gitignored
- control-plane logs show /agent/config hits from all 3 edges
- config_version bump -> clean nginx -t + reload on each edge (managed render, not stuck-guard)
- cdn.a2zjav.com -> 200/HIT/Server: Brisk/valid TLS on all 3, externally verified, unchanged content
- GATE 0 green -> Parts 1-6 underway (or cleanly checkpointed if you want a stop before them)
```

## Pitfalls
1. **Don't print/commit the private key or passwords.** Key is `chmod 600`, gitignored, mounted read-only.
2. **Key-only verification** (`IdentitiesOnly=yes`, `BatchMode=yes`) so you never mistake a lingering password path for success.
3. **Case B is human-only** — print the pubkey + console steps and stop; never reset edge passwords or rebuild the box.
4. **Host-key changed?** Could mean the edge was rebuilt (or worse). Don't blindly accept — flag it, confirm it's the same host before `ssh-keygen -R`.
5. **Live site stays up** — tunnels-down = frozen-good; only let a re-render happen once key auth is solid; verify each edge before the next.
6. Don't touch edge `sshd_config` or services beyond `authorized_keys`.
