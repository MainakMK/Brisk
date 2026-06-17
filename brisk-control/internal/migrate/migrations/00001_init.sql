-- +goose Up
-- TimescaleDB must be enabled before create_hypertable(); the timescale image
-- preloads the library so this succeeds.
CREATE EXTENSION IF NOT EXISTS timescaledb;

-- accounts: admin now; customers later (multi-tenant ready)
CREATE TABLE accounts (
  id         BIGSERIAL PRIMARY KEY,
  name       TEXT NOT NULL,
  role       TEXT NOT NULL DEFAULT 'admin',          -- admin | customer
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- edge servers (PoPs)
CREATE TABLE servers (
  id            BIGSERIAL PRIMARY KEY,
  name          TEXT NOT NULL,
  region        TEXT NOT NULL,                        -- e.g. "IN-DEL", "US-IL"
  ip            INET NOT NULL,
  hostname      TEXT,
  edge_id       TEXT UNIQUE NOT NULL,                 -- shows in X-Brisk-Edge, e.g. DEL1-07
  capacity_mbps INTEGER,                              -- 1000 or 10000
  status        TEXT NOT NULL DEFAULT 'pending',      -- pending|provisioning|online|offline|disabled
  last_seen     TIMESTAMPTZ,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- zones (sites being accelerated) — this is what agents will pull in Step 3
CREATE TABLE zones (
  id             BIGSERIAL PRIMARY KEY,
  account_id     BIGINT NOT NULL REFERENCES accounts(id) DEFAULT 1,
  name           TEXT NOT NULL,
  cdn_hostname   TEXT UNIQUE NOT NULL,                -- e.g. abcd.brisk-cdn.net
  custom_domain  TEXT,                                -- CNAME custom hostname
  origin_url     TEXT NOT NULL,
  tls_mode       TEXT NOT NULL DEFAULT 'letsencrypt', -- selfsigned|mkcert|letsencrypt
  video          BOOLEAN NOT NULL DEFAULT false,
  profile        TEXT NOT NULL DEFAULT 'vod',         -- vod|live
  playlist_ttl   TEXT NOT NULL DEFAULT '2s',
  segment_ttl    TEXT NOT NULL DEFAULT '12h',
  cors_origin    TEXT NOT NULL DEFAULT '*',
  brotli_level   INTEGER NOT NULL DEFAULT 5,
  status         TEXT NOT NULL DEFAULT 'active',
  config_version BIGINT NOT NULL DEFAULT 1,           -- bumped on change; agents compare to know when to re-pull
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- edge/cache rules per zone (Bunny "Edge Rules" equivalent)
CREATE TABLE cache_rules (
  id           BIGSERIAL PRIMARY KEY,
  zone_id      BIGINT NOT NULL REFERENCES zones(id) ON DELETE CASCADE,
  priority     INTEGER NOT NULL DEFAULT 0,
  match_type   TEXT NOT NULL,                         -- path_prefix|extension|regex
  match_value  TEXT NOT NULL,
  action       TEXT NOT NULL,                         -- override_cache_ttl|bypass_cache|force_download|redirect
  action_value TEXT,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- which servers serve which zones (Phase 2 = all; modeled for multi-PoP in Phase 3)
CREATE TABLE server_zones (
  server_id BIGINT NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
  zone_id   BIGINT NOT NULL REFERENCES zones(id) ON DELETE CASCADE,
  PRIMARY KEY (server_id, zone_id)
);

-- agent auth tokens (fleshed out in Step 2; table now). Store a HASH, never plaintext.
CREATE TABLE agent_tokens (
  id         BIGSERIAL PRIMARY KEY,
  server_id  BIGINT NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
  token_hash TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  revoked_at TIMESTAMPTZ
);

-- time-series stats (shipped by agents in Step 4) -> TimescaleDB hypertable
CREATE TABLE stats (
  time          TIMESTAMPTZ NOT NULL,
  server_id     BIGINT NOT NULL,
  zone_id       BIGINT,
  requests      BIGINT DEFAULT 0,
  hits          BIGINT DEFAULT 0,
  misses        BIGINT DEFAULT 0,
  bytes_sent    BIGINT DEFAULT 0,
  bandwidth_bps BIGINT DEFAULT 0,
  cpu_pct       DOUBLE PRECISION,
  ram_pct       DOUBLE PRECISION,
  disk_pct      DOUBLE PRECISION,
  hit_ratio     DOUBLE PRECISION
);
SELECT create_hypertable('stats', 'time', chunk_time_interval => INTERVAL '1 day');

-- helpful indexes
CREATE INDEX idx_servers_status ON servers (status);
CREATE INDEX idx_zones_account ON zones (account_id);
CREATE INDEX idx_cache_rules_zone ON cache_rules (zone_id);
CREATE INDEX idx_stats_server_time ON stats (server_id, time DESC);

-- seed the admin account (id=1) and advance the sequence past it
INSERT INTO accounts (id, name, role) VALUES (1, 'admin', 'admin');
SELECT setval(pg_get_serial_sequence('accounts', 'id'), 1, true);

-- +goose Down
DROP TABLE IF EXISTS stats;
DROP TABLE IF EXISTS agent_tokens;
DROP TABLE IF EXISTS server_zones;
DROP TABLE IF EXISTS cache_rules;
DROP TABLE IF EXISTS zones;
DROP TABLE IF EXISTS servers;
DROP TABLE IF EXISTS accounts;
