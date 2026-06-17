#!/usr/bin/env bash
# WAF + rate-limit acceptance harness (Phase 4 Step 4). Brings up the isolated lab
# (1 origin + 2 edges, 5 zones) and proves: per-zone enable, OWASP CRS SQLi/XSS
# block on the block zone + pass-through on the off zone (isolation), detect-mode
# would-block logging, custom-rule ordering, native rate limiting, the WordPress
# preset, the body-inspect cap, and the documented fail-open/closed policy.
set -uo pipefail
cd "$(dirname "$0")"
C="docker compose -f docker-compose.yml"
MAIN=19443      # edge-main  (a=block, b=off, d=detect)
FO=19444        # edge-failopen (fo=fail-open, fc=fail-closed)
PASS=0; FAIL=0
ok()  { echo "  PASS: $1"; PASS=$((PASS+1)); }
bad() { echo "  FAIL: $1"; FAIL=$((FAIL+1)); }

# status of a GET: $1=port $2=host $3=path [$4=ua]
status() {
  local ua="${4:-brisk-waf-lab/1.0}"
  curl -sk --max-time 20 -A "$ua" --resolve "$2:$1:127.0.0.1" -o /dev/null -w '%{http_code}' "https://$2:$1$3" 2>/dev/null
}
# expect: $1=label $2=actual $3=expected
expect() { if [ "$2" = "$3" ]; then ok "$1 ($2)"; else bad "$1 (got $2, want $3)"; fi; }

SQLI="/?id=1%27%20OR%20%271%27%3D%271"          # ?id=1' OR '1'='1  (CRS SQLi)
XSS="/?q=%3Cscript%3Ealert(1)%3C%2Fscript%3E"   # ?q=<script>alert(1)</script>  (CRS XSS)

echo "=== build edge image (nginx.org + modules + brisk-agent w/ Coraza) ==="
docker build -f Dockerfile.edge -t brisk-waf-edge:latest .. || { echo "build failed"; exit 1; }

echo "=== up ==="
$C up -d
echo "waiting for agents to render nginx + compile CRS + start..."; sleep 22

echo ""; echo "=== 1) topology ==="
echo "  edge-main:$MAIN  a=block b=off d=detect   |   edge-failopen:$FO  fo=fail-open fc=fail-closed"

echo ""; echo "=== 2) OWASP CRS blocks attacks on the BLOCK zone (a); clean passes ==="
expect "zone a: SQLi -> 403"  "$(status $MAIN a.brisk.local "$SQLI")" "403"
expect "zone a: XSS  -> 403"  "$(status $MAIN a.brisk.local "$XSS")"  "403"
expect "zone a: clean -> 200" "$(status $MAIN a.brisk.local "/")"     "200"

echo ""; echo "=== 3) PER-ZONE ISOLATION: the OFF zone (b) lets the same attacks through ==="
expect "zone b (off): SQLi -> 200" "$(status $MAIN b.brisk.local "$SQLI")" "200"
expect "zone b (off): XSS  -> 200" "$(status $MAIN b.brisk.local "$XSS")"  "200"

echo ""; echo "=== 4) DETECT mode (d): attacks are NOT enforced (200) but LOGGED as would-block ==="
expect "zone d (detect): SQLi -> 200" "$(status $MAIN d.brisk.local "$SQLI")" "200"
sleep 1
if $C logs edge-main 2>&1 | grep -q "zone=d.brisk.local action=detect"; then
  ok "zone d: would-block logged (action=detect)"
else
  bad "zone d: no 'action=detect' would-block line in agent log"
fi

echo ""; echo "=== 5) CUSTOM RULES + ordering (terminating short-circuit) ==="
expect "zone a: /admin/secret -> 403 (block rule)"      "$(status $MAIN a.brisk.local "/admin/secret")" "403"
expect "zone a: /admin/ok -> 200 (allow rule wins first)" "$(status $MAIN a.brisk.local "/admin/ok")"   "200"

echo ""; echo "=== 6) RATE LIMIT: wp_preset 5/min on /wp-login.php -> 6th = 429 ==="
codes=""
for i in $(seq 1 7); do codes="$codes $(status $MAIN a.brisk.local "/wp-login.php")"; done
echo "  /wp-login.php codes:$codes"
first=$(echo $codes | awk '{print $1}')
expect "wp-login: 1st request -> 200" "$first" "200"
if echo "$codes" | grep -q "429"; then ok "wp-login: a later request was rate-limited (429)"; else bad "wp-login: never hit 429"; fi
expect "other path /about -> 200 (rate limit is path-scoped)" "$(status $MAIN a.brisk.local "/about")" "200"

echo ""; echo "=== 7) WordPress preset: block /xmlrpc.php + scanner UA ==="
expect "zone a: /xmlrpc.php -> 403"           "$(status $MAIN a.brisk.local "/xmlrpc.php")"        "403"
expect "zone a: scanner UA (sqlmap) -> 403"   "$(status $MAIN a.brisk.local "/" "sqlmap/1.5.2")"  "403"
expect "zone a: normal UA (curl) -> 200"      "$(status $MAIN a.brisk.local "/" "curl/8.0")"      "200"

echo ""; echo "=== 8) BODY-INSPECT CAP: a SQLi in the POST BODY is NOT scanned (clean URI passes) ==="
# ~200 KB body containing a SQLi string; URI is clean. WAF inspects URI+headers
# only (auth_request body off), so this is allowed (200) — proving bodies aren't
# deep-scanned. The same payload in the QUERY STRING is blocked (test 2).
bodyfile=$(mktemp); { printf "user=admin&q="; head -c 200000 /dev/zero | tr '\0' 'A'; printf "%s" "' OR '1'='1"; } > "$bodyfile"
bodycode=$(curl -sk --max-time 20 --resolve "a.brisk.local:$MAIN:127.0.0.1" -o /dev/null -w '%{http_code}' \
  -X POST --data-binary "@$bodyfile" "https://a.brisk.local:$MAIN/submit" 2>/dev/null)
rm -f "$bodyfile"
expect "zone a: SQLi in 200KB body -> 200 (body not deep-scanned)" "$bodycode" "200"

echo ""; echo "=== 9) FAIL POLICY: WAF service down (unbindable waf_listen) ==="
expect "fo (fail-open):  SQLi -> 200 (allowed, availability)" "$(status $FO fo.brisk.local "$SQLI")" "200"
expect "fc (fail-closed): clean -> 500 (blocked, no WAF)"     "$(status $FO fc.brisk.local "/")"     "500"

echo ""; echo "=== 10) NO REGRESSIONS: healthz + default_server + the OFF zone serve normally ==="
expect "edge-main /healthz (default_server, no SNI match) -> 200" "$(status $MAIN nomatch.brisk.local "/healthz")" "200"
expect "zone b (off) clean -> 200" "$(status $MAIN b.brisk.local "/")" "200"

echo ""; echo "=== 11) RBAC + security-events pipeline ==="
echo "  NOTE: this standalone data-plane lab has no control plane. RBAC (a customer"
echo "  manages only its own zone's WAF) is enforced by the control plane's scopeZone"
echo "  chokepoint, and the firewall-log pipeline (agent ships -> /agent/security-events"
echo "  -> GET /zones/{id}/security-events + admin /security-events) is exercised by the"
echo "  brisk-control build/tests. Here the SAME events are visible in the agent log:"
$C logs edge-main 2>&1 | grep -E "waf: zone=(a|d)\.brisk\.local action=" | tail -4 | sed 's/^/    /'

echo ""; echo "=== teardown ==="
$C down -v >/dev/null 2>&1

echo ""; echo "=================================================="
echo "  WAF acceptance: $PASS passed, $FAIL failed"
echo "=================================================="
[ "$FAIL" -eq 0 ]
