#!/bin/sh
# Load-steering rollout (#3 control plane is already shipped; this rolls the #4-capable
# brisk-agent onto ONE live edge over SSH KEY auth). Edge self-protection ships DARK
# (edge_protect not set in agent.yaml), so the new agent renders a BYTE-IDENTICAL
# nginx.conf. The script prints the nginx.conf sha256 BEFORE and AFTER so the caller can
# confirm equality. Backs up the old binary (.prev-ls) and nginx.conf (.bak-ls) for a
# one-command rollback.
#
# Usage (alpine + openssh-client; mount tunnels dir at /env):
#   sh /env/deploy-loadsteer.sh NY|DE|BLR
# Creds read from /env/.env by prefix and used internally — NEVER echoed.
set -e
PFX="$1"
[ -n "$PFX" ] || { echo "usage: deploy-loadsteer.sh NY|DE|BLR"; exit 2; }

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

echo "[$PFX] BEFORE nginx.conf sha256 + services active:"
$SSH "${USER}@${IP}" "sha256sum /etc/nginx/nginx.conf | cut -c1-64; systemctl is-active brisk-agent; systemctl is-active nginx"

echo "[$PFX] copy new binary"
$SCP /env/brisk-agent-linux-amd64 "${USER}@${IP}:/usr/local/bin/brisk-agent.new"

echo "[$PFX] backup + swap + restart"
$SSH "${USER}@${IP}" "
  set -e
  cp -a /usr/local/bin/brisk-agent /usr/local/bin/brisk-agent.prev-ls
  cp -a /etc/nginx/nginx.conf /etc/nginx/nginx.conf.bak-ls
  chmod 0755 /usr/local/bin/brisk-agent.new
  systemctl stop brisk-agent
  mv /usr/local/bin/brisk-agent.new /usr/local/bin/brisk-agent
  systemctl start brisk-agent
  sleep 5
  systemctl is-active brisk-agent
"

echo "[$PFX] AFTER nginx.conf sha256 (must equal BEFORE) + nginx test + edge_protect check:"
$SSH "${USER}@${IP}" "
  sha256sum /etc/nginx/nginx.conf | cut -c1-64
  nginx -t 2>&1 | tail -1
  systemctl is-active nginx
  echo edge_protect_directives=\$(grep -c 'limit_conn brisk_perip' /etc/nginx/nginx.conf)   # expect 0 = gated off
"
echo "[$PFX] DEPLOY_DONE"
