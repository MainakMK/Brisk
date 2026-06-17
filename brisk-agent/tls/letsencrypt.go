// Let's Encrypt (ACME) certificate path — PRODUCTION / VPS.
//
// HTTP-01 via WEBROOT: lego writes the challenge token under
// <WebrootDir>/.well-known/acme-challenge/ and Nginx serves it on port 80. This
// works while Nginx is running (unlike a standalone :80 listener), so it covers
// both initial issuance (after the self-signed placeholder lets Nginx start) and
// renewals on the long-running agent.
//
// - Key type: ECDSA P-256 (certcrypto.EC256).
// - Staging vs production selected by Manager.Staging (always test on staging
//   first — production has strict rate limits).
// - The ACME account key is persisted so renewals reuse the account.
// - Renewals send the ARI "replaces" cert id, which is exempt from rate limits.
package tls

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
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/providers/http/webroot"
	"github.com/go-acme/lego/v4/registration"
)

// Let's Encrypt staging directory (production is lego's built-in default).
const leStagingURL = "https://acme-staging-v02.api.letsencrypt.org/directory"

// leUser implements lego's registration.User.
type leUser struct {
	email string
	reg   *registration.Resource
	key   crypto.PrivateKey
}

func (u *leUser) GetEmail() string                        { return u.email }
func (u *leUser) GetRegistration() *registration.Resource { return u.reg }
func (u *leUser) GetPrivateKey() crypto.PrivateKey        { return u.key }

// ObtainLetsEncrypt obtains or renews a real ECDSA cert for domain via ACME
// HTTP-01 over the Nginx-served webroot. Idempotent at the caller via
// NeedsLetsEncrypt; this always performs the ACME exchange when invoked.
func (m *Manager) ObtainLetsEncrypt(domain string) error {
	if m.Email == "" {
		return fmt.Errorf("letsencrypt requires an account email (letsencrypt_email)")
	}
	p := m.Paths(domain)
	if err := os.MkdirAll(p.Dir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(m.WebrootDir, ".well-known", "acme-challenge"), 0o755); err != nil {
		return fmt.Errorf("create webroot: %w", err)
	}

	user, err := m.acmeUser()
	if err != nil {
		return err
	}

	cfg := lego.NewConfig(user)
	cfg.Certificate.KeyType = certcrypto.EC256 // ECDSA P-256 leaf
	if m.Staging {
		cfg.CADirURL = leStagingURL
	}

	client, err := lego.NewClient(cfg)
	if err != nil {
		return fmt.Errorf("acme client: %w", err)
	}
	provider, err := webroot.NewHTTPProvider(m.WebrootDir)
	if err != nil {
		return fmt.Errorf("webroot provider: %w", err)
	}
	if err := client.Challenge.SetHTTP01Provider(provider); err != nil {
		return fmt.Errorf("set http-01 provider: %w", err)
	}

	reg, err := registerAccount(client)
	if err != nil {
		return err
	}
	user.reg = reg

	req := certificate.ObtainRequest{Domains: []string{domain}, Bundle: true}
	// ARI: if a real cert already exists, mark this as its replacement so the
	// renewal is exempt from rate limits.
	if old := loadLeafCert(p.FullChain); old != nil && !isSelfSigned(old) {
		if id, err := certificate.MakeARICertID(old); err == nil {
			req.ReplacesCertID = id
		}
	}

	res, err := client.Certificate.Obtain(req)
	if err != nil {
		return fmt.Errorf("obtain certificate for %s: %w", domain, err)
	}
	if err := os.WriteFile(p.FullChain, res.Certificate, 0o644); err != nil {
		return fmt.Errorf("write %q: %w", p.FullChain, err)
	}
	if err := os.WriteFile(p.PrivKey, res.PrivateKey, 0o600); err != nil {
		return fmt.Errorf("write %q: %w", p.PrivKey, err)
	}
	return nil
}

// registerAccount registers (or resolves an existing) ACME account for the key.
func registerAccount(client *lego.Client) (*registration.Resource, error) {
	reg, err := client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
	if err == nil {
		return reg, nil
	}
	// The key may already be registered — resolve the existing account.
	if existing, rErr := client.Registration.ResolveAccountByKey(); rErr == nil {
		return existing, nil
	}
	return nil, fmt.Errorf("acme register: %w", err)
}

// acmeUser loads or creates a persisted ACME account (separate per environment).
func (m *Manager) acmeUser() (*leUser, error) {
	env := "production"
	if m.Staging {
		env = "staging"
	}
	dir := filepath.Join(m.BaseDir, "account", env)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create account dir: %w", err)
	}
	key, err := loadOrCreateAccountKey(filepath.Join(dir, "account.key"))
	if err != nil {
		return nil, err
	}
	return &leUser{email: m.Email, key: key}, nil
}

// loadOrCreateAccountKey returns a persisted ECDSA account key, creating it once.
func loadOrCreateAccountKey(path string) (crypto.PrivateKey, error) {
	if raw, err := os.ReadFile(path); err == nil {
		block, _ := pem.Decode(raw)
		if block != nil {
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
