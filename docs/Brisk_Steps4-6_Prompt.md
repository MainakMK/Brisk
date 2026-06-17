# Brisk CDN — Phase 1 / Steps 4–6 Build Prompt

**For Claude Code.** Context already in the repo: `CLAUDE.md` (rules) + `docs/Brisk_Phase1_Build_Spec.md` (full plan) + `Brisk_Step3_Prompt.md`. **Steps 1–3 are done and verified** (repo skeleton, config, `nginx.go` + templates, HTTP caching MISS→HIT, `headers-more` built → `Server: Brisk` + `X-Brisk-*` on all responses, HLS/video with slice+coalescing, CORS, short‑TTL playlists). Build is clean, static binary, `nginx -t` passing.

> **Read `CLAUDE.md` and `docs/Brisk_Phase1_Build_Spec.md` first.** Then do Steps 4, 5, 6 **in order**, committing per step and running each step's tests before moving on. Do **not** start Step 7 (end‑to‑end) or any VPS work until 4–6 pass locally and you've given me the results.

**End state after 4–6:** the edge serves real **HTTPS (TLS 1.3, self‑signed locally)**, with **Brotli** compression and full **kernel + Nginx tuning**, installed by a **one‑command idempotent bootstrap** that registers **systemd** services so the whole edge **comes back automatically after a reboot**.

---

# STEP 4 — TLS (self‑signed local; Let's Encrypt hook for production)

## 4.1 `tls.go` — two modes
Implement a `tls` package with modes selected from `agent.yaml` per zone:
- **`selfsigned`** (local dev default) — generate an **ECDSA P‑256** cert + key **natively in Go** (`crypto/ecdsa` + `crypto/x509`, `elliptic.P256()`), self‑signed, with the zone domain in **Subject Alternative Name** (SAN — modern clients ignore CN). Write to `/etc/brisk/tls/<domain>/fullchain.pem` and `privkey.pem`. Regenerate only if missing or expired (idempotent).
- **`mkcert`** (optional local, no browser warnings) — if `mkcert` is installed, shell out to it to produce a locally‑trusted cert. Document install: `mkcert -install` then `mkcert <domain>`.
- **`letsencrypt`** (production, runs only on a real public VPS) — obtain/renew a real **ECDSA** cert via a **Go ACME library** (recommended: `github.com/go-acme/lego` or `github.com/caddyserver/certmagic`) using HTTP‑01 (or DNS‑01 later via Bunny DNS). Write to the same paths and reload Nginx; schedule auto‑renewal well before the ~90‑day expiry. **Build this path now but it's only exercised on the VPS** (Let's Encrypt must reach the public domain — impossible on localhost).

Keep the existing `config.Source` flow; `tls.go` runs after config load, before the first Nginx reload.

## 4.2 Nginx TLS config (Mozilla Intermediate, current best practice)
Emit into the server template. **This switches the server to 443; keep the existing 80→443 redirect.**
```nginx
server {
    listen 443 ssl;
    http2 on;
    server_name TEST_DOMAIN;

    ssl_certificate     /etc/brisk/tls/TEST_DOMAIN/fullchain.pem;
    ssl_certificate_key /etc/brisk/tls/TEST_DOMAIN/privkey.pem;

    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_ecdh_curve X25519:prime256v1:secp384r1;
    ssl_ciphers ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES256-GCM-SHA384:ECDHE-RSA-AES256-GCM-SHA384:ECDHE-ECDSA-CHACHA20-POLY1305:ECDHE-RSA-CHACHA20-POLY1305;
    ssl_prefer_server_ciphers off;          # let client pick (better ChaCha20 on mobile)

    # session resumption = big handshake savings for a CDN's returning clients
    ssl_session_cache shared:BriskTLS:50m;   # ~200k sessions
    ssl_session_timeout 1d;
    ssl_session_tickets on;                  # TUNABLE: on = faster resume; off = stricter forward secrecy

    more_set_headers 'Strict-Transport-Security: max-age=31536000';
    # ... existing more_set_headers (Server: Brisk, X-Brisk-*), locations, cache, video ...
}
```

## 4.3 IMPORTANT update vs the earlier spec — drop OCSP stapling
The Phase 1 spec/CLAUDE.md mentioned `ssl_stapling`. **Remove it.** Current reality: OCSP stapling **has no effect with Let's Encrypt** (Let's Encrypt is ending OCSP in favor of CRLs), and it's meaningless for self‑signed certs. Only commercial CAs benefit. So **do not enable `ssl_stapling`** in Brisk. (No `resolver` needed for stapling either.) Also: a custom `ssl_dhparam` file is **not** needed with this modern ECDHE‑only profile — don't generate one.

## 4.4 Step 4 tests
```bash
# TLS 1.3 negotiated
echo | openssl s_client -connect localhost:443 -servername test.example.com 2>/dev/null | grep -i 'Protocol\|Cipher'
# Cert is ECDSA + SAN matches domain
echo | openssl s_client -connect localhost:443 -servername test.example.com 2>/dev/null | openssl x509 -noout -text | grep -iA1 'Public Key Algorithm\|Subject Alternative'
# HTTP -> HTTPS redirect
curl -sI http://test.example.com/ | grep -i '301\|location'
# Cached HTTPS still works (MISS->HIT) over TLS
curl -ksI https://test.example.com/style.css | grep -i x-brisk-cache   # MISS then HIT
```
**Done when:** HTTPS serves over **TLS 1.3** with an **ECDSA** cert whose SAN matches the domain, HTTP redirects to HTTPS, and caching + Brisk headers still work over TLS. (With `mkcert`, the browser shows no warning; with raw self‑signed, use `-k`/expect a warning — that's normal locally.)

---

# STEP 5 — Edge tuning + Brotli

## 5.1 Build & load `ngx_brotli` (same dynamic‑module pattern as headers‑more)
```bash
NGINX_VER=$(nginx -v 2>&1 | grep -oP '[0-9]+\.[0-9]+\.[0-9]+')
cd /usr/local/src
git clone --recurse-submodules https://github.com/google/ngx_brotli.git
# (reuse the matching nginx-${NGINX_VER} source already fetched for headers-more)
cd nginx-${NGINX_VER}
./configure --with-compat --add-dynamic-module=../ngx_brotli --add-dynamic-module=../headers-more-nginx-module
make modules
cp objs/ngx_http_brotli_*.so /etc/nginx/modules/
```
Load in the **main context** (alongside the headers‑more load line):
```nginx
load_module modules/ngx_http_brotli_filter_module.so;
load_module modules/ngx_http_brotli_static_module.so;
```
> Alternative on Ubuntu: `apt install -y libnginx-mod-brotli` — but only if it matches the installed Nginx build. We use official nginx.org packages, so **building the module is the consistent path**. `bootstrap.go` owns this (idempotent; ABI‑locked to the Nginx version via the version‑stamp file already added in Step 3).

## 5.2 Brotli + Gzip config (http context)
```nginx
# Brotli (primary)
brotli on;
brotli_static on;            # serve pre-compressed .br files if present
brotli_comp_level 5;         # DYNAMIC on-the-fly: 4–6 sweet spot (CPU vs ratio). DO NOT use 11 here.
brotli_min_length 256;
brotli_buffers 16 8k;
brotli_window 512k;
brotli_types text/plain text/css text/javascript application/javascript application/json application/xml image/svg+xml application/manifest+json font/ttf font/otf;

# Gzip (fallback for clients without Brotli)
gzip on;
gzip_vary on;
gzip_comp_level 5;
gzip_min_length 256;
gzip_proxied any;
gzip_types text/plain text/css text/javascript application/javascript application/json application/xml image/svg+xml;
```
**Rules:** compress **text only** (HTML/CSS/JS/JSON/XML/SVG/fonts). **Never compress images/video** (`.jpg/.png/.webp/.mp4/.ts` are already compressed — re‑compressing wastes CPU). Use level **11 only for `brotli_static`** pre‑compressed assets, never for on‑the‑fly.

## 5.3 Kernel tuning — `/etc/sysctl.d/99-brisk.conf`
```ini
# congestion control (Google BBR) + fair queueing
net.core.default_qdisc            = fq
net.ipv4.tcp_congestion_control   = bbr
# big autotuning buffers for 10 Gbps / high-bandwidth paths
net.core.rmem_max                 = 33554432
net.core.wmem_max                 = 33554432
net.ipv4.tcp_rmem                 = 4096 131072 33554432
net.ipv4.tcp_wmem                 = 4096 131072 33554432
# queues / backlogs for bursty connection storms
net.core.somaxconn                = 65535
net.ipv4.tcp_max_syn_backlog      = 65536
net.core.netdev_max_backlog       = 250000
# connection churn + fast open
net.ipv4.tcp_fastopen             = 3
net.ipv4.tcp_fin_timeout          = 15
net.ipv4.tcp_tw_reuse             = 1
net.ipv4.ip_local_port_range      = 1024 65000
net.ipv4.tcp_slow_start_after_idle= 0
fs.file-max                       = 2000000
```
Apply: `sysctl --system`. Verify: `sysctl net.ipv4.tcp_congestion_control` → `bbr`.

## 5.4 Nginx worker / IO tuning (http + main context)
```nginx
worker_processes auto;
worker_rlimit_nofile 200000;
events { worker_connections 32768; multi_accept on; }     # main+events context

# inside http{}
sendfile on; tcp_nopush on; tcp_nodelay on;
aio threads;
keepalive_timeout 65; keepalive_requests 10000;
open_file_cache max=200000 inactive=60s;
open_file_cache_valid 60s; open_file_cache_min_uses 2; open_file_cache_errors on;
```

## 5.5 Caveats (must respect)
- **BBR cannot be set inside a plain Docker container** — congestion control is governed by the **host kernel**. So locally you can *write* the sysctl file, but **validate BBR on the real VPS** (or in WSL2 with a BBR‑capable kernel). Note this in output; don't fail the step if Docker won't switch to `bbr`.
- **Tune as controlled experiments** — these values suit high‑throughput edges, but measure before/after on the VPS; BBR fairness varies across kernels.

## 5.6 Step 5 tests
```bash
# Brotli on text
curl -ksI -H 'Accept-Encoding: br'   https://test.example.com/app.js  | grep -i content-encoding   # br
# Gzip fallback when Brotli not offered
curl -ksI -H 'Accept-Encoding: gzip' https://test.example.com/app.js  | grep -i content-encoding   # gzip
# Images are NOT compressed
curl -ksI -H 'Accept-Encoding: br'   https://test.example.com/logo.png | grep -i content-encoding   # (none)
# sysctl applied (on VPS/WSL2)
sysctl net.ipv4.tcp_congestion_control                                  # bbr
nginx -t                                                                # syntax ok
```
**Done when:** text assets return `Content-Encoding: br` (gzip fallback works), images are left uncompressed, the sysctl file applies (BBR active on VPS/WSL2), and `nginx -t` passes.

---

# STEP 6 — Bootstrap + systemd (survives reboot)

## 6.1 `bootstrap.go` — full idempotent installer
A single command (`brisk-agent --bootstrap` or a `bootstrap` subcommand) that, run on a fresh Ubuntu 24.04 box, makes it a working Brisk edge. Each action **checks before doing** (safe to re‑run):
1. Detect OS + add official **nginx.org** repo; install **Nginx stable 1.30.x** + build deps (`build-essential`, `libpcre3-dev`, `zlib1g-dev`, `libssl-dev`, `git`).
2. Fetch matching `nginx-${VER}` source; build & install dynamic modules **`headers-more`** + **`ngx_brotli`** (skip if the `.so` already matches `${VER}` via the version‑stamp file). Ensure the **slice module** is present (it is, in nginx.org packages).
3. Write `/etc/sysctl.d/99-brisk.conf` and run `sysctl --system`.
4. Create dirs: `/var/cache/brisk`, `/etc/brisk/tls`, `/var/log/nginx`, `/etc/brisk`.
5. Install the `brisk-agent` binary to `/usr/local/bin/`.
6. Generate initial config + TLS (self‑signed locally), `nginx -t`, then start.
7. Install + **enable** the systemd unit; ensure `nginx` is enabled too.

## 6.2 systemd unit — `deploy/brisk-agent.service`
```ini
[Unit]
Description=Brisk Edge Agent
After=network-online.target nginx.service
Wants=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/bin/brisk-agent --config /etc/brisk/agent.yaml
Restart=always
RestartSec=3
LimitNOFILE=1000000

[Install]
WantedBy=multi-user.target
```
Enable both so the edge self‑heals on boot:
```bash
systemctl daemon-reload
systemctl enable --now nginx
systemctl enable --now brisk-agent
```

## 6.3 "Survives reboot" — where to actually test it
- **Plain Docker containers do NOT run systemd** by default, so a true reboot test there is unreliable. Test reboot survival in **WSL2 with systemd enabled** (`/etc/wsl.conf` → `[boot] systemd=true`, then `wsl --shutdown` and reopen) **or** on the real **VPS** (`reboot`).
- In a Docker container you can still validate the **install + service definition + manual start** (`systemctl` works only if the image runs systemd; otherwise start the binary directly to confirm it boots, generates config, and serves).

## 6.4 Step 6 tests
```bash
# Fresh box -> one command -> everything up
sudo /usr/local/bin/brisk-agent --bootstrap
systemctl is-active nginx brisk-agent          # both: active
curl -ksI https://test.example.com/style.css | grep -i x-brisk-cache    # serving (MISS->HIT)

# Idempotent: re-run causes no errors and no duplicate work
sudo /usr/local/bin/brisk-agent --bootstrap    # exits clean, "already installed" messages

# Survives reboot (WSL2 or VPS)
sudo reboot
# after it comes back:
systemctl is-active nginx brisk-agent          # both: active automatically
curl -ksI https://test.example.com/ | grep -i 'server:'                 # Server: Brisk
```
**Done when:** a fresh box becomes a working HTTPS‑caching Brisk edge from **one command**, re‑running bootstrap is harmless (idempotent), and after a **reboot** both `nginx` and `brisk-agent` auto‑start and resume serving.

---

# Combined acceptance (Phase 1 nearly complete after this)
All Step 4/5/6 tests pass locally. The edge now: caches static + HLS video, brands every response as Brisk, serves **HTTPS/TLS 1.3 (ECDSA)**, **Brotli‑compresses** text, is **kernel‑tuned**, and **auto‑recovers on reboot** via systemd — installed by **one idempotent command**.

# Pitfalls (do not skip)
1. **Module ABI lock** — `headers-more` AND `ngx_brotli` are tied to the exact Nginx version; rebuild both on any Nginx upgrade or Nginx won't start. `bootstrap.go` handles via the version‑stamp.
2. **No OCSP stapling** — drop it (no effect with Let's Encrypt; meaningless for self‑signed). No `ssl_dhparam` either.
3. **Self‑signed → browser warnings** are expected locally; use `mkcert` for a trusted local cert, or `curl -k`.
4. **BBR in Docker** — host kernel governs it; validate BBR on VPS/WSL2, not plain Docker.
5. **systemd in Docker** — not default; test reboot survival in WSL2 (systemd=true) or VPS.
6. **Don't compress images/video**; Brotli/Gzip text only; `comp_level` 4–6 dynamic, 11 only for `brotli_static`.
7. **Always `nginx -t` before `nginx -s reload`**; roll back to the `.bak` on failure (keep the Step‑2 behavior).
8. **Session tickets trade‑off** — `on` = faster resumption (good for a CDN), `off` = stricter forward secrecy. Expose as a tunable; default `on`.

# Config additions (`agent.yaml`)
```yaml
tls_mode: "selfsigned"        # selfsigned | mkcert | letsencrypt
letsencrypt_email: ""          # required when tls_mode=letsencrypt (VPS)
session_tickets: true
brotli_comp_level: 5
```

# Next (after 4–6 pass)
**Step 7 — end‑to‑end local test** of the whole Phase 1 edge in Docker/WSL2, then deploy to **one cheap VPS** with `tls_mode: letsencrypt` to validate a real cert + public access + BBR. That completes **Phase 1**. Then we plan **Phase 2** (control plane + dashboard). Do not start Step 7 until you give the go‑ahead.
