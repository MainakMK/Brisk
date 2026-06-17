-- +goose Up
-- DNS deletion-protection lock (Phase 3). A singleton settings row guards
-- destructive DNS record operations: removing a record requires a deliberate,
-- time-delayed unlock (like a password-change cooldown) so routing records
-- can't be deleted by chance or by accident. Locked by default. Adding records
-- stays open (Step 2 auto-registration needs it); only removal is gated.
CREATE TABLE dns_settings (
  id                   INTEGER PRIMARY KEY DEFAULT 1,
  locked               BOOLEAN NOT NULL DEFAULT true,   -- protected by default
  unlock_requested_at  TIMESTAMPTZ,                     -- when the cooldown started (NULL = none pending)
  unlock_delay_seconds INTEGER NOT NULL DEFAULT 900,    -- cooldown before an unlock takes effect (15m default)
  updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT dns_settings_singleton CHECK (id = 1)
);

INSERT INTO dns_settings (id) VALUES (1) ON CONFLICT (id) DO NOTHING;

-- +goose Down
DROP TABLE IF EXISTS dns_settings;
