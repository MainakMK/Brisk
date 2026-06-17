#!/bin/sh
# brisk edge/shield container entrypoint (local origin-shield lab). The agent reads
# the mounted standalone agent.yaml, renders nginx.conf (Server: Brisk, X-Brisk-*,
# brotli, slice, and the per-zone shield-or-origin two-tier proxy), provisions
# self-signed certs, starts nginx (daemonized), then stays up on its signal loop.
set -e
echo "[brisk-lab] $(grep -m1 edge_id /etc/brisk/agent.yaml || true)"
exec brisk-agent -config /etc/brisk/agent.yaml
