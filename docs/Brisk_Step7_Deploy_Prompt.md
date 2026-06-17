# Brisk CDN — Phase 1 / Step 7 Build & Deploy Prompt (End‑to‑End + Real VPS)

**For Claude Code.** Context in the repo: `CLAUDE.md` + `docs/Brisk_Phase1_Build_Spec.md` + `Brisk_Step3_Prompt.md` + `Brisk_Steps4-6_Prompt.md`. **Steps 1–6 are done and verified locally** (caching, HLS/video, `Server: Brisk` + `X-Brisk-*`, TLS 1.3/ECDSA self‑signed, Brotli + kernel/Nginx tuning, idempotent `--bootstrap`, systemd). The Let's Encrypt path already exists (`github.com/go-acme/lego/v4`) but has only been built, not exercised.

> **Read `CLAUDE.md` and `docs/Brisk_Phase1_Build_Spec.md` first.** Step 7 is the final step of Phase 1: a full **end‑to‑end test**, then a **real deployment to one VPS with a real, browser‑trusted Let's Encrypt certificate**, plus validation, hardening, reboot/renewal resilience, and a light capacity sanity check.

## Runtime inputs (the user will provide these in chat — do NOT hardcode)
- **`<YOUR_DOMAIN>`** — a public domain/subdomain whose **A record already points to the VPS IP**.
- **`<VPS_IP>`**, **`<SSH_USER>`** (a non‑root sudo user), and **SSH access** (key‑based).
- **`<LE_EMAIL>`** — email for the Let's Encrypt account.

If any are missing when you start, **ask the user for them**. Run all deploy/test commands **on the VPS over SSH** (e.g. `ssh <SSH_USER>@<VPS_IP> "..."`), or directly if you are running on the VPS. **Confirm SSH connectivity first** before doing anything else.

---

# PART A — End‑to‑end local sanity (quick, before touching the VPS)
Re‑run the full Phase‑1 edge once in WSL2/Docker against the local test origin and confirm the headline checks still pass together (not just per‑step): cache MISS→HIT, `Server: Brisk` + `X-Brisk-*` on success and on 404, HLS 206 range, `.m3u8` short‑TTL, Brotli `br` on text + gzip fallback, images uncompressed, HTTPS on TLS 1.3 with the self‑signed ECDSA cert, `nginx -t` clean. This guards against regressions before the real deploy. Keep it brief.

---

# PART B — Deploy to the VPS

## B1. Pre‑flight
- VPS: **Ubuntu 24.04 LTS** (DigitalOcean droplet or similar), **key‑based SSH**, **non‑root sudo user**.
- DNS: confirm `<YOUR_DOMAIN>` resolves to `<VPS_IP>` **before** requesting a cert:
  ```bash
  dig +short <YOUR_DOMAIN>      # must return <VPS_IP>
  ```
- Time: ensure NTP/clock is correct (ACME is time‑sensitive): `timedatectl`.

## B2. Get Brisk onto the VPS
The `brisk-agent` is a static Go binary. Two clean options:
- **Cross‑compile locally + copy:** `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o brisk-agent ./...` then `scp` the binary + repo (templates, `deploy/`) to the VPS.
- **Build on the VPS:** install Go 1.26.x on the VPS, `git`/`scp` the repo, `go build`.
Either way the binary lands at `/usr/local/bin/brisk-agent` (the bootstrap also installs it).

## B3. Run the idempotent bootstrap on the VPS
```bash
sudo /usr/local/bin/brisk-agent --bootstrap
```
This must, on the real VPS, **successfully add the official nginx.org repo and install Nginx stable 1.30.x** (in the local sandbox there was no `noble` package and it fell back to distro 1.24.0 — on the real VPS verify it gets **nginx.org 1.30.x**, so the `http2 on;` path and module ABI are correct). Then it builds `headers-more` + `ngx_brotli` against 1.30.x, writes sysctl, creates dirs, generates config, and enables systemd units.
**Verify:**
```bash
nginx -v                          # expect nginx.org 1.30.x
nginx -t                          # syntax ok, modules load
systemctl is-active nginx brisk-agent
```

## B4. Set up a real test origin (so there's something to cache)
On the VPS (or a second small box), run a minimal origin the edge pulls from — e.g. a static site + a tiny HLS sample (a few `.ts`/`.m4s` + `init.mp4` + an `.m3u8`) served on `127.0.0.1:8000`. Point the zone's `origin` at it. **Keep the origin bound to localhost** (not public) so only the edge reaches it — first step of origin lockdown.

## B5. Let's Encrypt — STAGING FIRST, then PRODUCTION (critical)
**Do NOT request a production cert on the first try.** Let's Encrypt enforces real rate limits — the strictest is **5 certificates per exact set of domain names per 7 days, refilling 1 every ~34 hours** — so failed iterations against production can lock you out for days. The staging environment exists exactly for this and has very generous limits.

**Flow:**
1. Set `agent.yaml`: `tls_mode: letsencrypt`, `letsencrypt_email: <LE_EMAIL>`, `letsencrypt_staging: true`, domain `<YOUR_DOMAIN>`.
2. Ensure **port 80 is open and reachable** — HTTP‑01 validation places a token at `http://<YOUR_DOMAIN>/.well-known/acme-challenge/<TOKEN>` and **can only be done on port 80**. The agent (via lego HTTP‑01) must serve that path (own challenge server on :80, or webroot) while Nginx handles the rest.
3. Issue against **staging** (`https://acme-staging-v02.api.letsencrypt.org/directory`). Confirm the full ACME flow works and a cert is written to `/etc/brisk/tls/<YOUR_DOMAIN>/`. Staging certs are **not browser‑trusted** (expect a warning) — that's fine; we're proving the flow.
4. **Only after staging succeeds**, flip `letsencrypt_staging: false`, clear the staging cert, and issue against **production**. Now the cert is real and browser‑trusted.
5. Use **ECDSA** (P‑256) and ensure the client uses **ARI** (ACME Renewal Info) for renewals — lego supports it, and **ARI renewals are exempt from rate limits**.

> Profiles note: the default Let's Encrypt "classic" profile (≈90‑day, moving to 64‑day in 2027) is correct for a normal domain cert. Don't use the `shortlived`/IP‑cert profiles. DNS‑01 (via Bunny DNS) is a Phase‑2+ option for wildcards/multi‑PoP; for one domain on a public box, **HTTP‑01 is simplest** — use it.

## B6. Firewall + SSH hardening
```bash
sudo ufw allow 22/tcp      # SSH (or your custom port)
sudo ufw allow 80/tcp      # HTTP-01 + 80->443 redirect
sudo ufw allow 443/tcp     # HTTPS
sudo ufw enable
```
(If using DigitalOcean's cloud firewall, mirror the same three ports.) Disable SSH password auth (key‑only), keep the non‑root sudo user.

---

# PART C — Real‑world validation (over the public internet, real cert)
```bash
# Real, browser-trusted cert + TLS 1.3 + ECDSA
echo | openssl s_client -connect <YOUR_DOMAIN>:443 -servername <YOUR_DOMAIN> 2>/dev/null \
  | openssl x509 -noout -issuer -subject -dates -ext subjectAltName     # issuer = Let's Encrypt, SAN matches
echo | openssl s_client -connect <YOUR_DOMAIN>:443 2>/dev/null | grep -i 'Protocol\|Cipher'   # TLSv1.3

# No -k needed now (trusted):
curl -sI https://<YOUR_DOMAIN>/                         # 200, Server: Brisk
curl -sI https://<YOUR_DOMAIN>/style.css | grep -i x-brisk-cache   # MISS then HIT
curl -sI http://<YOUR_DOMAIN>/  | grep -i '301\|location'          # HTTP->HTTPS

# Video over the real domain
curl -sI -H 'Range: bytes=0-1048575' https://<YOUR_DOMAIN>/video/seg1.ts | head -1   # 206
curl -sI https://<YOUR_DOMAIN>/video/index.m3u8 | grep -i x-brisk-cache               # short-TTL
curl -sI https://<YOUR_DOMAIN>/video/seg1.ts | grep -i access-control-allow-origin    # CORS *

# Brotli + headers
curl -sI -H 'Accept-Encoding: br' https://<YOUR_DOMAIN>/app.js | grep -i content-encoding   # br
curl -sI https://<YOUR_DOMAIN>/ | grep -i 'x-brisk-'                                          # Edge/Cache/Request-Id

# BBR actually active on the real kernel (this is the real test — couldn't verify in Docker)
ssh <SSH_USER>@<VPS_IP> "sysctl net.ipv4.tcp_congestion_control"     # bbr
```
Optional external grade: run an SSL Labs test (ssllabs.com/ssltest) on `<YOUR_DOMAIN>` and aim for **A/A+**, or `testssl.sh <YOUR_DOMAIN>`.

---

# PART D — Auto‑renewal + reboot resilience
- **Renewal:** confirm the agent schedules ECDSA renewal via ARI well before expiry. Test a renewal against **staging** (don't burn production limits). Verify it rewrites the cert and **reloads Nginx without dropping connections** (`nginx -t` → `nginx -s reload`).
- **Reboot survival (now a real test — works on the VPS unlike Docker):**
  ```bash
  sudo reboot
  # after it returns:
  systemctl is-active nginx brisk-agent      # both active automatically
  curl -sI https://<YOUR_DOMAIN>/ | grep -i 'server:'   # Server: Brisk, still serving
  ```

---

# PART E — Capacity & latency sanity (light — not a full benchmark)
Confirm the edge serves under concurrency and that HITs are fast. Keep load modest (a small droplet is not the 10 Gbps target hardware):
```bash
# e.g. with `hey` (go install github.com/rakyll/hey@latest) — warm the cache first, then:
hey -n 5000 -c 100 https://<YOUR_DOMAIN>/style.css
# look for: high success rate, low p99 latency, cache HITs dominating
tail -f /var/log/nginx/brisk.access.log    # watch $upstream_cache_status = HIT
```
This is a smoke test of behavior under load, **not** a throughput benchmark (real RPS/Gbps numbers need the 10 Gbps production hardware — see the capacity figures in the spec).

---

# PART F — Security checklist (baseline for a public edge)
- **Origin not publicly exposed** — origin bound to localhost/private; only the edge reaches it. (Full origin lockdown — mTLS / secret pull header — is the dedicated security phase, but don't expose the origin IP now.)
- **Firewall** limited to 22/80/443; SSH **key‑only**, non‑root sudo.
- **No secrets in the repo or logs**; agent config perms locked down (`/etc/brisk` not world‑readable).
- **HSTS** present (already set). Brisk/`X-Brisk-*` headers don't leak Nginx version (`server_tokens off`, `Server: Brisk`).
- Optional: `fail2ban` for SSH.

---

# Pitfalls (do not skip)
1. **Staging before production, always** — production LE rate limit is ~5 certs / exact domain set / 7 days (refill 1 per ~34h). Iterate on staging; flip to prod only when the flow works. Use **ARI** (rate‑limit‑exempt renewals).
2. **HTTP‑01 needs port 80 open and reachable** — and DNS must already point to the VPS. Validation happens only on port 80.
3. **Verify nginx.org 1.30.x on the VPS** (not the 1.24 distro fallback seen in the sandbox) so modules ABI‑match and `http2 on;` is used.
4. **Module ABI lock** — `headers-more` + `ngx_brotli` are built against the exact Nginx version; a later Nginx upgrade requires rebuild (bootstrap handles via version‑stamp).
5. **Clock correctness** — ACME fails on skewed clocks; ensure NTP.
6. **Always `nginx -t` before reload**; keep the `.bak` rollback.
7. **Don't over‑load the small droplet** — capacity test is a smoke test, not the real benchmark.

---

# Definition of done — PHASE 1 COMPLETE ✅
- A real VPS serves `<YOUR_DOMAIN>` over **HTTPS / TLS 1.3** with a **browser‑trusted Let's Encrypt ECDSA** cert.
- Caching works over the public internet (MISS→HIT), HLS video (206 + short‑TTL playlist + CORS), **Brotli** on text, **`Server: Brisk`** + `X-Brisk-*` everywhere.
- **TCP BBR active** on the real kernel; tuning applied.
- **Survives reboot** (systemd auto‑start) and **auto‑renews** the cert via ARI.
- Baseline security in place (firewall, key‑only SSH, origin not exposed).

When all the above pass, **Phase 1 is done** — Brisk is a real, single‑node CDN edge.

# Next — Phase 2 (preview, do NOT start)
Control plane (`brisk-control`, Docker) + dashboard skeleton + the agent's **pull‑config from control plane** (with local persistence) + **instant purge** over a message channel (NATS) + **stats shipping** to Postgres/TimescaleDB. The Phase‑1 stub interfaces (`config.Source`, `purge.Purger`, `stats.Reporter`, `client.ControlPlane`) are where this plugs in. Wait for the user's go‑ahead and a Phase‑2 prompt.
