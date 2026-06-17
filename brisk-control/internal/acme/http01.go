// HTTP-01 issuance for CUSTOMER custom domains (Phase 4 Step 2).
//
// Why HTTP-01 (not DNS-01) for custom domains: Brisk does NOT control the
// customer's DNS, so it can't write their _acme-challenge TXT. But once the
// customer CNAMEs cdn.theirsite.com -> their Brisk zone hostname, ALL HTTP traffic
// for that domain — including the CA's validation request — already lands on
// Brisk's edges. So Brisk answers the challenge itself:
//
//   - lego runs centrally here (same process as the API), with this in-memory
//     ChallengeStore as its HTTP-01 provider: Present() records token->keyAuth,
//     CleanUp() drops it.
//   - Every edge proxies :80 /.well-known/acme-challenge/* to the control plane
//     over the agent tunnel, and the control plane's challenge handler serves the
//     keyAuth from this same store. So WHICHEVER geo-routed edge the CA hits, the
//     challenge answers correctly (solves the classic multi-server HTTP-01 problem
//     — the CA may validate from multiple vantage points and hit any edge).
//
// The store lives in the control-plane process and is shared by reference between
// the issuer (writer) and the HTTP handler (reader) — no Postgres round-trip on
// the CA's hot validation path. Issuance is serialized by the manager, but the map
// is mutex-guarded and holds multiple tokens safely regardless.
package acme

import (
	"crypto/x509"
	"fmt"
	"sync"

	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/lego"
)

// ChallengeStore is lego's HTTP-01 provider AND the source the control-plane
// challenge handler reads. It maps an ACME token to its keyAuth payload.
type ChallengeStore struct {
	mu   sync.RWMutex
	keys map[string]string // token -> keyAuth
}

// NewChallengeStore returns an empty challenge store.
func NewChallengeStore() *ChallengeStore {
	return &ChallengeStore{keys: make(map[string]string)}
}

// Present implements lego's challenge.Provider: record the keyAuth so the
// control-plane handler can serve http://<domain>/.well-known/acme-challenge/<token>.
func (s *ChallengeStore) Present(domain, token, keyAuth string) error {
	s.mu.Lock()
	s.keys[token] = keyAuth
	s.mu.Unlock()
	return nil
}

// CleanUp implements lego's challenge.Provider: drop the token after validation.
func (s *ChallengeStore) CleanUp(domain, token, keyAuth string) error {
	s.mu.Lock()
	delete(s.keys, token)
	s.mu.Unlock()
	return nil
}

// Get returns the keyAuth for an ACME token (read by the challenge HTTP handler).
func (s *ChallengeStore) Get(token string) (string, bool) {
	s.mu.RLock()
	v, ok := s.keys[token]
	s.mu.RUnlock()
	return v, ok
}

// ObtainHTTP01 issues a single-domain certificate via the ACME HTTP-01 challenge,
// answered through Brisk's edges by the shared ChallengeStore. Unlike the wildcard
// DNS-01 path it needs NO Bunny key — the customer's DNS is not touched.
//
// If prev is a real prior leaf, its ARI replacement id is sent so a renewal skips
// the issuance rate limit. The returned Bundle carries the fullchain + key + leaf.
func (i *Issuer) ObtainHTTP01(domain string, store *ChallengeStore, prev *x509.Certificate) (*Bundle, error) {
	if i.Email == "" {
		return nil, fmt.Errorf("acme: account email required (BRISK_TLS_EMAIL)")
	}
	if domain == "" {
		return nil, fmt.Errorf("acme: no domain to issue")
	}
	if store == nil {
		return nil, fmt.Errorf("acme: nil challenge store")
	}

	user, err := i.user()
	if err != nil {
		return nil, err
	}

	cfg := lego.NewConfig(user)
	cfg.Certificate.KeyType = certcrypto.EC256 // ECDSA P-256 leaf (Brisk standard)
	if i.Staging {
		cfg.CADirURL = leStagingURL
	}

	client, err := lego.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("acme client: %w", err)
	}

	// The control plane is the only HTTP-01 solver — it never binds :80 itself
	// (the edges proxy the challenge to it). lego just needs Present/CleanUp.
	if err := client.Challenge.SetHTTP01Provider(store); err != nil {
		return nil, fmt.Errorf("set http-01 provider: %w", err)
	}

	reg, err := registerAccount(client)
	if err != nil {
		return nil, err
	}
	user.reg = reg

	req := certificate.ObtainRequest{Domains: []string{domain}, Bundle: true}
	if prev != nil {
		if id, err := certificate.MakeARICertID(prev); err == nil {
			req.ReplacesCertID = id
		}
	}

	res, err := client.Certificate.Obtain(req)
	if err != nil {
		return nil, fmt.Errorf("obtain http-01 certificate for %s: %w", domain, err)
	}
	return &Bundle{
		FullChain: res.Certificate,
		PrivKey:   res.PrivateKey,
		Leaf:      ParseLeaf(res.Certificate),
	}, nil
}
