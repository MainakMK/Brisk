-- +goose Up
-- Total physical RAM + cache-disk capacity (bytes) reported by the agent, so the
-- dashboard can show absolute "X GB free of Y GB" next to the live usage percentages.
-- Quasi-static (a box's hardware), so stored on the server row (not per stats sample);
-- the live AVAILABLE is derived from total × (1 − used%) at read time. Nullable: stays
-- NULL until an agent that reports totals checks in.
ALTER TABLE servers ADD COLUMN IF NOT EXISTS ram_total_bytes BIGINT;
ALTER TABLE servers ADD COLUMN IF NOT EXISTS disk_total_bytes BIGINT;

-- +goose Down
ALTER TABLE servers DROP COLUMN IF EXISTS disk_total_bytes;
ALTER TABLE servers DROP COLUMN IF EXISTS ram_total_bytes;
