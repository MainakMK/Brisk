-- +goose Up
-- A rollout = one "Deploy this version to these PoPs" action. The engine opens one PoP's
-- wave at a time and advances only after a soak window of health. State lives entirely in
-- these tables so the engine resumes cleanly after a control-plane restart.
CREATE TABLE IF NOT EXISTS rollouts (
  id              BIGSERIAL PRIMARY KEY,
  release_version TEXT NOT NULL REFERENCES agent_releases(version),
  target_pops     TEXT[] NOT NULL,                 -- edge_ids in this rollout, in order
  soak_seconds    INT NOT NULL DEFAULT 90,         -- per-edge watch window before the next opens
  status          TEXT NOT NULL DEFAULT 'running', -- scheduled|running|paused|done|failed|cancelled
  scheduled_at    TIMESTAMPTZ,                     -- if set, the engine starts at this time
  error_reason    TEXT NOT NULL DEFAULT '',
  created_by      TEXT NOT NULL DEFAULT '',
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  started_at      TIMESTAMPTZ,
  finished_at     TIMESTAMPTZ
);

-- At most ONE active rollout (scheduled|running|paused) at a time. An expression index on the
-- constant TRUE, partial to active rows, makes a second active row collide on the same key.
CREATE UNIQUE INDEX IF NOT EXISTS one_active_rollout
  ON rollouts ((TRUE)) WHERE status IN ('scheduled', 'running', 'paused');

-- One row per PoP in a rollout. The control plane derives state from each edge's heartbeat
-- (agent_version) + health: queued -> in_progress (wave open) -> soaking -> done | failed | skipped.
CREATE TABLE IF NOT EXISTS rollout_targets (
  rollout_id   BIGINT NOT NULL REFERENCES rollouts(id) ON DELETE CASCADE,
  edge_id      TEXT NOT NULL,
  from_version TEXT NOT NULL DEFAULT '',
  to_version   TEXT NOT NULL,
  state        TEXT NOT NULL DEFAULT 'queued',
  error_reason TEXT NOT NULL DEFAULT '',
  soak_until   TIMESTAMPTZ,
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (rollout_id, edge_id)
);

-- Append-only audit of deploy/pause/rollback/upload actions (who/what/when).
CREATE TABLE IF NOT EXISTS audit_log (
  id         BIGSERIAL PRIMARY KEY,
  actor      TEXT NOT NULL DEFAULT '',
  action     TEXT NOT NULL,
  subject    TEXT NOT NULL DEFAULT '',
  details    TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS audit_log;
DROP TABLE IF EXISTS rollout_targets;
DROP TABLE IF EXISTS rollouts;
