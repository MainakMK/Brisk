-- +goose Up
-- DNS reconcile audit trail (Phase 3 Step 2). Every DNS change the reconciler
-- applies to a brisk:server:<edge_id> routing record is recorded here, so the
-- dashboard (Step 5) and humans can see what happened and why.
CREATE TABLE dns_audit (
  id          BIGSERIAL PRIMARY KEY,
  action      TEXT NOT NULL,             -- add | enable | disable | update | remove
  edge_id     TEXT,                       -- the server this routing record belongs to
  record_name TEXT,                       -- routing record name (e.g. cdn)
  value       TEXT,                       -- the A-record IP
  record_id   BIGINT,                     -- the Bunny record id (when known)
  reason      TEXT NOT NULL,              -- heartbeat | status_change | server_delete | manual | periodic | drift
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_dns_audit_created ON dns_audit (created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS dns_audit;
