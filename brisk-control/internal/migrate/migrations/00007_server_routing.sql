-- +goose Up
-- Per-server Smart-Record routing settings (Phase 3 Step 3). The reconciler turns
-- the flat cdn.<zone> set into a geo/latency smart-routed set driven by
-- servers.region; these columns let an operator tune one PoP without touching the
-- network-wide default.
--   routing_weight   0-100, splits load among same-location edges (10G box can
--                    outweigh a 1G box later). Default 100.
--   routing_override '' = use the network-wide BRISK_DNS_ROUTING_MODE; otherwise
--                    pin this server to 'geographic' or 'latency'.
ALTER TABLE servers
  ADD COLUMN routing_weight   INTEGER NOT NULL DEFAULT 100,
  ADD COLUMN routing_override TEXT    NOT NULL DEFAULT '';

-- Keep weight in the valid Smart-Record range.
ALTER TABLE servers
  ADD CONSTRAINT servers_routing_weight_range CHECK (routing_weight BETWEEN 0 AND 100);

-- +goose Down
ALTER TABLE servers DROP CONSTRAINT IF EXISTS servers_routing_weight_range;
ALTER TABLE servers DROP COLUMN IF EXISTS routing_override;
ALTER TABLE servers DROP COLUMN IF EXISTS routing_weight;
