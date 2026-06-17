# Brisk CDN — Phase 1 / Step 3 Build Prompt

**For Claude Code.** Context: `CLAUDE.md` (rules) + `docs/Brisk_Phase1_Build_Spec.md` (full plan) are already in the repo. **Steps 1 & 2 are done and verified** (repo skeleton, `config.go`, `nginx.go` + templates, HTTP caching with MISS→HIT, static 5.7 MB binary, `nginx -t` passing). The slice/HLS locations and `proxy_cache_lock` are already scaffolded in the templates.

> **Read `CLAUDE.md` and `docs/Brisk_Phase1_Build_Spec.md` first.** Then do Step 3 exactly as below. Work in small commits and run the acceptance tests at the end before declaring done.

---

## Step 3 goal (one line)

Make the edge fully **branded and video‑correct**: build the **`headers-more`** module so the `Server` header becomes **`Brisk`**, serve all `X-Brisk-*` headers consistently (including on errors), finalize **HLS/video caching** (MPEG‑TS *and* fMP4/CMAF), tune **request coalescing** for large segments, and verify with **206 range** + **playlist** + **coalescing** tests.

---

## Task 1 — Build & load the `headers-more` module

**Why:** the built‑in `add_header` cannot override the protected `Server` header (it only adds, and only on 2xx/3xx). The OpenResty **`headers-more-nginx-module`** can set/replace/clear any header, including `Server`, on all responses. It is the standard tool every CDN uses to brand the `Server` header.

**Build as a dynamic module against the EXACT installed Nginx version** (module ABI is locked to the build):
```bash
# install build deps + matching nginx source (stable 1.30.x — must match the installed package)
NGINX_VER=$(nginx -v 2>&1 | grep -oP '[0-9]+\.[0-9]+\.[0-9]+')
cd /usr/local/src
wget https://nginx.org/download/nginx-${NGINX_VER}.tar.gz && tar xzf nginx-${NGINX_VER}.tar.gz
git clone --depth=1 https://github.com/openresty/headers-more-nginx-module.git
cd nginx-${NGINX_VER}
./configure --with-compat --add-dynamic-module=../headers-more-nginx-module
make modules
cp objs/ngx_http_headers_more_filter_module.so /etc/nginx/modules/
```
Then load it in the **main (top‑level) context** of `nginx.conf` (the agent template must emit this near the existing `ngx_brotli` loads):
```nginx
load_module modules/ngx_http_headers_more_filter_module.so;
```
- The `bootstrap.go` installer must do this build step (idempotent — skip if the `.so` already matches the running Nginx version).
- Pin/record the Nginx version; if Nginx is upgraded, the module must be rebuilt (note this in `bootstrap.go`).

## Task 2 — Convert ALL branded headers to `more_set_headers`

**Why:** `add_header` has an inheritance trap — if any `location` block uses its own `add_header` (or `more_set_headers`), the parent/server `add_header` directives are **dropped** for that location. To guarantee the Brisk headers appear **everywhere and on every status (including 4xx/5xx)**, set them all with `more_set_headers` at the `server` level (and they'll apply consistently).

In the `server.tmpl`, replace the `add_header X-Brisk-*` lines with:
```nginx
more_set_headers 'Server: Brisk';
more_set_headers 'X-Brisk-Edge: EDGE_ID';            # filled from agent.yaml edge_id
more_set_headers 'X-Brisk-Cache: $upstream_cache_status';   # HIT / MISS / EXPIRED / BYPASS / UPDATING
more_set_headers 'X-Brisk-Request-Id: $request_id';
more_set_headers 'Strict-Transport-Security: max-age=31536000';
```
- `more_set_headers` accepts Nginx variables in values (so `$upstream_cache_status` and `$request_id` work).
- Keep `server_tokens off` as well.

## Task 3 — Finalize HLS / video caching (MPEG‑TS *and* fMP4/CMAF)

Modern HLS comes in two segment formats — handle both:
- **MPEG‑TS:** `.ts` segments, MIME `video/mp2t`.
- **fMP4 / CMAF:** `.m4s` segments + an **init segment** (`init.mp4` / `.mp4`), MIME `video/mp4`. (Apple requires fMP4 for HEVC, so this is common for 4K/HEVC.)
- **DASH (optional, future):** `.mpd` manifest + `.m4s`.

**Add correct MIME types** (http or server context):
```nginx
types {
    application/vnd.apple.mpegurl  m3u8;
    application/dash+xml           mpd;
    video/mp2t                     ts;
    video/mp4                      mp4 m4s;
}
```

**Add CORS** — HLS/DASH players (hls.js, Shaka, native) require CORS headers or playback fails cross‑origin:
```nginx
more_set_headers 'Access-Control-Allow-Origin: *';
more_set_headers 'Access-Control-Allow-Headers: Range';
more_set_headers 'Access-Control-Expose-Headers: Content-Length, Content-Range';
```
(For Phase 1 a wildcard `*` is fine; we'll make the allowed origin per‑zone configurable later.)

**Segments + media files (`.ts`, `.m4s`, `.mp4`)** — slice + cache (this location already exists; confirm/adjust):
```nginx
location ~* \.(ts|m4s|mp4)$ {
    slice 1m;                                   # requires slice module (present in nginx.org pkg)
    proxy_cache brisk_cache;
    proxy_cache_key $host$uri$is_args$args$slice_range;   # $slice_range is REQUIRED in the key
    proxy_set_header Range $slice_range;
    proxy_http_version 1.1;                     # byte-range needs HTTP/1.1 to origin
    proxy_cache_valid 200 206 SEGMENT_TTL;      # VOD: 12h+, live: short (e.g. 10s)
    proxy_pass http://brisk_origin;
}
```
> **Rule:** with slice caching the source file must be **immutable** while cached (Nginx validates the ETag; if the origin file changes mid‑fill, the cache fill aborts). Fine for finished VOD; for replaced videos, purge or version the URL.

## Task 4 — Playlist (`.m3u8` / `.mpd`) handling — REFINEMENT to the spec

The original spec said "never cache `.m3u8` (BYPASS)." Research shows a better, configurable approach:
- **VOD (finished videos):** the playlist is static → cache it (longer TTL) like any file.
- **Live / frequently replaced:** cache with a **very short TTL** (≈ segment duration, e.g. 1–10s) so origin isn't hammered but viewers still get fresh playlists. Short‑TTL caching also lets `proxy_cache_lock` coalesce the playlist requests.

Make it per‑zone configurable (`agent.yaml`: `playlist_ttl`, default short for safety). Template:
```nginx
location ~* \.(m3u8|mpd)$ {
    proxy_cache brisk_cache;
    proxy_cache_key $host$uri$is_args$args;
    proxy_cache_valid 200 PLAYLIST_TTL;         # default 2s (live) ... set 1h+ for VOD
    proxy_pass http://brisk_origin;
}
```
> If a zone is pure VOD, set `playlist_ttl` long. If live or unknown, keep it short (≈2–4s). `X-Brisk-Cache` will then show HIT/MISS/EXPIRED rather than a hard BYPASS — update the acceptance test accordingly (see Task 6).

## Task 5 — Tune request coalescing for video

`proxy_cache_lock` is already enabled at the http level. For **large video segments** the default 5s lock window is too short (a 5 MB segment can take longer to pull), so override inside the segment location with longer values:
```nginx
proxy_cache_lock on;
proxy_cache_lock_age 30s;        # let the first request keep filling for up to 30s
proxy_cache_lock_timeout 30s;    # others wait up to 30s, then fetch directly
proxy_cache_use_stale updating error timeout;   # serve stale while refreshing
proxy_cache_background_update on;
```
- With the slice module, the lock works **per slice** (each `$slice_range` is its own key), so a wave of viewers requesting the same popular segment collapses to one origin pull per slice. This is exactly Bunny‑style coalescing.
- **Tunable to expose in `agent.yaml`:** `proxy_cache_min_uses` (default `1` = cache segments on first hit, best for a CDN; set `2` to reduce write pressure / avoid caching one‑off long‑tail content). Document the trade‑off.

## Task 6 — Update agent config & templates

Add to `agent.yaml` per‑zone (with sane defaults so existing config still works):
```yaml
zones:
  - domain: "test.example.com"
    origin: "http://origin:8000"
    tls: "selfsigned"
    video: true               # enable HLS/video locations
    profile: "vod"            # vod | live  -> sets playlist_ttl & segment_ttl defaults
    playlist_ttl: "2s"        # optional override
    segment_ttl: "12h"        # optional override
    cors_origin: "*"          # optional
    min_uses: 1
```
`nginx.go` renders these into the templates. Keep the existing static/`location /` block unchanged.

---

## Acceptance tests (Step 3 definition of done)

Run against the Docker test origin (serve a small HLS asset — both a `.ts` set and an fMP4 `.m4s`+`init.mp4` set if possible):
```bash
# 1) Branding: Server is now Brisk (not nginx), on success AND on a 404
curl -ksI https://test.example.com/ | grep -i '^server:'              # Server: Brisk
curl -ksI https://test.example.com/nope-404 | grep -iE 'server:|x-brisk-'   # Brisk headers present on 404 too

# 2) Brisk headers on a normal hit
curl -ksI https://test.example.com/style.css | grep -i 'x-brisk-'     # Edge, Cache, Request-Id all present

# 3) Range / 206 on a segment
curl -ksI -H 'Range: bytes=0-1048575' https://test.example.com/video/seg1.ts | head -1   # HTTP/.. 206

# 4) fMP4 init + segment cache (MISS then HIT)
curl -ksI https://test.example.com/video/init.mp4 | grep -i x-brisk-cache   # MISS
curl -ksI https://test.example.com/video/init.mp4 | grep -i x-brisk-cache   # HIT
curl -ksI https://test.example.com/video/seg1.m4s | grep -i x-brisk-cache   # MISS then HIT on repeat

# 5) Playlist behavior (short-TTL cache, NOT a hard bypass)
curl -ksI https://test.example.com/video/index.m3u8 | grep -iE 'x-brisk-cache|cache-control'

# 6) CORS present on video
curl -ksI https://test.example.com/video/seg1.ts | grep -i 'access-control-allow-origin'   # *

# 7) Coalescing: 50 concurrent misses on the same segment -> ~1 origin pull (check origin access log)
seq 50 | xargs -P50 -I{} curl -ks https://test.example.com/video/seg1.ts -o /dev/null
```
**Done when:** `Server: Brisk` shows everywhere (incl. errors), all `X-Brisk-*` headers present, 206 works, init+segments cache (MISS→HIT), playlist behaves per profile, CORS present, and the coalescing test shows a single origin pull for the burst.

---

## Pitfalls to avoid (do not skip)

1. **Module ABI lock** — `headers-more` (and `ngx_brotli`) MUST be built against the exact installed Nginx version. On any Nginx upgrade, rebuild the modules or Nginx won't start. `bootstrap.go` must handle this.
2. **`add_header` inheritance** — do not mix `add_header` and `more_set_headers` in a way that drops headers in nested locations. Use `more_set_headers` for all Brisk headers, set at `server` level.
3. **Slice immutability** — sliced files must not change while cached; for replaced videos, purge/version the URL.
4. **Byte‑range to origin needs HTTP/1.1** — keep `proxy_http_version 1.1;` in segment locations.
5. **`$slice_range` in the cache key** — omitting it corrupts sliced caching.
6. **Never reload on a bad config** — `nginx -t` first; only `nginx -s reload` on success; roll back to the `.bak` on failure (already implemented in Step 2 — keep it).
7. **CORS on errors too** — players need CORS even on 4xx; `more_set_headers` covers this (unlike `add_header`).

## Forward note (don't build yet)
Purge over the network and shipping stats remain Phase 2 (stubs already exist: `purge.Purger`, `stats.Reporter`). Open‑source Nginx has no built‑in purge directive — Brisk will purge by deleting the cache file (or via the `ngx_cache_purge` community module). Leave that for Phase 2.

---

**After Step 3 passes, the next step is Step 4 — `tls.go` (self‑signed local) → real HTTPS / TLS 1.3.** Don't start it until Step 3's tests pass.
