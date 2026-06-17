#!/bin/sh
# Rename ONE live edge's edge_id (the X-Brisk-Edge identity) over SSH key auth. Backs up
# the on-edge agent.yaml (.bak-rename), rewrites edge_id, restarts brisk-agent so nginx
# re-renders with the new X-Brisk-Edge header, then prints proof. The DB edge_id + DNS
# retag are handled by the caller (psql UPDATE + reconcile). Auth is token-based, so the
# id change never breaks the agent's heartbeat/pull.
#
# Usage (alpine + openssh-client; tunnels dir at /env):
#   sh /env/rename-edge.sh NY|DE|BLR  <new-edge-id>
set -e
PFX="$1"; NEW="$2"
[ -n "$PFX" ] && [ -n "$NEW" ] || { echo "usage: rename-edge.sh NY|DE|BLR <new-edge-id>"; exit 2; }

IP=$(grep "^${PFX}_IP=" /env/.env | cut -d= -f2-)
USER=$(grep "^${PFX}_USER=" /env/.env | cut -d= -f2-)
PORT=$(grep "^${PFX}_PORT=" /env/.env | cut -d= -f2-)
[ -n "$IP" ] && [ -n "$USER" ] || { echo "missing creds for $PFX"; exit 2; }
[ -n "$PORT" ] || PORT=22

cp /env/id_ed25519 /tmp/k && chmod 600 /tmp/k
SSH="ssh -i /tmp/k -o IdentitiesOnly=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=15 -p $PORT"

echo "[$PFX] BEFORE:"
$SSH "${USER}@${IP}" "grep '^edge_id:' /etc/brisk/agent.yaml; systemctl is-active brisk-agent nginx"

$SSH "${USER}@${IP}" "
  set -e
  cp -a /etc/brisk/agent.yaml /etc/brisk/agent.yaml.bak-rename
  sed -i 's|^edge_id:.*|edge_id: \"$NEW\"|' /etc/brisk/agent.yaml
  systemctl restart brisk-agent
  sleep 6
  systemctl is-active brisk-agent
"

echo "[$PFX] AFTER:"
$SSH "${USER}@${IP}" "
  grep '^edge_id:' /etc/brisk/agent.yaml
  nginx -t 2>&1 | tail -1
  systemctl is-active nginx
  echo header=\$(grep -o 'X-Brisk-Edge: [^'\\''\"]*' /etc/nginx/nginx.conf | head -1)
"
echo "[$PFX] RENAME_DONE -> $NEW"
