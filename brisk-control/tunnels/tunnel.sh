#!/bin/sh
# Brisk reverse-SSH tunnel entrypoint. Keeps ONE edge connected to the laptop
# control plane via autossh (auto-reconnects on laptop sleep / IP change).
#
# Required env: EDGE_IP
# Optional env: EDGE_USER (root), EDGE_PORT (22), EDGE_PASS (password auth; omit
#   to use a mounted key), CONTROL_HOST (brisk-control), CONTROL_PORT (8080),
#   NATS_HOST (nats), NATS_PORT (4222), TUNNEL_CONTROL_PORT (18080),
#   TUNNEL_NATS_PORT (14222).
set -eu

: "${EDGE_IP:?EDGE_IP is required}"
EDGE_USER="${EDGE_USER:-root}"
EDGE_PORT="${EDGE_PORT:-22}"
CONTROL_HOST="${CONTROL_HOST:-brisk-control}"
CONTROL_PORT="${CONTROL_PORT:-8080}"
NATS_HOST="${NATS_HOST:-nats}"
NATS_PORT="${NATS_PORT:-4222}"
TUNNEL_CONTROL_PORT="${TUNNEL_CONTROL_PORT:-18080}"
TUNNEL_NATS_PORT="${TUNNEL_NATS_PORT:-14222}"

# autossh restarts ssh if the forwards go stale; ssh keepalives detect a dead link.
SSH_OPTS="-N \
  -o ServerAliveInterval=15 -o ServerAliveCountMax=3 \
  -o ExitOnForwardFailure=yes \
  -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=/dev/null \
  -p ${EDGE_PORT} \
  -R 127.0.0.1:${TUNNEL_CONTROL_PORT}:${CONTROL_HOST}:${CONTROL_PORT} \
  -R 127.0.0.1:${TUNNEL_NATS_PORT}:${NATS_HOST}:${NATS_PORT}"

echo "brisk-tunnel: ${EDGE_USER}@${EDGE_IP}:${EDGE_PORT}  edge:127.0.0.1:${TUNNEL_CONTROL_PORT}->${CONTROL_HOST}:${CONTROL_PORT}  edge:127.0.0.1:${TUNNEL_NATS_PORT}->${NATS_HOST}:${NATS_PORT}"

# AUTOSSH_GATETIME=0 = restart even if the first connection dies quickly.
export AUTOSSH_GATETIME=0
if [ -n "${EDGE_PASS:-}" ]; then
  exec sshpass -p "${EDGE_PASS}" autossh -M 0 ${SSH_OPTS} "${EDGE_USER}@${EDGE_IP}"
else
  # key auth: the key is mounted read-only at /root/.ssh/id_ed25519, but a Windows
  # bind mount gives it loose perms — ssh REFUSES a private key that's group/world
  # readable. Copy it to a private 0600 path first, and pin to it (IdentitiesOnly).
  cp /root/.ssh/id_ed25519 /tmp/brisk_tunnel_key
  chmod 600 /tmp/brisk_tunnel_key
  exec autossh -M 0 -i /tmp/brisk_tunnel_key -o IdentitiesOnly=yes ${SSH_OPTS} "${EDGE_USER}@${EDGE_IP}"
fi
