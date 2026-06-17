#!/bin/sh
# Hybrid Shield+PoP + keepalive rollout — swap brisk-agent on ONE live edge over
# SSH KEY auth (the fleet moved off passwords). Byte-identical gate: prints the
# nginx.conf sha256 BEFORE and AFTER so the caller can confirm the new agent renders
# the SAME config (the keepalive ships dark — no zone is shielded). Backs up the old
# binary (.prev-hybrid) and nginx.conf (.bak-hybrid) for one-command rollback.
#
# Usage (alpine + openssh-client; mount tunnels dir at /env):
#   sh /env/deploy-hybrid.sh NY|DE|BLR
# Creds read from /env/.env by prefix and used internally — NEVER echoed.
set -e
PFX="$1"
[ -n "$PFX" ] || { echo "usage: deploy-hybrid.sh NY|DE|BLR"; exit 2; }

IP=$(grep "^${PFX}_IP=" /env/.env | cut -d= -f2-)
USER=$(grep "^${PFX}_USER=" /env/.env | cut -d= -f2-)
PORT=$(grep "^${PFX}_PORT=" /env/.env | cut -d= -f2-)
[ -n "$IP" ] && [ -n "$USER" ] || { echo "missing creds for $PFX"; exit 2; }
[ -n "$PORT" ] || PORT=22

# Windows bind-mounted key has loose perms -> copy to a private 0600 path + pin it.
cp /env/id_ed25519 /tmp/k && chmod 600 /tmp/k
SSH="ssh -i /tmp/k -o IdentitiesOnly=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=15 -p $PORT"
SCP="scp -i /tmp/k -o IdentitiesOnly=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=15 -P $PORT"

echo "[$PFX] probe ssh"
$SSH "${USER}@${IP}" "true"

echo "[$PFX] BEFORE nginx.conf sha256 + agent active:"
$SSH "${USER}@${IP}" "sha256sum /etc/nginx/nginx.conf | cut -c1-64; systemctl is-active brisk-agent; systemctl is-active nginx"

echo "[$PFX] copy new binary"
$SCP /env/brisk-agent-linux-amd64 "${USER}@${IP}:/usr/local/bin/brisk-agent.new"

echo "[$PFX] backup + swap + restart"
$SSH "${USER}@${IP}" "
  set -e
  cp -a /usr/local/bin/brisk-agent /usr/local/bin/brisk-agent.prev-hybrid
  cp -a /etc/nginx/nginx.conf /etc/nginx/nginx.conf.bak-hybrid
  chmod 0755 /usr/local/bin/brisk-agent.new
  systemctl stop brisk-agent
  mv /usr/local/bin/brisk-agent.new /usr/local/bin/brisk-agent
  systemctl start brisk-agent
  sleep 5
  systemctl is-active brisk-agent
"

echo "[$PFX] AFTER nginx.conf sha256 (must equal BEFORE) + nginx test:"
$SSH "${USER}@${IP}" "sha256sum /etc/nginx/nginx.conf | cut -c1-64; nginx -t 2>&1 | tail -1; systemctl is-active nginx"
echo "[$PFX] DEPLOY_DONE"
