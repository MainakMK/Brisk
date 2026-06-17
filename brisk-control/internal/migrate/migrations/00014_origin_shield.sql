-- +goose Up
-- Origin Shield — mid-tier cache, per zone (Phase 4 Step 3).
--
-- A "shield" is another Brisk PoP that sits in front of the origin. For a shielded
-- zone, normal edges proxy that zone's cache-misses to the SHIELD (which caches the
-- zone under the same key) instead of each edge hitting the origin — so many edges
-- missing the same object collapse to ~one origin fetch (proxy_cache_lock at both
-- tiers). The shield itself, and any edge that is its own shield, go to the origin
-- directly (loop guard, enforced in the control plane).
--
--   servers.role            'edge' (default, user-facing, in the geo DNS set) |
--                           'shield' (mid-tier, NOT in DNS rotation; nearest origin)
--   zones.origin_shield_enabled   per-zone opt-in (great for static/video; little
--                           benefit for dynamic — extra hop, no consolidation)
--   zones.shield_server_id  which shield PoP fronts this zone. NULL => use the
--                           network-wide default (BRISK_DEFAULT_SHIELD_SERVER_ID).
--                           ON DELETE SET NULL so deleting a shield PoP cleanly
--                           reverts its zones to direct-origin (degrade, never break).
ALTER TABLE servers
  ADD COLUMN role TEXT NOT NULL DEFAULT 'edge';

ALTER TABLE zones
  ADD COLUMN origin_shield_enabled BOOLEAN NOT NULL DEFAULT false,
  ADD COLUMN shield_server_id      BIGINT REFERENCES servers(id) ON DELETE SET NULL;

CREATE INDEX servers_role_idx ON servers (role);

-- +goose Down
DROP INDEX IF EXISTS servers_role_idx;
ALTER TABLE zones
  DROP COLUMN IF EXISTS shield_server_id,
  DROP COLUMN IF EXISTS origin_shield_enabled;
ALTER TABLE servers
  DROP COLUMN IF EXISTS role;
