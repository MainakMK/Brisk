-- +goose Up
-- Purge job ledger — every API/dashboard purge is recorded here (status pending),
-- published to NATS, and flipped to done/partial as edges ack completion.
CREATE TABLE purge_jobs (
  id           BIGSERIAL PRIMARY KEY,
  account_id   BIGINT NOT NULL DEFAULT 1,
  zone_id      BIGINT REFERENCES zones(id) ON DELETE CASCADE,  -- NULL for purge-all
  type         TEXT NOT NULL,                       -- url | prefix | zone | all
  target       TEXT NOT NULL,                       -- e.g. /video/movie.mp4  ('*' for all)
  status       TEXT NOT NULL DEFAULT 'pending',     -- pending | done | partial | failed
  edges_total  INTEGER NOT NULL DEFAULT 0,
  edges_done   INTEGER NOT NULL DEFAULT 0,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  completed_at TIMESTAMPTZ
);

CREATE INDEX idx_purge_jobs_zone_created ON purge_jobs (zone_id, created_at DESC);
CREATE INDEX idx_purge_jobs_created ON purge_jobs (created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS purge_jobs;
