-- +goose Up
-- Admin drain / maintenance mode (Phase 3 Step 5). Distinct from `status` and
-- from health: an operator can pull a PoP out of rotation (record Disabled=true,
-- box keeps serving in-flight requests) and bring it back. Precedence in the
-- reconciler is drain > health > heartbeat, so a drained PoP stays out even when
-- healthy. off != delete — drain reuses the Step-2 "disabled-but-kept" state.
--   drained       true = operator pulled this PoP from rotation
--   drained_at    when it was drained (for the UI timeline)
--   drain_reason  optional operator note
ALTER TABLE servers
  ADD COLUMN drained      BOOLEAN     NOT NULL DEFAULT false,
  ADD COLUMN drained_at   TIMESTAMPTZ,
  ADD COLUMN drain_reason TEXT        NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE servers
  DROP COLUMN IF EXISTS drain_reason,
  DROP COLUMN IF EXISTS drained_at,
  DROP COLUMN IF EXISTS drained;
