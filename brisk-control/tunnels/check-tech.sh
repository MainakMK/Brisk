#!/bin/sh
# Diagnostic: compare the RUNNING brisk-agent process exe vs the on-disk binary, to detect a
# lingering old process after a stop/swap/start. Creds from /env/.env (never echoed).
set -e
PFX="$1"
IP=$(grep "^${PFX}_IP=" /env/.env | cut -d= -f2-)
USER=$(grep "^${PFX}_USER=" /env/.env | cut -d= -f2-)
PORT=$(grep "^${PFX}_PORT=" /env/.env | cut -d= -f2-); [ -n "$PORT" ] || PORT=22
cp /env/id_ed25519 /tmp/k && chmod 600 /tmp/k
SSH="ssh -i /tmp/k -o IdentitiesOnly=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=15 -p $PORT"
echo "=== $PFX ==="
$SSH "${USER}@${IP}" '
  PID=$(systemctl show -p MainPID --value brisk-agent)
  echo "MainPID=$PID started=$(systemctl show -p ActiveEnterTimestamp --value brisk-agent)"
  echo "disk_sha=$(sha256sum /usr/local/bin/brisk-agent | cut -c1-16)"
  echo "run_exe_sha=$(sha256sum /proc/$PID/exe 2>/dev/null | cut -c1-16)"
  echo "log_tail:"; journalctl -u brisk-agent -n 4 --no-pager 2>/dev/null | tail -4
'
