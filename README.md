# Brisk CDN

A self-hosted, fully-owned content delivery network — the free, proven pieces the
big CDNs (Bunny, KeyCDN, CDN77) are built on, assembled into something you run and
own end to end. Built for large private sites whose core content is **HLS video**
plus static web assets, and designed to scale and later be sold as a product.

The recipe is deliberately boring and battle-tested:

> **Nginx (cache/data plane) + a small Go agent (control) + Lua (programmable edge).**

No third-party service sits in the production request path. Claude Code built it;
**your own Go binary and Nginx run it.**

---

## How it fits together

Brisk is split into two independent planes, plus a dashboard:

```
                ┌──────────────────────────────────────────┐
                │            CONTROL PLANE                   │
                │   brisk-control (Go) + TimescaleDB + NATS  │
                │   - REST API + RBAC + admin tokens         │
                │   - per-zone config, DNS reconciler,       │
                │     analytics, logs, purge fan-out,        │
                │     signed agent self-update / rollouts    │
                └───────────────┬────────────────────────────┘
                                │  pull config (ETag/304),
                                │  heartbeats, stats, logs
                                ▼
   ┌──────────────┐    ┌──────────────┐    ┌──────────────┐
   │  EDGE (NYC)  │    │  EDGE (FRA)  │    │  EDGE (BLR)  │   ← bare-metal / VPS
   │ Nginx + Lua  │    │ Nginx + Lua  │    │ Nginx + Lua  │     one static Go binary
   │ brisk-agent  │    │ brisk-agent  │    │ brisk-agent  │     each (no Docker)
   └──────────────┘    └──────────────┘    └──────────────┘
            ▲ serves cached HLS + static assets to end users

   ┌────────────────────────────────────────────────────────┐
   │  brisk-dashboard (React + Vite + Tailwind)              │
   │  talks to the control plane's REST API                  │
   └────────────────────────────────────────────────────────┘
```

**Golden rule:** the data plane is independent of the control plane. If the
control plane / dashboard is down, edges keep serving from their last saved
config. *Dashboard down ≠ CDN down.*

---

## Repository layout

| Path | What it is |
|------|------------|
| [`brisk-agent/`](brisk-agent/) | The Go edge agent — one small static binary per PoP. Renders + validates Nginx config (never reloads a bad config), manages TLS, ships stats/logs, runs the Lua edge, applies cache/WAF/shield config pulled from the control plane, and self-updates from signed releases. |
| [`brisk-control/`](brisk-control/README.md) | The Go control plane — REST API (chi), Postgres/TimescaleDB store, NATS purge bus, Bunny DNS reconciler, auth/RBAC, analytics & logs ingest, agent release + rollout engine. Ships with `docker-compose.yml` for local dev. |
| [`brisk-control/tunnels/`](brisk-control/tunnels/README.md) | Operational tooling: reverse-SSH tunnels from edges to a local control plane, plus the gated one-edge-at-a-time deploy scripts (`deploy-*.sh`) used to roll agent changes byte-identically. |
| [`brisk-dashboard/`](brisk-dashboard/README.md) | The web UI — React + TypeScript + Vite + Tailwind v4. Servers, Zones, Analytics, Logs, Purge, DNS, Security, Settings. |
| [`docs/`](docs/) | Architecture, runbooks, feature docs, and security audits. Start with [`docs/features/Brisk_Architecture_Bible.html`](docs/features/Brisk_Architecture_Bible.html) for the full top-to-bottom architecture. |
| [`dashboard-reference/`](dashboard-reference/) | Design research: feature comparison, design spec, tokens, mockup concepts. |
| `lua-lab/`, `shield-lab/`, `waf-lab/` | Self-contained Docker test harnesses for the Lua edge, origin shield, and WAF. |
| [`CLAUDE.md`](CLAUDE.md) | Compact project rules + current status (read first if you're picking the project back up). |

---

## Stack (June 2026)

- **OS (edges):** Ubuntu 24.04 LTS, bare-metal / VPS (no Docker on edges)
- **Cache:** Nginx stable 1.30.x (official nginx.org packages) — slice module for HLS, request coalescing, Brotli + Gzip, `headers-more` branding
- **Agent / control plane:** Go 1.26.x, single static binary
- **TLS:** Let's Encrypt ECDSA P-256, TLS 1.3 + 1.2, OCSP stapling, HSTS
- **Edge programmability:** Lua (OpenResty modules), gated per-zone
- **Data:** Postgres + TimescaleDB (analytics, request logs)
- **DNS / routing:** Bunny DNS (Smart Records, geo + latency steering, health-aware failover)
- **Kernel:** TCP BBR (`fq` + `bbr`)

---

## Local development (quick start)

The control plane + dashboard run locally in Docker. The edges are real Linux
hosts (or the provided lab harnesses for feature testing).

```bash
# 1. Control plane (API :8080, TimescaleDB, NATS) + dashboard (:5173)
cd brisk-control
cp .env.example .env          # fill in secrets — see "Configuration" below
docker compose up -d

# 2. Dashboard env (browser → control-plane API URL)
cd ../brisk-dashboard
cp .env.example .env          # VITE_API_URL=http://localhost:8080

# 3. Open the dashboard
#    http://localhost:5173
```

Feature test harnesses (each self-contained):

```bash
cd lua-lab    && ./run.sh     # programmable Lua edge
cd shield-lab && ./run.sh     # origin shield / collapse forwarding
cd waf-lab    && ./run.sh     # Coraza WAF + OWASP CRS
```

> Some capabilities only prove out on a real VPS (a real Let's Encrypt cert,
> public latency, and BBR's real effect — the host kernel governs BBR inside
> Docker). See `docs/` for the deploy + rollout runbooks.

---

## Configuration & secrets

Every component reads its secrets from a local `.env` (never committed). Copy the
matching `*.env.example` and fill it in:

| File | Holds |
|------|-------|
| `brisk-control/.env` | DB DSN, Bunny DNS API key, admin bootstrap creds, ACME email, feature gates (`BRISK_ROLLOUT_ENABLED`, `BRISK_LOAD_STEER_ENABLED`, …), agent signing public keys |
| `brisk-control/tunnels/.env` | SSH host/credentials for the reverse tunnels (edge IPs) |
| `brisk-dashboard/.env` | `VITE_API_URL` — the control-plane base URL the browser calls |

**Never commit:** `.env` files, SSH private keys (`id_ed25519`), TLS keys,
`agent-*.yaml` (carry live agent tokens), DB dumps (`brisk-control/backups/`), or
the built agent binaries. These are all covered by [`.gitignore`](.gitignore).
The ed25519 **private signing key** for agent releases lives only with the
operator (password manager / CI secret) — it is never stored in this repo.

---

## Status

Phases 1–4 are **built and validated**: single-edge caching → multi-PoP control
plane + dashboard → Bunny DNS routing with health-aware failover → the
multi-tenant, sellable-CDN surface (custom domains + auto-TLS, per-zone origin
shield, per-zone WAF, a programmable Lua edge, real logs + analytics, signed
agent self-update + region rollouts). Everything new ships **gated and off by
default**, so enabling a feature never changes a byte of a PoP's config until you
turn it on.

For the exhaustive architecture (every endpoint, env var, migration, data shape,
and the rendered `nginx.conf`), see
[`docs/features/Brisk_Architecture_Bible.html`](docs/features/Brisk_Architecture_Bible.html).

---

## License

Proprietary — all rights reserved. Not yet licensed for redistribution.
