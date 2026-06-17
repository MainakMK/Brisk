#!/bin/sh
# Security-parity rollout: rolls the brisk-agent that adds the four Bunny-parity Security
# features onto ONE live edge over SSH KEY auth. All three EDGE features ship DARK (no zone
# enables any), so the new agent MUST render a BYTE-IDENTICAL nginx.conf:
#   - custom 502/504 error page (error_page 502 504 /__brisk_5xx)  -> only when a zone sets HTML
#   - blocked-IP denylist (deny <cidr> on content locations)        -> only when a zone lists IPs
#   - block-root-path / block-POST (server-level if 403/405)        -> only when a zone enables
# This script ASSERTS sha256 BEFORE == AFTER and exits non-zero if they differ (so the caller
# halts). Backs up the old binary (.prev-sec) and nginx.conf (.bak-sec) for one-command rollback.
# nginx is NOT stopped — it keeps serving the current config while the agent swaps + re-renders
# (nginx -t before reload; rollback on failure), so the edge never stops serving.
#
# Usage (alpine + openssh-client; mount tunnels dir at /env):
#   sh /env/deploy-security.sh NY|DE|BLR
# Creds read from /env/.env by prefix and used internally — NEVER echoed.
set -e
PFX="$1"
[ -n "$PFX" ] || { echo "usage: deploy-security.sh NY|DE|BLR"; exit 2; }

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
  cp -a /usr/local/bin/brisk-agent /usr/local/bin/brisk-agent.prev-sec
  cp -a /etc/nginx/nginx.conf /etc/nginx/nginx.conf.bak-sec
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
  echo error_5xx=\$(grep -c '__brisk_5xx' /etc/nginx/nginx.conf)            # expect 0 = gated off
  echo blocked_ips=\$(grep -c 'deny ' /etc/nginx/nginx.conf)               # expect 1 (pre-existing stub_status deny all) = gated off
  echo block_post=\$(grep -c 'request_method = POST' /etc/nginx/nginx.conf) # expect 0 = gated off
  echo block_root=\$(grep -cF 'uri ~ \"/\$\"' /etc/nginx/nginx.conf)        # expect 0 = gated off
"

# Hard byte-identical gate: all three edge features ship dark, so the rendered nginx.conf MUST
# be unchanged. (They only emit when a zone opts in, and no live zone has.)
if [ "$BEFORE" != "$AFTER" ]; then
  echo "[$PFX] !!! BYTE-IDENTICAL GATE FAILED: nginx.conf changed ($BEFORE != $AFTER) — ROLL BACK with brisk-agent.prev-sec"
  exit 1
fi
echo "[$PFX] BYTE_IDENTICAL_OK ($BEFORE)"
echo "[$PFX] DEPLOY_DONE"
