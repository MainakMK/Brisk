#!/bin/sh
# Phase 4 Step 6 Part 1 — roll the Lua module onto ONE live edge, one at a time.
# Swaps the brisk-agent binary, then runs `--bootstrap` (EnsureLua builds the
# lua-nginx-module; EnsureGeoIP builds the geoip2 module — both gated, no behavior
# change until rules/DB exist), which re-renders nginx.conf WITH the lua module and
# reloads (nginx -t first; a bad config never reloads). nginx keeps serving the
# whole time. Run on a DRAINED edge; verify byte-identical, then undrain.
#
# Usage (alpine container w/ /bin/brisk-agent-linux-amd64 + tunnels/.env at /env/.env):
#   deploy-lua.sh NY|DE|BLR
# Creds read from /env/.env by prefix, captured with cut (no shell expansion),
# never echoed. BLR password stores a literal '$' as '$$' (compose escaping) -> unescape.
set -e
PFX="$1"
[ -n "$PFX" ] || { echo "usage: deploy-lua.sh NY|DE|BLR"; exit 2; }

IP=$(grep "^${PFX}_IP=" /env/.env | cut -d= -f2-)
USER=$(grep "^${PFX}_USER=" /env/.env | cut -d= -f2-)
PORT=$(grep "^${PFX}_PORT=" /env/.env | cut -d= -f2-)
PASS=$(grep "^${PFX}_PASS=" /env/.env | cut -d= -f2- | sed 's/\$\$/\$/g')
[ -n "$IP" ] && [ -n "$USER" ] && [ -n "$PASS" ] || { echo "missing creds for $PFX"; exit 2; }
[ -n "$PORT" ] || PORT=22

export SSHPASS="$PASS"
SSH="sshpass -e ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=15 -p $PORT"
SCP="sshpass -e scp -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=15 -P $PORT"

echo "[$PFX] probe ssh ($IP)"
$SSH "${USER}@${IP}" "true"

echo "[$PFX] record pre-state (nginx version, lua module presence)"
$SSH "${USER}@${IP}" "nginx -v 2>&1; ls -1 /etc/nginx/modules/ngx_http_lua_module.so 2>/dev/null || echo 'lua-module: absent (expected pre-rollout)'"

echo "[$PFX] copying new binary"
$SCP /bin/brisk-agent-linux-amd64 "${USER}@${IP}:/usr/local/bin/brisk-agent.new"

echo "[$PFX] stop agent, BACK UP current binary (.prev), swap, bootstrap (build lua + render + reload), start agent"
# Stop the serve agent so it doesn't fight bootstrap over nginx.conf. nginx keeps
# running (only the agent control process stops). We back up the live binary to
# .prev for one-command rollback. Bootstrap does its own nginx -t + reload; if it
# fails, nginx keeps the OLD config and we STILL start the serve agent (its own
# nginx -t guard keeps last-good config) so the box is never left agent-less.
$SSH "${USER}@${IP}" "
  systemctl stop brisk-agent
  cp -a /usr/local/bin/brisk-agent /usr/local/bin/brisk-agent.prev
  mv /usr/local/bin/brisk-agent.new /usr/local/bin/brisk-agent
  chmod 0755 /usr/local/bin/brisk-agent
  echo '[remote] running bootstrap (compiles LuaJIT + modules; a few minutes)...'
  if /usr/local/bin/brisk-agent --bootstrap --config /etc/brisk/agent.yaml; then
    echo '[remote] bootstrap OK'
  else
    echo '[remote] WARNING: bootstrap returned non-zero (continuing; nginx keeps last-good config)'
  fi
  echo '[remote] lua module:'; ls -l /etc/nginx/modules/ngx_http_lua_module.so 2>&1 || echo 'lua .so MISSING'
  echo '[remote] nginx -t:'; nginx -t 2>&1 || echo 'nginx -t FAILED (nginx still serving prior good config)'
  systemctl start brisk-agent
  sleep 2
  echo -n '[remote] agent active: '; systemctl is-active brisk-agent
  echo '[remote] lua load_module lines:'; grep -h 'load_module' /etc/nginx/nginx.conf | grep -i lua || echo '(no lua load_module rendered)'
"
echo "[$PFX] DEPLOY_LUA_DONE"

# --- rollback (manual, if verification fails) ---
# $SSH root@IP 'systemctl stop brisk-agent; mv /usr/local/bin/brisk-agent.prev /usr/local/bin/brisk-agent; \
#   rm -f /etc/nginx/modules/ngx_http_lua_module.so /etc/nginx/modules/ndk_http_module.so; \
#   systemctl start brisk-agent; nginx -t && nginx -s reload'
