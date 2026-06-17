-- +goose Up
-- Capacity-weighted routing (opt-in, default OFF). When weight_by_capacity is true for
-- a server, the DNS reconciler derives that PoP's Smart-Record weight from its
-- capacity_mbps (normalized against the fleet's biggest capacity-weighted box) instead
-- of the manual routing_weight. Default false => the column is inert and every PoP keeps
-- using its manual routing_weight (today's behavior, byte-identical DNS output).
ALTER TABLE servers ADD COLUMN IF NOT EXISTS weight_by_capacity BOOLEAN NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE servers DROP COLUMN IF EXISTS weight_by_capacity;
