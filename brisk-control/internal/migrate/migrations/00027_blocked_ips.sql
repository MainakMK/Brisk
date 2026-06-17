-- +goose Up
-- Per-zone Blocked-IP denylist (Bunny-style "Blocked IPs"). A single comma-separated
-- TEXT column of IPs / CIDRs (validated on write). EMPTY (the default) => OFF, so every
-- existing zone (incl. live ones) renders byte-identical nginx until a tenant adds an
-- entry. The agent emits `blocked_ips` only when non-empty (omitempty), so a default
-- zone's agent-config payload + ETag are unchanged. Enforced at the edge via nginx
-- `deny <cidr>;` on CONTENT locations only (never /healthz or the ACME challenge, so the
-- control-plane health checker can never be locked out). Deny-only => all other IPs pass.
ALTER TABLE zones
  ADD COLUMN blocked_ips TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE zones
  DROP COLUMN blocked_ips;
