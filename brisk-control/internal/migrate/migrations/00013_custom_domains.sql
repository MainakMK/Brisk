-- +goose Up
-- Customer custom domains + per-domain auto-TLS (Phase 4 Step 2 — the commercial
-- gateway). A tenant points THEIR OWN domain (e.g. cdn.theirsite.com) at a Brisk
-- zone via CNAME; Brisk verifies the DNS lands on Brisk, then auto-issues a
-- per-domain Let's Encrypt cert (lego HTTP-01, answered by the edges' challenge
-- proxy), stores it in tls_certs (keyed by name = the domain), and fans it to all
-- edges over the existing agent-config pull channel. The edge serves the domain
-- via SNI (its own server block, same zone origin).
--
-- The lifecycle state machine (status):
--   pending_dns -> verifying -> issuing -> active -> (renewing) -> failed/removed
--   * pending_dns : added; waiting for the customer to create the CNAME.
--   * verifying   : DNS being (re)checked; never start ACME before this passes
--                   (the gate that protects the LE failed-validation rate limit
--                   AND prevents issuing for domains not actually routed to Brisk).
--   * issuing     : DNS verified; ACME HTTP-01 in flight (serialized).
--   * active      : cert issued + stored + fanned out; served via SNI.
--   * renewing    : transient during an automatic in-margin renewal.
--   * failed      : verification/issuance/renewal failed or the CNAME was removed;
--                   last_error carries the human-readable reason. A failed RENEWAL
--                   keeps serving the OLD cert until expiry (never drop TLS early).
--
-- Cert storage: the PEM material lives in tls_certs (same table as the managed
-- wildcard), keyed by name = domain. cert_name records that key. The dashboard
-- only ever sees status/last_error/expiry — never the key or chain.
--
-- Rate-limit discipline: attempt_count + next_attempt_at throttle retries with
-- exponential backoff so a stuck domain can't hammer the CA. Verification is the
-- abuse gate; issuance is serialized by the manager (one ACME job at a time).
CREATE TABLE custom_domains (
    id               BIGSERIAL   PRIMARY KEY,
    zone_id          BIGINT      NOT NULL REFERENCES zones(id) ON DELETE CASCADE,
    account_id       BIGINT      NOT NULL,
    domain           TEXT        NOT NULL UNIQUE,   -- a domain can't belong to two zones (409 on dup)
    status           TEXT        NOT NULL DEFAULT 'pending_dns',
    verify_method    TEXT        NOT NULL DEFAULT 'cname',
    cert_name        TEXT        NOT NULL DEFAULT '', -- tls_certs.name for this domain's cert (= domain)
    last_error       TEXT        NOT NULL DEFAULT '',
    attempt_count    INT         NOT NULL DEFAULT 0,  -- consecutive failed verify/issue attempts (backoff)
    last_verified_at TIMESTAMPTZ,                      -- last time the CNAME chain confirmed -> Brisk
    next_attempt_at  TIMESTAMPTZ,                      -- earliest next verify/issue (exponential backoff gate)
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The manager scans by status + next_attempt_at; the agent fan-out joins active
-- domains to a server's assigned zones.
CREATE INDEX custom_domains_zone_idx   ON custom_domains (zone_id);
CREATE INDEX custom_domains_status_idx ON custom_domains (status);

-- +goose Down
DROP TABLE IF EXISTS custom_domains;
