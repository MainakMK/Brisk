// Package acme issues and renews the Brisk wildcard TLS certificate from the
// CONTROL PLANE using lego's Bunny DNS-01 challenge (Phase 3.7 Step 2, Part 3).
//
// Why control-plane-central (vs. per-edge acme.sh): wildcard certs REQUIRE
// DNS-01, and a wildcard names a TXT record under the shared zone
// (_acme-challenge.a2zjav.com). If all 3 edges tried to solve it independently
// they would race on that record. Issuing once, centrally, with the single Bunny
// key the control plane already holds, removes the race, keeps the key in one
// place, and is the natural home for Phase-4 custom-domain certs. The issued cert
// is stored in Postgres (store.TLSCert) and fanned out to edges over the existing
// agent-config pull channel.
//
//   - Key type: ECDSA P-256 (certcrypto.EC256) — Brisk standard (CLAUDE.md).
//   - Staging vs production via Issuer.Staging (always validate on staging first).
//   - The ACME account key is persisted (AccountDir) so renewals reuse the account.
//   - Renewals pass the ARI "replaces" id, exempt from issuance rate limits.
package acme

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"

	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/challenge/dns01"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/providers/dns/bunny"
	"github.com/go-acme/lego/v4/registration"
)

// leStagingURL is the Let's Encrypt staging directory (prod is lego's default).
const leStagingURL = "https://acme-staging-v02.api.letsencrypt.org/directory"

// Bundle is a freshly issued certificate + its private key, plus the parsed leaf
// for metadata (issuer/serial/validity).
type Bundle struct {
	FullChain []byte
	PrivKey   []byte
	Leaf      *x509.Certificate
}

// Issuer obtains certs via lego + Bunny DNS-01. Reuse the control plane's Bunny
// API key (never logged). AccountDir persists the ACME account key across runs.
type Issuer struct {
	Email      string // ACME account email (required)
	Staging    bool   // use the Let's Encrypt staging directory
	BunnyKey   string // Bunny API key for the DNS-01 TXT challenge (never logged)
	AccountDir string // dir holding the persisted ACME account key
}

// acmeUser implements lego's registration.User.
type acmeUser struct {
	email string
	reg   *registration.Resource
	key   crypto.PrivateKey
}

func (u *acmeUser) GetEmail() string                        { return u.email }
func (u *acmeUser) GetRegistration() *registration.Resource { return u.reg }
func (u *acmeUser) GetPrivateKey() crypto.PrivateKey        { return u.key }

// Obtain issues a single certificate covering all of domains (the first is the
// CN; e.g. ["*.a2zjav.com","a2zjav.com"]). If prev is a real prior leaf, its ARI
// id is sent as the replacement so the renewal skips rate limits.
func (i *Issuer) Obtain(domains []string, prev *x509.Certificate) (*Bundle, error) {
	if i.Email == "" {
		return nil, fmt.Errorf("acme: account email required (BRISK_TLS_EMAIL)")
	}
	if i.BunnyKey == "" {
		return nil, fmt.Errorf("acme: bunny api key required for DNS-01")
	}
	if len(domains) == 0 {
		return nil, fmt.Errorf("acme: no domains to issue")
	}

	user, err := i.user()
	if err != nil {
		return nil, err
	}

	cfg := lego.NewConfig(user)
	cfg.Certificate.KeyType = certcrypto.EC256 // ECDSA P-256 leaf
	if i.Staging {
		cfg.CADirURL = leStagingURL
	}

	client, err := lego.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("acme client: %w", err)
	}

	// Bunny DNS-01 provider, configured with our key directly (no global env).
	pcfg := bunny.NewDefaultConfig()
	pcfg.APIKey = i.BunnyKey
	provider, err := bunny.NewDNSProviderConfig(pcfg)
	if err != nil {
		return nil, fmt.Errorf("bunny dns-01 provider: %w", err)
	}
	// Check TXT propagation against PUBLIC recursive resolvers (not the container's
	// 127.0.0.11 Docker DNS, which caches/merges TXT poorly), and relax the
	// "all nameservers agree" requirement. The wildcard + apex authorizations share
	// the same _acme-challenge.<zone> TXT name with two values; the strict check
	// against the internal resolver stalls. Let's Encrypt validates against the
	// AUTHORITATIVE Bunny nameservers anyway, so a best-effort precheck is enough.
	if err := client.Challenge.SetDNS01Provider(provider,
		dns01.AddRecursiveNameservers([]string{"1.1.1.1:53", "8.8.8.8:53"}),
		dns01.DisableCompletePropagationRequirement(),
	); err != nil {
		return nil, fmt.Errorf("set dns-01 provider: %w", err)
	}

	reg, err := registerAccount(client)
	if err != nil {
		return nil, err
	}
	user.reg = reg

	req := certificate.ObtainRequest{Domains: domains, Bundle: true}
	if prev != nil {
		if id, err := certificate.MakeARICertID(prev); err == nil {
			req.ReplacesCertID = id
		}
	}

	res, err := client.Certificate.Obtain(req)
	if err != nil {
		return nil, fmt.Errorf("obtain certificate for %v: %w", domains, err)
	}
	return &Bundle{
		FullChain: res.Certificate,
		PrivKey:   res.PrivateKey,
		Leaf:      ParseLeaf(res.Certificate),
	}, nil
}

// registerAccount registers (or resolves an existing) ACME account for the key.
func registerAccount(client *lego.Client) (*registration.Resource, error) {
	reg, err := client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
	if err == nil {
		return reg, nil
	}
	if existing, rErr := client.Registration.ResolveAccountByKey(); rErr == nil {
		return existing, nil
	}
	return nil, fmt.Errorf("acme register: %w", err)
}

// user loads or creates a persisted ACME account (separate per environment).
func (i *Issuer) user() (*acmeUser, error) {
	env := "production"
	if i.Staging {
		env = "staging"
	}
	dir := filepath.Join(i.AccountDir, env)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create acme account dir: %w", err)
	}
	key, err := loadOrCreateAccountKey(filepath.Join(dir, "account.key"))
	if err != nil {
		return nil, err
	}
	return &acmeUser{email: i.Email, key: key}, nil
}

// loadOrCreateAccountKey returns a persisted ECDSA account key, creating it once.
func loadOrCreateAccountKey(path string) (crypto.PrivateKey, error) {
	if raw, err := os.ReadFile(path); err == nil {
		if block, _ := pem.Decode(raw); block != nil {
			if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
				return k, nil
			}
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate account key: %w", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		return nil, fmt.Errorf("persist account key: %w", err)
	}
	return key, nil
}

// ParseLeaf parses the first (leaf) certificate from a PEM bundle, or nil.
func ParseLeaf(pemBytes []byte) *x509.Certificate {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil
	}
	return cert
}
