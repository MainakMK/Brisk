#!/bin/sh
# Origin-options rollout: rolls the origin-options-capable brisk-agent onto ONE live edge
# over SSH KEY auth. The origin options (Verify-SSL / Follow-redirects / Forward-host) ship
# DARK (no zone enables any), so the new agent MUST render a BYTE-IDENTICAL nginx.conf.
# This script ASSERTS sha256 BEFORE == AFTER and exits non-zero if they differ (so the
# caller halts the rollout). Backs up the old binary (.prev-origin) and nginx.conf
# (.bak-origin) for a one-command rollback. (Hotlink's .prev-hl backups are left intact.)
#
# Usage (alpine + openssh-client; mount tunnels dir at /env):
#   sh /env/deploy-origin.sh NY|DE|BLR
# Creds read from /env/.env by prefix and used internally — NEVER echoed.
set -e
PFX="$1"
[ -n "$PFX" ] || { echo "usage: deploy-origin.sh NY|DE|BLR"; exit 2; }

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

echo "[$PFX] BEFORE nginx.conf sha256 + agent sha + services active:"
BEFORE=$($SSH "${USER}@${IP}" "sha256sum /etc/nginx/nginx.conf | cut -c1-64")
echo "  before_sha=$BEFORE"
echo "  agent_before=$($SSH "${USER}@${IP}" "sha256sum /usr/local/bin/brisk-agent | cut -c1-16")"
$SSH "${USER}@${IP}" "systemctl is-active brisk-agent; systemctl is-active nginx" || true

echo "[$PFX] copy new binary"
$SCP /env/brisk-agent-linux-amd64 "${USER}@${IP}:/usr/local/bin/brisk-agent.new"

echo "[$PFX] backup + swap + restart (nginx keeps serving; agent re-renders + reloads)"
$SSH "${USER}@${IP}" "
  set -e
  cp -a /usr/local/bin/brisk-agent /usr/local/bin/brisk-agent.prev-origin
  cp -a /etc/nginx/nginx.conf /etc/nginx/nginx.conf.bak-origin
  chmod 0755 /usr/local/bin/brisk-agent.new
  systemctl stop brisk-agent
  mv /usr/local/bin/brisk-agent.new /usr/local/bin/brisk-agent
  systemctl start brisk-agent
  sleep 6
  systemctl is-active brisk-agent
"

echo "[$PFX] AFTER nginx.conf sha256 + checks:"
AFTER=$($SSH "${USER}@${IP}" "sha256sum /etc/nginx/nginx.conf | cut -c1-64")
echo "  after_sha=$AFTER"
echo "  agent_after=$($SSH "${USER}@${IP}" "sha256sum /usr/local/bin/brisk-agent | cut -c1-16")"
$SSH "${USER}@${IP}" "
  nginx -t 2>&1 | tail -1
  systemctl is-active nginx
  printf 'healthz='; curl -fsS -o /dev/null -w '%{http_code}\n' http://127.0.0.1/healthz || echo FAIL
  echo origin_ssl_verify=\$(grep -c 'proxy_ssl_verify on' /etc/nginx/nginx.conf)        # expect 0 = gated off
  echo origin_follow_redirect=\$(grep -c '@brisk_follow_redirect' /etc/nginx/nginx.conf) # expect 0 = gated off
"

# Hard byte-identical gate: the origin options ship dark, so the rendered nginx.conf MUST
# be unchanged. (Forward-host / verify-ssl / follow-redirects only emit when a zone opts in.)
if [ "$BEFORE" != "$AFTER" ]; then
  echo "[$PFX] !!! BYTE-IDENTICAL GATE FAILED: nginx.conf changed ($BEFORE != $AFTER) — ROLL BACK with brisk-agent.prev-origin"
  exit 1
fi
echo "[$PFX] BYTE_IDENTICAL_OK ($BEFORE)"
echo "[$PFX] DEPLOY_DONE"
