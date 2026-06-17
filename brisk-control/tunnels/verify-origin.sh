#!/bin/sh
# Verify the live zones serve through ONE edge (used as a before/after gate for the
# origin-options rollout). Curls each given hostname against the edge's IP via --resolve
# and prints only "PFX host code server" — the edge IP (from /env/.env) is NEVER echoed.
#
# Usage (alpine + curl; mount tunnels dir at /env):
#   sh /env/verify-origin.sh NY testmainak.cdn.a2zjav.com testjim.cdn.a2zjav.com
set -e
PFX="$1"; shift || true
[ -n "$PFX" ] && [ -n "$1" ] || { echo "usage: verify-origin.sh NY|DE|BLR host [host...]"; exit 2; }
IP=$(grep "^${PFX}_IP=" /env/.env | cut -d= -f2-)
[ -n "$IP" ] || { echo "missing IP for $PFX"; exit 2; }
for H in "$@"; do
  code=$(curl -k -s -o /dev/null -w '%{http_code}' --resolve "${H}:443:${IP}" "https://${H}/" || echo ERR)
  srv=$(curl -k -sI --resolve "${H}:443:${IP}" "https://${H}/" 2>/dev/null | tr -d '\r' | awk 'tolower($1)=="server:"{print $2}')
  echo "$PFX $H code=$code server=${srv:-?}"
done
