-- +goose Up
-- Control-plane-managed TLS certificates (Phase 3.7 Step 2, Part 3).
--
-- The control plane issues a wildcard cert (e.g. "*.a2zjav.com" + apex
-- "a2zjav.com") once via lego's Bunny DNS-01 challenge, stores it here, and ships
-- the PEM material to edges over the existing agent-config pull channel. This
-- replaces per-edge acme.sh: one issuer, one Bunny key (central), no 3-edge race
-- on the shared _acme-challenge TXT, and it is the natural home for Phase-4
-- custom-domain certs.
--
-- Why store the private key in the DB: the agent-config handler reads it here to
-- fan it out to edges. It is the same trust level as the Bunny API key already in
-- the control plane's env. The control plane is the single secret-holder; the row
-- is never exposed via the dashboard (/tls/status returns metadata only, never the
-- key or chain body).
--
--   name        stable key for the cert, e.g. the apex "a2zjav.com"
--   domains     comma-joined SAN list, e.g. "*.a2zjav.com,a2zjav.com"
--   staging     issued from the Let's Encrypt STAGING directory (untrusted; test)
--   serial      leaf serial (hex) — changes on every (re)issue; drives the agent
--               config ETag so a renewal triggers a pull
CREATE TABLE tls_certs (
    id          BIGSERIAL   PRIMARY KEY,
    name        TEXT        NOT NULL UNIQUE,
    domains     TEXT        NOT NULL,
    fullchain   TEXT        NOT NULL,
    privkey     TEXT        NOT NULL,
    issuer      TEXT        NOT NULL DEFAULT '',
    serial      TEXT        NOT NULL DEFAULT '',
    staging     BOOLEAN     NOT NULL DEFAULT false,
    not_before  TIMESTAMPTZ,
    not_after   TIMESTAMPTZ,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS tls_certs;
