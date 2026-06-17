#!/bin/sh
# Free stale reverse-forward ports (18080 control, 14222 NATS) on every edge after a
# brisk-control recreate + tunnel restart. The edge sshd holds the OLD session's
# forwarded ports until a long TCP timeout (no ClientAliveInterval), which blocks the
# new autossh from re-binding ("remote port forwarding failed"). Killing the holder
# lets the next autossh retry bind immediately. Data plane (nginx :443) is untouched.
# Usage (alpine + openssh-client; mount tunnels dir at /env): sh /env/free-ports.sh
set -e
cp /env/id_ed25519 /tmp/k && chmod 600 /tmp/k
SSHBASE="ssh -i /tmp/k -o IdentitiesOnly=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=15"
for P in NY DE BLR; do
  IP=$(grep "^${P}_IP=" /env/.env | cut -d= -f2-)
  U=$(grep "^${P}_USER=" /env/.env | cut -d= -f2-)
  PT=$(grep "^${P}_PORT=" /env/.env | cut -d= -f2-)
  [ -n "$PT" ] || PT=22
  [ -n "$IP" ] && [ -n "$U" ] || { echo "[$P] missing creds"; continue; }
  echo "[$P] freeing stale 18080/14222 forwards"
  $SSHBASE -p "$PT" "${U}@${IP}" 'fuser -k 18080/tcp 14222/tcp 2>/dev/null || true; sleep 1; if ss -tlnH 2>/dev/null | grep -Eq "127.0.0.1:(18080|14222)"; then echo "  STILL_HELD"; else echo "  FREED"; fi'
done
