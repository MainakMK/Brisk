-- +goose Up
-- Quasi-static tech/runtime stack each agent reports about its PoP, so the dashboard
-- can show "what's running here": nginx + brisk-agent versions (already sent on the
-- heartbeat but previously discarded), plus OS pretty name, kernel release, and the Go
-- runtime the agent was built with. Stored on the server row (not per stats sample) and
-- refreshed on every heartbeat. Empty-string default: stays '' until an agent that
-- reports them checks in, and an older agent (which omits the newer fields) never wipes
-- existing values (the heartbeat UPDATE keeps a column when the incoming value is '').
ALTER TABLE servers ADD COLUMN IF NOT EXISTS nginx_version TEXT NOT NULL DEFAULT '';
ALTER TABLE servers ADD COLUMN IF NOT EXISTS agent_version TEXT NOT NULL DEFAULT '';
ALTER TABLE servers ADD COLUMN IF NOT EXISTS os_pretty     TEXT NOT NULL DEFAULT '';
ALTER TABLE servers ADD COLUMN IF NOT EXISTS kernel        TEXT NOT NULL DEFAULT '';
ALTER TABLE servers ADD COLUMN IF NOT EXISTS go_version    TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE servers DROP COLUMN IF EXISTS go_version;
ALTER TABLE servers DROP COLUMN IF EXISTS kernel;
ALTER TABLE servers DROP COLUMN IF EXISTS os_pretty;
ALTER TABLE servers DROP COLUMN IF EXISTS agent_version;
ALTER TABLE servers DROP COLUMN IF EXISTS nginx_version;
