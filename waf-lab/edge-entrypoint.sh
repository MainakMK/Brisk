#!/bin/sh
# brisk edge container entrypoint (local WAF lab). The agent reads the mounted
# standalone agent.yaml, renders nginx.conf (auth_request /_waf + native limit_req
# for WAF-enabled zones), starts its loopback Coraza WAF service, provisions
# self-signed certs, starts nginx (daemonized), then stays up on its signal loop.
set -e
echo "[brisk-waf-lab] $(grep -m1 edge_id /etc/brisk/agent.yaml || true)"
exec brisk-agent -config /etc/brisk/agent.yaml
