#!/bin/sh
# Swap the brisk-agent binary on one live edge (Phase 3.7 Step 2 cutover).
# Usage (inside an alpine container with the binary at /bin/brisk-agent-linux-amd64
# and tunnels/.env at /env/.env):  deploy-agent.sh NY|DE|BLR
#
# Reads creds from /env/.env by prefix. The BLR password stores a literal '$' as
# '$$' (docker-compose escaping), so unescape it for direct sshpass use. Values are
# captured with cut (no shell expansion), never echoed.
set -e
PFX="$1"
[ -n "$PFX" ] || { echo "usage: deploy-agent.sh NY|DE|BLR"; exit 2; }

IP=$(grep "^${PFX}_IP=" /env/.env | cut -d= -f2-)
USER=$(grep "^${PFX}_USER=" /env/.env | cut -d= -f2-)
PORT=$(grep "^${PFX}_PORT=" /env/.env | cut -d= -f2-)
PASS=$(grep "^${PFX}_PASS=" /env/.env | cut -d= -f2- | sed 's/\$\$/\$/g')
[ -n "$IP" ] && [ -n "$USER" ] && [ -n "$PASS" ] || { echo "missing creds for $PFX"; exit 2; }
[ -n "$PORT" ] || PORT=22

export SSHPASS="$PASS"
SSH="sshpass -e ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=15 -p $PORT"
SCP="sshpass -e scp -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=15 -P $PORT"

# Probe FIRST (set -e aborts before we stop anything if SSH is unreachable).
echo "[$PFX] probe ssh ($IP)"
$SSH "${USER}@${IP}" "true"

echo "[$PFX] stopping brisk-agent ($IP)"
$SSH "${USER}@${IP}" "systemctl stop brisk-agent" || true

echo "[$PFX] copying new binary"
$SCP /bin/brisk-agent-linux-amd64 "${USER}@${IP}:/usr/local/bin/brisk-agent.new"

echo "[$PFX] swap + start"
$SSH "${USER}@${IP}" "
  set -e
  mv /usr/local/bin/brisk-agent.new /usr/local/bin/brisk-agent
  chmod 0755 /usr/local/bin/brisk-agent
  systemctl start brisk-agent
  sleep 2
  systemctl is-active brisk-agent
"
echo "[$PFX] DEPLOY_OK"
