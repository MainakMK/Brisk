# Brisk CDN — GATE 0 Unblock (v2): actually TEST the root password, then fix the right thing

**For Claude Code.** Supersedes the diagnosis in `Brisk_GATE0_Unblock_SSH_KeyAuth_Prompt.md`. **Key correction:** the earlier "locked out / Case B" verdict was wrong — every probe used `BatchMode=yes` (key-only), so **the root password was never actually tested.** The user confirms all 3 edges use **root password** auth. This prompt tests the password directly and fixes whichever real cause it finds. Context unchanged: Part-0 DB work done (zone 6 restored/managed/cfg 7, on all 3 edges; zone 12 quarantined; rollback dump present). Live site UP on frozen-good config; tunnels down because the stored password is being **rejected** by the tunnel — cause not yet confirmed.

> **Two leading causes (this prompt distinguishes them):**
> 1. **Container mangles the password** — docker-compose interpolates `$` in `.env` (a `$` in the root password becomes corrupted unless written `$$`), or a stale/changed container known_hosts. → fixable with **no console access**.
> 2. **sshd blocks root password over the network** — an OS/unattended-upgrade re-asserted `PermitRootLogin prohibit-password` or dropped `PasswordAuthentication no` in `/etc/ssh/sshd_config.d/*.conf`. The provider **web console still accepts the password** (console ≠ sshd), so it looks like "password works" while network SSH refuses it. → needs **one console action** per box.

> **Safety:** live site stays up; tunnels-down = frozen-good. No edge `sshd_config` edits over a path you don't have. Don't print passwords/keys. One edge at a time.

---

## STEP A — Decisive test: does the `.env` password work over SSH (password-only)?
Test the **actual stored password** against each edge, forcing password auth (no key, no BatchMode), short timeout. Requires `sshpass` locally (`which sshpass || sudo apt-get install -y sshpass`).
```bash
set -a; . brisk-control/tunnels/.env; set +a   # loads NY_PASS / DE_PASS / BLR_PASS
for pair in "104.248.231.144:$NY_PASS" "188.245.225.172:$DE_PASS" "139.59.78.21:$BLR_PASS"; do
  ip=${pair%%:*}; pw=${pair#*:}
  echo -n "$ip  password-only login: "
  SSHPASS="$pw" sshpass -e ssh -o PubkeyAuthentication=no -o PreferredAuthentications=password \
    -o ConnectTimeout=8 -o StrictHostKeyChecking=accept-new root@$ip 'echo OK' 2>&1 | tail -1
done
```
Interpret per edge (do NOT print the password itself):
- **`OK`** → the stored password is valid over SSH. The tunnel container is sending a *different* string → **CASE 1 (container mangling)** → go STEP B.
- **`Permission denied`** → the stored password is rejected over SSH (rotated, or root-password blocked by sshd) → **CASE 2** → go STEP C to pin down which, then STEP D.
- **timeout / refused** → network/tunnel-port issue, not auth → report and stop.

Also sanity-check whether compose is corrupting the value: 
```bash
grep -nE 'PASS=' brisk-control/tunnels/.env        # look for $, `, ", ', spaces in the values
docker compose -f brisk-control/tunnels/docker-compose.yml config | grep -iE 'PASS|SSHPASS' | sed 's/=.*/=<redacted>/'
# If the value in `.env` differs from what compose resolves (especially around `$`), that's CASE 1.
```

## STEP B — CASE 1 fix: stop the container mangling the password (no console)
Root cause is almost always `$` in the password being eaten by compose interpolation, or a changed host key in the container.
1. **Take the password out of compose interpolation.** Don't pass it as a compose-interpolated env. Instead mount it as a file and have the tunnel use `sshpass -f`:
   ```bash
   # write the literal password to a root-only file per edge (no shell interpolation):
   install -m600 /dev/stdin brisk-control/tunnels/secrets/ny.pass <<'EOF'
   <paste NY root password literally>
   EOF
   # repeat de.pass / blr.pass ; ensure brisk-control/tunnels/secrets/ is gitignored
   ```
   ```yaml
   # docker-compose.yml per tunnel service:
   volumes:
     - ./secrets/ny.pass:/run/secrets/ny.pass:ro
   # tunnel command: sshpass -f /run/secrets/ny.pass ssh -o StrictHostKeyChecking=accept-new ... root@<ip> ...
   ```
   (Alternative if you keep `.env`: escape every `$` as `$$` in the `.env` values, or wrap the whole value in single quotes — but the file + `sshpass -f` approach is mangling-proof and preferred.)
2. **Clear any stale container known_hosts** (use `accept-new`, or reset if the edge host key legitimately changed): ensure the ssh call uses `-o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=/root/.ssh/known_hosts` and the file isn't pinned to an old key.
3. Bring up and confirm pulls → STEP E.
   ```bash
   docker compose -f brisk-control/tunnels/docker-compose.yml up -d
   docker compose -f brisk-control/tunnels/docker-compose.yml logs --tail=80   # no "Permission denied"
   docker compose -f brisk-control/docker-compose.yml logs --since=5m brisk-control | grep -i "/agent/config"
   ```

## STEP C — CASE 2: confirm WHY the password is rejected (needs console once)
The user has provider web-console access (DO/Hetzner). Have the user open the console on one edge and run:
```bash
sudo sshd -T | grep -Ei 'permitrootlogin|passwordauthentication|kbdinteractiveauthentication'
sudo grep -RiE 'permitrootlogin|passwordauthentication' /etc/ssh/sshd_config /etc/ssh/sshd_config.d/ 2>/dev/null
sudo passwd -S root          # is the account/password expired or locked?  (L/NP/expiry)
```
- `permitrootlogin prohibit-password` or `passwordauthentication no` → **the update blocked it** (most likely). → STEP D, pick a fix.
- effective settings allow it but login still fails → the **password was rotated**; the user sets it (`sudo passwd root`) and you update the secret. → STEP D, password path.

## STEP D — CASE 2 fix: choose ONE (both done via web console)
Print these for the user; they run them in the provider console, then tell you which they did.

**Option D1 — RECOMMENDED, permanent (switch tunnels to key auth; immune to all future rotation/updates).** Generate a tunnel key locally and have the user paste the **public** key once per box:
```bash
cd brisk-control/tunnels && test -f id_ed25519 || ssh-keygen -t ed25519 -f ./id_ed25519 -N "" -C brisk-tunnel
cat ./id_ed25519.pub   # <-- give this to the user
```
Console (each edge): 
```bash
mkdir -p ~/.ssh && chmod 700 ~/.ssh
echo '<id_ed25519.pub contents>' >> ~/.ssh/authorized_keys && chmod 600 ~/.ssh/authorized_keys
```
Then (you) mount `id_ed25519:/root/.ssh/id_ed25519:ro`, set the tunnel ssh to `-i /root/.ssh/id_ed25519 -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new`, retire the `*_PASS` secrets, `up -d`, verify pulls → STEP E. **Note:** prohibit-password does NOT block key login, so this works even if the update flipped sshd.

**Option D2 — restore root password over SSH (keeps current setup; fragile to future updates).** Console (each edge):
```bash
sudo sed -ri 's/^#?\s*PermitRootLogin.*/PermitRootLogin yes/' /etc/ssh/sshd_config
sudo sed -ri 's/^#?\s*PasswordAuthentication.*/PasswordAuthentication yes/' /etc/ssh/sshd_config
sudo grep -RiE 'permitrootlogin|passwordauthentication' /etc/ssh/sshd_config.d/   # fix any drop-in that re-disables it
sudo systemctl restart ssh && sudo sshd -T | grep -Ei 'permitrootlogin|passwordauth'  # confirm yes/yes
```
If the password was also rotated: `sudo passwd root`, then update the tunnel secret (`.env` / `secrets/*.pass`). Then (you) `up -d`, verify pulls → STEP E.

## STEP E — Confirm pulls, then finish GATE 0
```bash
docker compose -f brisk-control/docker-compose.yml logs --since=5m brisk-control | grep -i "/agent/config"  # 200/304 from all 3
```
Then bump `cdn.a2zjav.com` cfg (7→8) → clean managed re-render per edge (`nginx -t` pass + reload) → external check:
```bash
for ip in 104.248.231.144 188.245.225.172 139.59.78.21; do echo "== $ip =="; \
  curl -ksI --resolve cdn.a2zjav.com:443:$ip https://cdn.a2zjav.com/ | egrep -i 'HTTP/|server:|x-brisk-cache'; done
# all 3: 200, Server: Brisk, HIT, valid TLS  => GATE 0 GREEN
```

## STEP F — Continue Parts 1→6
Once GATE 0 is green, proceed through `Brisk_Phase4_Step7_AutoOnboard_Prompt.md` Parts 1→6 (auto-assign → `*.cdn.a2zjav.com` CNAME → `*.cdn.a2zjav.com` TLS → delete-teardown + `purge_jobs` FK → dashboard → e2e). One edge at a time, rollback ready, live site 200/HIT throughout.

---

## Acceptance
```
- STEP A run; password-only result recorded per edge (OK vs denied) — the test that was previously skipped
- Root cause identified: CASE 1 (container/compose mangling) or CASE 2 (sshd blocked / password rotated)
- Fix applied; tunnels up with NO "Permission denied"; all 3 edges hit /agent/config
- cfg bump -> clean nginx -t + reload per edge; cdn.a2zjav.com 200/HIT/Server: Brisk on all 3, unchanged
- secrets handled safely (file+sshpass -f or key auth); nothing secret printed or committed
```

## Pitfalls
1. **Don't conclude "locked out" without STEP A** — password-only test first; BatchMode/key probes do NOT test passwords.
2. **`$` in `.env` is the classic trap** — compose interpolates it; prefer `sshpass -f <file>` or escape `$$`.
3. **Console ≠ sshd** — the password working in the provider web console does NOT mean sshd accepts root password over the network; check `sshd -T`.
4. **Option D1 (keys) is the durable fix** — survives the next unattended-upgrade; D2 will likely break again.
5. **Live site stays up** — only let a re-render happen after pulls are confirmed; verify each edge before the next.
6. Never print the root password or private key; gitignore `secrets/` and `id_ed25519`.
