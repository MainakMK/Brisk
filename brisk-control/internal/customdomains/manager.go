package customdomains

import (
	"context"
	"crypto/x509"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"brisk-control/internal/acme"
	"brisk-control/internal/store"
)

// Lifecycle timing.
const (
	scanInterval = 30 * time.Second    // how often the loop re-scans all domains
	renewMargin  = 30 * 24 * time.Hour // renew when the cert expires within this window

	// Pending-verification poll cadence (cheap DNS lookups, no ACME spent): retry
	// roughly every pendingBackoffBase, growing mildly, capped — it's normal to wait
	// minutes-to-hours for a customer to create the CNAME.
	pendingBackoffBase = 2 * time.Minute
	pendingBackoffMax  = 15 * time.Minute

	// Issuance/renewal failure backoff (ACME spent). Floored well above the LE
	// "5 failed validations/hour" limit (20m floor => <=3/hour) and grows to a day.
	issueBackoffBase = 20 * time.Minute
	issueBackoffMax  = 12 * time.Hour
)

// Manager drives the custom-domain state machine: it verifies DNS, issues +
// renews per-domain HTTP-01 certs (serialized), stores them in tls_certs, and
// bumps the parent zone's config_version so edges re-pull the new vhost/cert.
//
// It NEVER touches Nginx (the agents do, over the config-pull channel) and is
// safe to run continuously: an in-margin cert is left alone, a stuck domain backs
// off, and a failed renewal keeps the old cert serving (never drops TLS early).
type Manager struct {
	store    *store.Store
	issuer   *acme.Issuer
	chal     *acme.ChallengeStore
	verifier *Verifier
	staging  bool
	log      *slog.Logger

	issueMu sync.Mutex // serialize ACME issuance — one job at a time (no CA thundering herd)
}

// NewManager builds the lifecycle manager. issuer/chal are the shared HTTP-01
// issuer + challenge store (the latter also read by the control-plane challenge
// handler). staging selects the LE staging directory (always iterate on staging).
func NewManager(st *store.Store, issuer *acme.Issuer, chal *acme.ChallengeStore, staging bool, log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	return &Manager{store: st, issuer: issuer, chal: chal, verifier: NewVerifier(), staging: staging, log: log}
}

// Run scans on a ticker until ctx is cancelled. Each tick processes every domain
// whose backoff gate (next_attempt_at) has elapsed.
func (m *Manager) Run(ctx context.Context) {
	m.log.Info("custom-domain manager running", "staging", m.staging, "scan", scanInterval.String())
	t := time.NewTicker(scanInterval)
	defer t.Stop()
	m.scan(ctx) // immediate first pass
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.scan(ctx)
		}
	}
}

// scan walks all domains and advances any that are due.
func (m *Manager) scan(ctx context.Context) {
	domains, err := m.store.ListAllCustomDomains(ctx)
	if err != nil {
		m.log.Warn("custom-domain scan: list failed", "err", err.Error())
		return
	}
	now := time.Now()
	for _, d := range domains {
		if d.NextAttemptAt != nil && now.Before(*d.NextAttemptAt) {
			continue // backoff gate not yet elapsed
		}
		m.process(ctx, d)
	}
}

// CheckNow runs one immediate verify/issue pass for a single domain (the "check
// now" button), bypassing the backoff gate, and returns the refreshed row.
func (m *Manager) CheckNow(ctx context.Context, id int64) (store.CustomDomain, error) {
	d, err := m.store.GetCustomDomain(ctx, id)
	if err != nil {
		return store.CustomDomain{}, err
	}
	m.process(ctx, d)
	return m.store.GetCustomDomain(ctx, id)
}

// process advances one domain by its current status.
func (m *Manager) process(ctx context.Context, d store.CustomDomain) {
	switch d.Status {
	case store.CustomDomainActive, store.CustomDomainRenewing:
		m.maybeRenew(ctx, d)
	default: // pending_dns, verifying, issuing, failed -> (re)verify then issue
		m.verifyThenIssue(ctx, d)
	}
}

// verifyThenIssue runs the DNS gate and, if it passes, issues the cert. ACME is
// never attempted before DNS verifies (rate-limit + abuse gate).
func (m *Manager) verifyThenIssue(ctx context.Context, d store.CustomDomain) {
	expectCNAMEs, expectIPs, err := m.expectations(ctx, d)
	if err != nil {
		m.log.Warn("custom-domain: load expectations failed", "domain", d.Domain, "err", err.Error())
		return
	}

	_ = m.store.SetCustomDomainStatus(ctx, d.ID, store.CustomDomainVerifying, d.LastError)
	res := m.verifier.Verify(ctx, d.Domain, expectCNAMEs, expectIPs)
	if !res.OK {
		// Stay pending; mild backoff (no ACME spent). Surface the human detail.
		next := backoff(pendingBackoffBase, pendingBackoffMax, int(d.AttemptCount))
		_ = m.store.MarkCustomDomainFailure(ctx, d.ID, store.CustomDomainPendingDNS, res.Detail, time.Now().Add(next))
		m.log.Info("custom-domain: not verified yet", "domain", d.Domain, "detail", res.Detail)
		return
	}
	_ = m.store.MarkCustomDomainVerified(ctx, d.ID)

	// Verified -> issue. Serialize ACME (one job at a time).
	_ = m.store.SetCustomDomainStatus(ctx, d.ID, store.CustomDomainIssuing, "")
	m.log.Info("custom-domain: verified, issuing", "domain", d.Domain, "detail", res.Detail, "staging", m.staging)

	if err := m.issue(ctx, d.Domain, nil); err != nil {
		next := backoff(issueBackoffBase, issueBackoffMax, int(d.AttemptCount))
		_ = m.store.MarkCustomDomainFailure(ctx, d.ID, store.CustomDomainFailed, "issuance failed: "+err.Error(), time.Now().Add(next))
		m.log.Warn("custom-domain: issuance failed", "domain", d.Domain, "err", err.Error(),
			"retry_at", time.Now().Add(next).Format(time.RFC3339))
		return
	}
	_ = m.store.SetCustomDomainActive(ctx, d.ID, d.Domain)
	_ = m.store.BumpZoneConfigVersion(ctx, d.ZoneID) // edges re-pull -> new vhost+cert
	m.log.Info("custom-domain: ACTIVE", "domain", d.Domain, "zone_id", d.ZoneID)
}

// maybeRenew renews an active domain's cert when it is within renewMargin of
// expiry. It RE-VERIFIES DNS first; if the CNAME was removed it records the error
// and backs off but KEEPS serving the old cert (never drops TLS early).
func (m *Manager) maybeRenew(ctx context.Context, d store.CustomDomain) {
	cur, err := m.store.GetTLSCert(ctx, certName(d))
	if err != nil {
		// No stored cert for an "active" domain -> treat as a fresh issue.
		m.verifyThenIssue(ctx, d)
		return
	}
	leaf := acme.ParseLeaf([]byte(cur.FullChain))
	envSwitched := cur.Staging != m.staging
	if leaf != nil && time.Until(leaf.NotAfter) > renewMargin && !envSwitched {
		return // healthy and not in margin — nothing to do
	}

	// In margin (or env switched). Re-verify DNS BEFORE spending an ACME attempt.
	expectCNAMEs, expectIPs, err := m.expectations(ctx, d)
	if err != nil {
		m.log.Warn("custom-domain: renew expectations failed", "domain", d.Domain, "err", err.Error())
		return
	}
	res := m.verifier.Verify(ctx, d.Domain, expectCNAMEs, expectIPs)
	if !res.OK {
		// Detached CNAME: alert + back off, but keep status active (old cert keeps
		// serving until expiry). Don't hammer ACME for a domain that left.
		next := backoff(issueBackoffBase, issueBackoffMax, int(d.AttemptCount))
		_ = m.store.MarkCustomDomainFailure(ctx, d.ID, store.CustomDomainActive,
			"renewal blocked — DNS no longer points at Brisk: "+res.Detail, time.Now().Add(next))
		m.log.Warn("custom-domain: renewal blocked (DNS detached), keeping old cert", "domain", d.Domain, "detail", res.Detail)
		return
	}

	_ = m.store.SetCustomDomainStatus(ctx, d.ID, store.CustomDomainRenewing, "")
	var prev *x509.Certificate
	if leaf != nil && !cur.Staging && !m.staging {
		prev = leaf // ARI replacement only for prod->prod renewals
	}
	if err := m.issue(ctx, d.Domain, prev); err != nil {
		next := backoff(issueBackoffBase, issueBackoffMax, int(d.AttemptCount))
		// Keep serving the old cert; mark active + error (not failed -> vhost stays).
		_ = m.store.MarkCustomDomainFailure(ctx, d.ID, store.CustomDomainActive,
			"renewal failed (old cert still serving): "+err.Error(), time.Now().Add(next))
		m.log.Warn("custom-domain: renewal failed, keeping old cert", "domain", d.Domain, "err", err.Error())
		return
	}
	_ = m.store.SetCustomDomainActive(ctx, d.ID, d.Domain)
	_ = m.store.BumpZoneConfigVersion(ctx, d.ZoneID) // new serial -> edges re-pull the renewed cert
	m.log.Info("custom-domain: renewed", "domain", d.Domain)
}

// issue runs the serialized ACME HTTP-01 obtain and stores the cert in tls_certs
// (keyed by the domain). prev (a real prior leaf) enables ARI on renewal.
func (m *Manager) issue(ctx context.Context, domain string, prev *x509.Certificate) error {
	m.issueMu.Lock() // one ACME job at a time across the loop + manual triggers
	defer m.issueMu.Unlock()

	b, err := m.issuer.ObtainHTTP01(domain, m.chal, prev)
	if err != nil {
		return err
	}
	rec := store.TLSCert{
		Name:      domain,
		Domains:   domain,
		FullChain: string(b.FullChain),
		PrivKey:   string(b.PrivKey),
		Staging:   m.staging,
	}
	if b.Leaf != nil {
		rec.Issuer = b.Leaf.Issuer.CommonName
		rec.Serial = b.Leaf.SerialNumber.Text(16)
		nb, na := b.Leaf.NotBefore, b.Leaf.NotAfter
		rec.NotBefore, rec.NotAfter = &nb, &na
	}
	if err := m.store.UpsertTLSCert(ctx, rec); err != nil {
		return fmt.Errorf("store cert: %w", err)
	}
	return nil
}

// expectations returns the CNAME targets + edge IPs that count as "pointed at
// Brisk" for a domain: the parent zone's CDN hostname, and every known edge IP.
func (m *Manager) expectations(ctx context.Context, d store.CustomDomain) (cnames, ips []string, err error) {
	z, err := m.store.GetZone(ctx, d.ZoneID)
	if err != nil {
		return nil, nil, err
	}
	cnames = []string{z.CDNHostname}
	servers, err := m.store.ListServers(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, s := range servers {
		if ip := strings.TrimSpace(s.IP); ip != "" {
			ips = append(ips, ip)
		}
	}
	return cnames, ips, nil
}

// certName is the tls_certs store key for a domain's cert (its CertName, else the
// domain itself for older rows).
func certName(d store.CustomDomain) string {
	if strings.TrimSpace(d.CertName) != "" {
		return d.CertName
	}
	return d.Domain
}

// backoff returns base*2^n capped at max.
func backoff(base, max time.Duration, n int) time.Duration {
	d := base
	for i := 0; i < n && d < max; i++ {
		d *= 2
	}
	if d > max {
		d = max
	}
	return d
}
