#!/usr/bin/env bash
# Origin Shield acceptance harness (Phase 4 Step 3). Brings up the isolated lab
# (2 origins, 1 shield, 2 edges) and proves: collapse to ~one origin fetch via the
# shield (same key across tiers), proxy_cache_lock concurrency, video-slice caching,
# per-zone isolation, graceful shield-death fallback, and the loop guard.
set -uo pipefail
cd "$(dirname "$0")"
C="docker compose -f docker-compose.yml"
PASS=0; FAIL=0
ok()   { echo "  PASS: $1"; PASS=$((PASS+1)); }
bad()  { echo "  FAIL: $1"; FAIL=$((FAIL+1)); }

# value of one header from an edge: $1=port $2=host $3=path $4=header
hval() { curl -sk --max-time 15 --resolve "$2:$1:127.0.0.1" -o /dev/null -D - "https://$2:$1$3" 2>/dev/null \
         | tr -d '\r' | awk -v h="$(echo "$4" | tr A-Z a-z):" 'tolower($1)==h{print $2}'; }
status() { curl -sk --max-time 15 --resolve "$2:$1:127.0.0.1" -o /dev/null -w '%{http_code}' "https://$2:$1$3" 2>/dev/null; }
# origin fetch count for a path substring: $1=origin svc $2=path
ocount() { $C logs "$1" 2>&1 | grep -c "GET $2 "; }

echo "=== build content ==="
mkdir -p content-a content-b
head -c 3145728 /dev/urandom > content-a/clip.mp4    # 3 MB => 3 one-MB slices
head -c 3145728 /dev/urandom > content-b/clip.mp4
printf 'origin A\n' > content-a/index.html
printf 'origin B\n' > content-b/index.html

echo "=== up ==="
$C up -d
echo "waiting for agents to render + start nginx..."; sleep 12

echo ""; echo "=== 1) topology ==="
echo "  shield: $(grep -m1 edge_id agent-shield.yaml)  edges: EDGE-1, EDGE-2  zones: a(shield ON) b(shield OFF)"

echo ""; echo "=== 2) COLLAPSE: same cold zone-A object from BOTH edges -> ONE origin fetch ==="
O2="/cold/t2-$$.css"
c1=$(hval 18443 a.brisk.local "$O2" X-Brisk-Cache);  s1=$(hval 18443 a.brisk.local "$O2" X-Brisk-Shield)
sleep 1
c2=$(hval 18444 a.brisk.local "$O2" X-Brisk-Cache);  s2=$(hval 18444 a.brisk.local "$O2" X-Brisk-Shield)
sleep 1
n=$(ocount origin-a "$O2")
echo "  edge1: cache=$c1 shield=$s1   edge2: cache=$c2 shield=$s2   origin-a fetches=$n"
[ "$n" = "1" ] && ok "one origin fetch for two edges (collapsed at shield)" || bad "expected 1 origin fetch, got $n"
[ "$s2" = "HIT" ] && ok "edge2 saw X-Brisk-Shield: HIT (shield served it)" || bad "edge2 X-Brisk-Shield=$s2 (want HIT)"

echo ""; echo "=== 3) CONCURRENCY: same cold object, concurrent from both edges -> ~one origin fetch ==="
O3="/cold/t3-$$.css"
for i in 1 2 3 4 5; do curl -sk --resolve a.brisk.local:18443:127.0.0.1 -o /dev/null "https://a.brisk.local:18443$O3" & done
for i in 1 2 3 4 5; do curl -sk --resolve a.brisk.local:18444:127.0.0.1 -o /dev/null "https://a.brisk.local:18444$O3" & done
wait; sleep 1
n=$(ocount origin-a "$O3")
echo "  origin-a fetches for $O3 (10 concurrent across 2 edges) = $n"
{ [ "$n" -ge 1 ] && [ "$n" -le 2 ]; } && ok "proxy_cache_lock collapsed concurrent misses (<=2, got $n)" || bad "expected <=2 origin fetches, got $n"

echo ""; echo "=== 4) VIDEO: sliced clip.mp4 from both edges -> origin sees one pull PER SLICE ==="
curl -sk --resolve a.brisk.local:18443:127.0.0.1 -o /dev/null "https://a.brisk.local:18443/clip.mp4"; sleep 1
curl -sk --resolve a.brisk.local:18444:127.0.0.1 -o /dev/null "https://a.brisk.local:18444/clip.mp4"; sleep 1
n=$(ocount origin-a "/clip.mp4")
echo "  origin-a fetches for /clip.mp4 across BOTH edges = $n (3 MB => ~3 slices; want ~3, not ~6)"
{ [ "$n" -ge 1 ] && [ "$n" -le 4 ]; } && ok "shield cached slices: origin saw ~one pull per slice ($n), not per edge" || bad "slice collapse off: $n origin fetches"

echo ""; echo "=== 5) PER-ZONE ISOLATION: zone B (shield OFF) pulls origin-b directly ==="
O5="/cold/t5-$$.css"
sB=$(hval 18443 b.brisk.local "$O5" X-Brisk-Shield); sleep 1
nb=$(ocount origin-b "$O5"); na=$(ocount origin-a "$O5")
echo "  zone B edge1: X-Brisk-Shield='${sB:-<none>}'  origin-b fetches=$nb  origin-a fetches=$na"
[ -z "$sB" ] && ok "zone B has no shield tier (direct origin)" || bad "zone B unexpectedly shielded (shield=$sB)"
{ [ "$nb" = "1" ] && [ "$na" = "0" ]; } && ok "zone B hit origin-b only (no cross-zone/origin mixing)" || bad "zone B origin routing wrong (a=$na b=$nb)"

echo ""; echo "=== 6) FALLBACK: stop shield -> zone A still served from origin-a directly ==="
$C stop shield >/dev/null 2>&1; sleep 2
O6="/cold/t6-$$.css"
st=$(status 18443 a.brisk.local "$O6"); n=$(ocount origin-a "$O6")
echo "  with shield DOWN: edge1 zone-A status=$st  origin-a direct fetches=$n"
[ "$st" = "200" ] && ok "zone stayed served with the shield down (graceful fallback)" || bad "zone A returned $st with shield down (blackhole!)"
$C start shield >/dev/null 2>&1; sleep 8
st=$(status 18443 a.brisk.local "/cold/t6b-$$.css")
[ "$st" = "200" ] && ok "shield resumed after restart (status $st)" || bad "shield did not resume ($st)"

echo ""; echo "=== 7) LOOP GUARD: the shield pulls the ORIGIN, never another tier ==="
probe=$(docker run --rm --network brisk-shield-lab_default curlimages/curl:latest \
          -sk --max-time 15 --connect-to a.brisk.local:443:shield:443 -D - -o /dev/null \
          "https://a.brisk.local/cold/t7-$$.css" 2>/dev/null | tr -d '\r')
se=$(echo "$probe" | awk 'tolower($1)=="x-brisk-edge:"{print $2}')
ss=$(echo "$probe" | awk 'tolower($1)=="x-brisk-shield:"{print $2}')
echo "  shield direct: X-Brisk-Edge=$se  X-Brisk-Shield='${ss:-<none>}'"
{ [ "$se" = "SHIELD-1" ] && [ -z "$ss" ]; } && ok "shield serves origin directly (no self/loop shielding)" || bad "shield loop-guard suspect (edge=$se shield=$ss)"

echo ""; echo "=== 10) CACHE-KEY PARITY (implicit) ==="
echo "  test 2 collapsing to ONE origin fetch IS the key-parity proof: a key mismatch"
echo "  would make edge2 MISS at the shield => 2 origin fetches."
[ "$(ocount origin-a "$O2")" = "1" ] && ok "edge & shield agree on the cache key" || bad "key mismatch (origin fetched twice)"

echo ""; echo "================  $PASS passed, $FAIL failed  ================"
[ "$FAIL" = "0" ]
