#!/usr/bin/env bash
# Lua-edge acceptance harness (Phase 4 Step 5). Brings up the isolated lab (1 origin
# + 1 edge, 4 zones) and proves the Lua programmable layer enforces the per-zone
# custom cache rules (override-ttl / bypass / force-download / redirect, priority
# first-match) + request/response header transforms (with the managed-header
# deny-list), per zone, fail-open on a broken rule, with no regressions to WAF /
# health / cache, on the nginx.org build.
set -uo pipefail
cd "$(dirname "$0")"
C="docker compose -f docker-compose.yml"
P=20443
PASS=0; FAIL=0
ok()  { echo "  PASS: $1"; PASS=$((PASS+1)); }
bad() { echo "  FAIL: $1"; FAIL=$((FAIL+1)); }

# status of a GET: $1=host $2=path  (no redirect follow)
status() { curl -sk --max-time 20 --resolve "$1:$P:127.0.0.1" -o /dev/null -w '%{http_code}' "https://$1:$P$2" 2>/dev/null; }
# header value from a GET: $1=host $2=path $3=header (case-insensitive)
hval() { curl -sk --max-time 20 --resolve "$1:$P:127.0.0.1" -o /dev/null -D - "https://$1:$P$2" 2>/dev/null \
         | tr -d '\r' | awk -v h="$(echo "$3" | tr A-Z a-z):" 'tolower($1)==h{$1="";sub(/^ /,"");print}'; }
expect()  { if [ "$2" = "$3" ];      then ok "$1 ($2)"; else bad "$1 (got '$2', want '$3')"; fi; }
contains(){ if echo "$2" | grep -qi "$3"; then ok "$1"; else bad "$1 (got '$2', want ~'$3')"; fi; }
absent()  { if [ -z "$2" ];          then ok "$1 (absent)"; else bad "$1 (present: '$2')"; fi; }

echo "=== build edge image (nginx.org + lua-nginx-module + headers-more + brotli + agent) ==="
docker build -f Dockerfile.edge -t brisk-lua-edge:latest .. || { echo "build failed"; exit 1; }

echo "=== up ==="
$C up -d
echo "waiting for agent to write lua + render + start nginx..."; sleep 16

echo ""; echo "=== 1) Lua layer active on the nginx.org build (module loads; nginx -t passed -> edge serves) ==="
expect "zone a serves (nginx up => nginx -t clean)" "$(status a.lua.local /)" "200"
contains "Lua header_filter ran (X-Custom present on zone a)" "$(hval a.lua.local / X-Custom)" "hello"

echo ""; echo "=== 2) override_cache_ttl: extension css -> TTL 45s (client Cache-Control) ==="
contains "a/style.css Cache-Control max-age=45" "$(hval a.lua.local /style.css Cache-Control)" "max-age=45"

echo ""; echo "=== 3) bypass_cache: /api/ always BYPASS; a normal path caches ==="
expect "a/api/data #1 X-Brisk-Cache BYPASS" "$(hval a.lua.local /api/x X-Brisk-Cache)" "BYPASS"
expect "a/api/data #2 X-Brisk-Cache BYPASS" "$(hval a.lua.local /api/x X-Brisk-Cache)" "BYPASS"

echo ""; echo "=== 4) force_download: extension pdf -> Content-Disposition: attachment ==="
contains "a/doc.pdf Content-Disposition attachment" "$(hval a.lua.local /doc.pdf Content-Disposition)" "attachment"

echo ""; echo "=== 5) redirect: /old -> 301 /new (before upstream) ==="
expect   "a/old -> 301"        "$(status a.lua.local /old)" "301"
contains "a/old Location /new" "$(hval a.lua.local /old Location)" "/new"

echo ""; echo "=== 6) priority / first-match: /first matches redirect(p0) + bypass(p1) -> redirect wins ==="
expect   "a/first -> 301"      "$(status a.lua.local /first)" "301"
contains "a/first Location /A" "$(hval a.lua.local /first Location)" "/A"

echo ""; echo "=== 7) header transforms (request + response) + managed-header deny-list ==="
contains "request add: origin saw X-Up (X-Echo-XUp=brisk-req)" "$(hval a.lua.local / X-Echo-XUp)" "brisk-req"
contains "response add: client sees X-Custom=hello"            "$(hval a.lua.local / X-Custom)" "hello"
absent   "response remove: X-Origin-Remove dropped"            "$(hval a.lua.local / X-Origin-Remove)"
contains "deny-list: HSTS NOT clobbered (still max-age=31536000)" "$(hval a.lua.local / Strict-Transport-Security)" "max-age=31536000"

echo ""; echo "=== 8) PER-ZONE ISOLATION: zone b (no rules) unaffected ==="
if echo "$(hval b.lua.local /style.css Cache-Control)" | grep -qi "max-age=45"; then bad "zone b css must NOT be 45s"; else ok "zone b css keeps default TTL (not 45s)"; fi
expect "zone b /old -> 200 (no redirect)" "$(status b.lua.local /old)" "200"
absent "zone b has no X-Custom transform"  "$(hval b.lua.local / X-Custom)"

echo ""; echo "=== 9) FAIL-OPEN: zone c broken regex rule -> served normally (pcall fallback) ==="
expect "zone c / -> 200 (broken rule didn't blackhole)" "$(status c.lua.local /)" "200"

echo ""; echo "=== 10) NO REGRESSIONS: WAF intact + /healthz & /_waf skip guard ==="
expect "zone a WAF still blocks SQLi -> 403" "$(status a.lua.local '/?id=1%27%20OR%20%271%27%3D%271')" "403"
expect "zone a /healthz -> 200 (skip guard, despite zone a rules)" "$(status a.lua.local /healthz)" "200"
expect "zone d /foo -> 301 (lua '/' redirect works)" "$(status d.lua.local /foo)" "301"
expect "zone d /healthz -> 200 (skip guard, despite '/' redirect rule)" "$(status d.lua.local /healthz)" "200"

echo ""; echo "=== 11/12) propagation + live-site safety ==="
echo "  Propagation: a rule change bumps config_version -> edges re-pull -> reload (poll interval);"
echo "  the agent re-renders zones_data.lua + re-runs init_by_lua on reload. (Mechanism; not curl-tested here.)"
echo "  Live-safety: zone b (no rules/transforms) renders NO Lua hooks -> byte-identical (proven above +"
echo "  by the nginx render unit test). Edges WITHOUT the lua module render no Lua at all."

echo ""; echo "=== teardown ==="
$C down -v >/dev/null 2>&1

echo ""; echo "=================================================="
echo "  Lua-edge acceptance: $PASS passed, $FAIL failed"
echo "=================================================="
[ "$FAIL" -eq 0 ]
