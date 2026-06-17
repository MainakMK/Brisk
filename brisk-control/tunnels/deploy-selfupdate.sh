#!/bin/sh
# Self-update rollout (Phase 8a): rolls the brisk-agent that can pull + verify + apply signed
# releases from the control plane (selfupdate package + your trusted ed25519 public key k1 baked
# in). This change adds ONLY a boot self-check + a /agent/release poll loop — it touches ZERO
# nginx rendering — so the new agent MUST render a BYTE-IDENTICAL nginx.conf. This script ASSERTS
# sha256(nginx.conf) BEFORE == AFTER and exits non-zero if they differ (caller halts). Backs up the
# old binary (.prev-su) + nginx.conf (.bak-su) for one-command rollback. nginx is NOT stopped — it
# keeps serving while the agent swaps + re-renders (nginx -t before reload; rollback on failure),
# so the edge never stops serving. Nothing self-updates yet: no signed release exists until you
# push one, and /agent/release is a no-op until a rollout wave is opened from the dashboard.
#
# Usage (alpine + openssh-client; mount tunnels dir at /env):
#   sh /env/deploy-selfupdate.sh NY|DE|BLR
# Creds read from /env/.env by prefix and used internally — NEVER echoed.
set -e
PFX="$1"
[ -n "$PFX" ] || { echo "usage: deploy-selfupdate.sh NY|DE|BLR"; exit 2; }

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
$SCP /env/brisk-agent-su-linux "${USER}@${IP}:/usr/local/bin/brisk-agent.new"

echo "[$PFX] backup + swap + restart (nginx keeps serving; agent re-renders + reloads)"
$SSH "${USER}@${IP}" "
  set -e
  cp -a /usr/local/bin/brisk-agent /usr/local/bin/brisk-agent.prev-su
  cp -a /etc/nginx/nginx.conf /etc/nginx/nginx.conf.bak-su
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
"

# Hard byte-identical gate: self-update is a heartbeat/poll-only change, no nginx rendering
# touched, so the rendered nginx.conf MUST be unchanged.
if [ "$BEFORE" != "$AFTER" ]; then
  echo "[$PFX] !!! BYTE-IDENTICAL GATE FAILED: nginx.conf changed ($BEFORE != $AFTER) — ROLL BACK: mv /usr/local/bin/brisk-agent.prev-su /usr/local/bin/brisk-agent && systemctl restart brisk-agent"
  exit 1
fi
echo "[$PFX] BYTE_IDENTICAL_OK ($BEFORE)"
echo "[$PFX] DEPLOY_DONE"
