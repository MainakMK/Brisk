-- +goose Up
-- Hybrid Shield+PoP (Build Spec, Layer 1): a role=shield server can ALSO remain a
-- public PoP. serve_public=true (default) + role=shield => HYBRID (kept in the geo
-- DNS set AND acts as the parent/shield). serve_public=false + role=shield => pure
-- shield (excluded from DNS — today's behavior). Ignored for role=edge (edges are
-- always public). The column defaults true but is INERT until a box is role=shield,
-- so existing edge rows are unaffected and the live fleet stays byte-identical.
ALTER TABLE servers ADD COLUMN IF NOT EXISTS serve_public BOOLEAN NOT NULL DEFAULT true;

-- Byte-identical guarantee: any EXISTING role=shield server was a PURE shield
-- (excluded from DNS). Backfill those to serve_public=false so the new default of
-- true does not accidentally flip a live shield into a public PoP. Hybrid is an
-- explicit opt-in the operator toggles later per server.
UPDATE servers SET serve_public = false WHERE role = 'shield';

-- +goose Down
ALTER TABLE servers DROP COLUMN IF EXISTS serve_public;
