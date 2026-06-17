#!/bin/sh
# Read-only edge check for the hotlink demo: shows whether THIS edge re-rendered
# valid_referers + the guard, and the last few agent log lines. Creds from /env/.env.
set -e
PFX="$1"; [ -n "$PFX" ] || { echo "usage: hotlink-check.sh NY|DE|BLR"; exit 2; }
IP=$(grep "^${PFX}_IP=" /env/.env | cut -d= -f2-)
USER=$(grep "^${PFX}_USER=" /env/.env | cut -d= -f2-)
PORT=$(grep "^${PFX}_PORT=" /env/.env | cut -d= -f2-); [ -n "$PORT" ] || PORT=22
cp /env/id_ed25519 /tmp/k && chmod 600 /tmp/k
SSH="ssh -i /tmp/k -o IdentitiesOnly=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=15 -p $PORT"
$SSH "${USER}@${IP}" 'sh -s' <<"EOF"
echo "agent_binary_sha=$(sha256sum /usr/local/bin/brisk-agent | cut -c1-16)"
echo "--- recent agent log: render/nginx/error lines ---"
journalctl -u brisk-agent --no-pager -n 60 | grep -iE "nginx|render|error|applied|valid|referer|fail|reload|invalid" | grep -iv heartbeat | tail -10
echo "valid_referers_count=$(grep -c valid_referers /etc/nginx/nginx.conf || true)"
echo "invalid_referer_count=$(grep -c invalid_referer /etc/nginx/nginx.conf || true)"
echo "--- valid_referers line (if any) ---"
grep -n "valid_referers" /etc/nginx/nginx.conf || echo "(none)"
echo "--- nginx -t ---"
nginx -t 2>&1 | tail -1
echo "--- last 8 agent log lines ---"
journalctl -u brisk-agent --no-pager -n 8 | tail -8
EOF
