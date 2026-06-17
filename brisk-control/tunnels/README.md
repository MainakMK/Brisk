# Brisk tunnels — laptop control plane ↔ live edges (Phase 3.7 Step 1, Option A)

Connectivity that installs **nothing** on the laptop host or the edges: a small
`autossh` container per edge dials **outbound** from the laptop to each public
edge (NAT-friendly) and reverse-forwards the control API + NATS onto that edge's
`localhost`. The control plane stays **private** — it is never bound to the public
internet; each edge reaches it only through its own loopback over the SSH tunnel.

```
laptop (behind NAT)                          edge (public IP, sshd)
┌───────────────────────────┐  ssh -R   ┌──────────────────────────┐
│ brisk-control:8080  ◄──────┼───────────┼─ 127.0.0.1:18080  ◄─ agent│  BRISK_CONTROL_URL
│ nats:4222           ◄──────┼───────────┼─ 127.0.0.1:14222  ◄─ agent│  BRISK_NATS_URL
│ (autossh container dials out)          │                          │
└───────────────────────────┘           └──────────────────────────┘
```

## Run
```bash
cd brisk-control/tunnels
cp .env.example .env          # fill NY_PASS / DE_PASS / BLR_PASS (or use key auth)
docker compose up -d --build  # one tunnel container per edge, auto-reconnecting
docker compose logs -f        # watch the links come up
```
The tunnels attach to the control plane's Docker network (`brisk-control_default`),
so the reverse forwards resolve `brisk-control:8080` and `nats:4222` directly.

## Verify (from an edge)
```bash
ssh root@<edge> 'curl -s http://127.0.0.1:18080/health'   # -> {"status":"ok",...}
ssh root@<edge> 'nc -z 127.0.0.1 14222 && echo NATS reachable'
```

## Agent config (uniform across the fleet)
Each edge's `/etc/brisk/agent.yaml` points at the tunnel loopback ports:
```yaml
control_plane_url: "http://127.0.0.1:18080"
nats_url:          "nats://127.0.0.1:14222"
```
These are the **only** endpoint values. Edge ports are identical on every box
(each edge is isolated on its own loopback), so the agent.yaml is the same fleet-wide.

## Resilience
- `autossh` auto-reconnects when the laptop sleeps or its IP changes (the laptop
  always initiates; the edges have stable public IPs).
- If the laptop/control plane is offline, edges keep serving from last-known-good
  config + local cache (agent design), and JetStream replays missed purges on
  reconnect. **Dashboard down ≠ CDN down.**
- The reconciler's **all-offline guard** (Phase 3.7) means even a total heartbeat
  blackout won't disable the DNS records — the zone stays resolvable.

## Going public later = change 2 URLs (not a rebuild)
When the control plane moves to a public VPS:
1. Stop these tunnels (`docker compose down`).
2. Set the control plane's `AGENT_CONTROL_PLANE_URL` / `AGENT_NATS_URL` (or each
   agent.yaml's `control_plane_url` / `nats_url`) to the VPS's public URLs.
3. Agents re-pull and reconnect. No agent rebuild, no template change.
