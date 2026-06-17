#!/bin/sh
# brisk edge container entrypoint (local Lua-edge lab). The agent reads the mounted
# standalone agent.yaml, detects the lua module, writes the Lua library + per-zone
# data to /etc/brisk/lua, renders nginx.conf with the rewrite/header_filter hooks,
# starts the Coraza WAF service + nginx (daemonized), then stays up on its loop.
set -e
echo "[brisk-lua-lab] $(grep -m1 edge_id /etc/brisk/agent.yaml || true)"
exec brisk-agent -config /etc/brisk/agent.yaml
