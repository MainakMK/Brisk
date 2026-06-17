#!/bin/sh
# Force ONE edge's agent to re-pull + re-render now, then report whether hotlink
# rendered (valid_referers) and the nginx -t result + recent non-heartbeat log lines.
set -e
PFX="$1"; [ -n "$PFX" ] || { echo "usage: hotlink-restart.sh NY|DE|BLR"; exit 2; }
IP=$(grep "^${PFX}_IP=" /env/.env | cut -d= -f2-)
USER=$(grep "^${PFX}_USER=" /env/.env | cut -d= -f2-)
PORT=$(grep "^${PFX}_PORT=" /env/.env | cut -d= -f2-); [ -n "$PORT" ] || PORT=22
cp /env/id_ed25519 /tmp/k && chmod 600 /tmp/k
SSH="ssh -i /tmp/k -o IdentitiesOnly=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=15 -p $PORT"
$SSH "${USER}@${IP}" 'sh -s' <<"EOF"
systemctl restart brisk-agent
sleep 9
echo "valid_referers=$(grep -c valid_referers /etc/nginx/nginx.conf || true)"
echo "invalid_referer=$(grep -c invalid_referer /etc/nginx/nginx.conf || true)"
echo "--- valid_referers lines ---"
grep -n valid_referers /etc/nginx/nginx.conf || echo "(none)"
echo "--- nginx -t ---"
nginx -t 2>&1 | tail -1
echo "--- agent log (non-heartbeat, last 12) ---"
journalctl -u brisk-agent --no-pager -n 40 | grep -iv heartbeat | tail -12
EOF
