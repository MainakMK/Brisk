#!/bin/sh
# Inspect / retire acme.sh on one live edge (Phase 3.7 Step 2 cutover, Step 4).
# Managed TLS now owns /etc/brisk/tls/<domain>/, so acme.sh must stop rewriting it.
# Usage (alpine container w/ tunnels/.env at /env/.env): retire-acme.sh NY|DE|BLR [inspect|disable]
# Default mode is inspect (read-only). Reversible: re-add the cron line to restore.
set -e
PFX="$1"; MODE="${2:-inspect}"
[ -n "$PFX" ] || { echo "usage: retire-acme.sh NY|DE|BLR [inspect|disable]"; exit 2; }

IP=$(grep "^${PFX}_IP=" /env/.env | cut -d= -f2-)
USER=$(grep "^${PFX}_USER=" /env/.env | cut -d= -f2-)
PORT=$(grep "^${PFX}_PORT=" /env/.env | cut -d= -f2-)
PASS=$(grep "^${PFX}_PASS=" /env/.env | cut -d= -f2- | sed 's/\$\$/\$/g')
[ -n "$IP" ] && [ -n "$USER" ] && [ -n "$PASS" ] || { echo "missing creds for $PFX"; exit 2; }
[ -n "$PORT" ] || PORT=22
export SSHPASS="$PASS"
SSH="sshpass -e ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=15 -p $PORT"

INSPECT='echo "--- acme cron ---"; (crontab -l 2>/dev/null | grep -i acme.sh) || echo "(none)";
echo "--- acme units ---"; (systemctl list-unit-files 2>/dev/null | grep -i acme) || echo "(none)";
echo "--- ~/.acme.sh ---"; (ls -d ~/.acme.sh 2>/dev/null) || echo "(none)"'

DISABLE='echo "=== DISABLING acme.sh ===";
( crontab -l 2>/dev/null | grep -v -i "acme.sh" ) | crontab - 2>/dev/null || true;
for u in $(systemctl list-unit-files 2>/dev/null | grep -i acme | awk "{print \$1}"); do systemctl disable --now "$u" 2>/dev/null || true; done;
echo "--- verify no acme cron remains ---"; (crontab -l 2>/dev/null | grep -i acme.sh) && echo "WARN: cron still present" || echo "OK: no acme cron"'

echo "[$PFX] $IP  (mode=$MODE)"
if [ "$MODE" = disable ]; then
  $SSH "${USER}@${IP}" "$INSPECT; $DISABLE"
else
  $SSH "${USER}@${IP}" "$INSPECT"
fi
