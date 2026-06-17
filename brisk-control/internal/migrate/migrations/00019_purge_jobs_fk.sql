-- +goose Up
-- Phase 4 Step 7 Part 4: purge history must SURVIVE zone deletion. Today
-- purge_jobs.zone_id is ON DELETE CASCADE, so deleting a zone erases its entire
-- purge audit trail (and the zone-wide teardown purge row we just wrote on delete
-- would vanish with it). Switch the FK to ON DELETE SET NULL and snapshot the zone
-- hostname onto each row at creation, so the history stays meaningful after the
-- zone row is gone.
ALTER TABLE purge_jobs ADD COLUMN IF NOT EXISTS hostname TEXT NOT NULL DEFAULT '';

-- Backfill hostname for existing rows from their zone (best-effort; purge-all rows
-- have no zone and keep '').
UPDATE purge_jobs p SET hostname = z.cdn_hostname
  FROM zones z WHERE p.zone_id = z.id AND p.hostname = '';

-- Re-point the FK from CASCADE to SET NULL: a zone delete now NULLs zone_id but
-- keeps the row, with hostname preserved for the audit trail.
ALTER TABLE purge_jobs DROP CONSTRAINT IF EXISTS purge_jobs_zone_id_fkey;
ALTER TABLE purge_jobs
  ADD CONSTRAINT purge_jobs_zone_id_fkey
  FOREIGN KEY (zone_id) REFERENCES zones(id) ON DELETE SET NULL;

-- +goose Down
ALTER TABLE purge_jobs DROP CONSTRAINT IF EXISTS purge_jobs_zone_id_fkey;
ALTER TABLE purge_jobs
  ADD CONSTRAINT purge_jobs_zone_id_fkey
  FOREIGN KEY (zone_id) REFERENCES zones(id) ON DELETE CASCADE;
ALTER TABLE purge_jobs DROP COLUMN IF EXISTS hostname;
