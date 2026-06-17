#!/bin/sh
# Phase 4 Step 6 Part 1 — READ-ONLY post-rollout verification of ONE edge's rendered
# nginx.conf: proves the Lua module is loaded (http-block gated lua) BUT no per-zone
# Lua enforcement hooks are rendered (opt-in intact), and the geoip2 block stays off
# (no mmdb). Usage (alpine container w/ tunnels/.env at /env/.env): verify-lua.sh NY|DE|BLR
set -e
PFX="$1"
[ -n "$PFX" ] || { echo "usage: verify-lua.sh NY|DE|BLR"; exit 2; }
IP=$(grep "^${PFX}_IP=" /env/.env | cut -d= -f2-)
USER=$(grep "^${PFX}_USER=" /env/.env | cut -d= -f2-)
PORT=$(grep "^${PFX}_PORT=" /env/.env | cut -d= -f2-)
PASS=$(grep "^${PFX}_PASS=" /env/.env | cut -d= -f2- | sed 's/\$\$/\$/g')
[ -n "$IP" ] && [ -n "$USER" ] && [ -n "$PASS" ] || { echo "missing creds for $PFX"; exit 2; }
[ -n "$PORT" ] || PORT=22
export SSHPASS="$PASS"
SSH="sshpass -e ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=15 -p $PORT"

$SSH "${USER}@${IP}" '
  C=/etc/nginx/nginx.conf
  echo "lua_module_loaded=$(grep -c "load_module.*ngx_http_lua_module.so" $C)   (expect 1)"
  echo "init_by_lua=$(grep -c "init_by_lua_file" $C)   (expect 1)"
  echo "lua_shared_dict_brisk_rl=$(grep -c "lua_shared_dict brisk_rl" $C)   (expect 1)"
  echo "PER_ZONE_HOOKS set_\$brisk_zone=$(grep -c "set \$brisk_zone" $C)   (expect 0 = opt-in intact)"
  echo "rewrite_by_lua_file=$(grep -c "rewrite_by_lua_file" $C)   (expect 0)"
  echo "access_by_lua_file=$(grep -c "access_by_lua_file" $C)   (expect 0)"
  echo "geoip2_block=$(grep -c "geoip2 /etc/brisk/geoip" $C)   (expect 0 = no mmdb, gated off)"
  echo "brisk_country_map_fallback=$(grep -c "map \$remote_addr \$brisk_country" $C)   (expect 1)"
  echo "json_request_log=$(grep -c "brisk.requests.log" $C)   (expect 1)"
  echo "zones_data_lua:"; head -3 /etc/brisk/lua/zones_data.lua 2>/dev/null || echo "  (absent)"
'
