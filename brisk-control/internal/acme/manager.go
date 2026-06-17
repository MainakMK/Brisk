package acme

import (
	"context"
	"crypto/x509"
	"errors"
	"log/slog"
	"strings"
	"time"

	"brisk-control/internal/store"
)

// renewMargin: renew when the stored cert expires within this window.
const renewMargin = 30 * 24 * time.Hour

// Manager keeps the managed wildcard cert fresh: it issues on first run, then
// renews within renewMargin of expiry. The issued cert lives in the store and is
// fanned out to edges by the agent-config handler — the Manager never touches
// Nginx. It is intentionally idempotent and safe to run on every control-plane
// start (an in-margin cert is left untouched).
type Manager struct {
	issuer  *Issuer
	store   *store.Store
	name    string   // store key for the cert (e.g. apex "a2zjav.com")
	domains []string // SANs, e.g. ["*.a2zjav.com","a2zjav.com"]
	staging bool
	log     *slog.Logger
}

// NewManager builds a renewal manager. name is the store key; domains are the
// SANs to issue (wildcard first by convention).
func NewManager(issuer *Issuer, st *store.Store, name string, domains []string, staging bool, log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	return &Manager{issuer: issuer, store: st, name: name, domains: domains, staging: staging, log: log}
}

// EnsureOnce issues or renews the cert if needed and stores it. It returns
// (changed, err): changed is true when a new cert was written. Reasons to
// (re)issue: no stored cert, leaf unparsable, within renewMargin of expiry, or
// the staging flag flipped (staging<->production switch during cutover).
func (m *Manager) EnsureOnce(ctx context.Context) (bool, error) {
	cur, err := m.store.GetTLSCert(ctx, m.name)
	need := false
	var prev *x509.Certificate
	switch {
	case errors.Is(err, store.ErrNotFound):
		need = true
	case err != nil:
		return false, err
	default:
		if cur.Staging != m.staging {
			need = true // env switched (e.g. staging -> production cutover)
		}
		leaf := ParseLeaf([]byte(cur.FullChain))
		if leaf == nil {
			need = true
		} else {
			if time.Until(leaf.NotAfter) < renewMargin {
				need = true
			}
			// ARI replacement only makes sense renewing a real prod cert.
			if !m.staging && !cur.Staging {
				prev = leaf
			}
		}
	}
	if !need {
		return false, nil
	}

	b, err := m.issuer.Obtain(m.domains, prev)
	if err != nil {
		return false, err
	}

	rec := store.TLSCert{
		Name:      m.name,
		Domains:   strings.Join(m.domains, ","),
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
		return false, err
	}
	m.log.Info("tls: managed cert issued",
		"name", m.name, "domains", rec.Domains, "issuer", rec.Issuer,
		"serial", rec.Serial, "staging", m.staging, "not_after", rec.NotAfter)
	return true, nil
}

// Run issues on startup, then checks every 12h. On error it retries sooner with a
// capped backoff so a transient Bunny/ACME failure self-heals without spamming.
// It never returns until ctx is cancelled.
func (m *Manager) Run(ctx context.Context) {
	const (
		steady     = 12 * time.Hour
		minBackoff = 5 * time.Minute
		maxBackoff = 2 * time.Hour
	)
	backoff := minBackoff
	for {
		next := steady
		if _, err := m.EnsureOnce(ctx); err != nil {
			m.log.Warn("tls: ensure failed (will retry)", "name", m.name, "err", err.Error())
			next = backoff
			if backoff *= 2; backoff > maxBackoff {
				backoff = maxBackoff
			}
		} else {
			backoff = minBackoff
		}
		t := time.NewTimer(next)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
	}
}
