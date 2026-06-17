// Package tls obtains and installs certificates for each zone.
//
// Step 4 implements three modes (selected per zone via agent.yaml tls / tls_mode):
//   - selfsigned : generate an ECDSA P-256 self-signed cert natively in Go, with
//     the domain in the SAN. Local dev default. (Browsers warn — use -k or mkcert.)
//   - mkcert     : shell out to mkcert for a locally-trusted cert (no warnings).
//   - letsencrypt: obtain/renew a real ECDSA cert via the ACME library (lego).
//     VPS-only — Let's Encrypt must reach the public domain (see letsencrypt.go).
//
// All modes write the same files so the Nginx template is mode-agnostic:
//   /etc/brisk/tls/<domain>/fullchain.pem
//   /etc/brisk/tls/<domain>/privkey.pem
//
// Per CLAUDE.md update (Step 4.3): NO OCSP stapling (no effect with Let's Encrypt,
// meaningless for self-signed) and NO custom dhparam (modern ECDHE-only profile).
package tls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// TLS mode values (kept in sync with the config package).
const (
	ModeSelfSigned  = "selfsigned"
	ModeMkcert      = "mkcert"
	ModeLetsEncrypt = "letsencrypt"

	// DefaultBaseDir is where per-domain cert directories live.
	DefaultBaseDir = "/etc/brisk/tls"

	// DefaultWebrootDir is where the ACME HTTP-01 challenge files are written and
	// served from (Nginx serves /.well-known/acme-challenge/ from here).
	DefaultWebrootDir = "/var/www/brisk-acme"

	// renewMargin: regenerate/renew when the cert expires within this window.
	renewMargin = 30 * 24 * time.Hour
)

// CertPaths is where a zone's certificate and key live on disk.
type CertPaths struct {
	Dir       string
	FullChain string // .../fullchain.pem
	PrivKey   string // .../privkey.pem
}

// Manager provisions certificates under BaseDir.
type Manager struct {
	BaseDir    string // default /etc/brisk/tls
	Email      string // ACME account email (letsencrypt mode)
	Staging    bool   // use the Let's Encrypt staging directory
	WebrootDir string // ACME HTTP-01 webroot (default /var/www/brisk-acme)
}

// NewManager returns a Manager. baseDir/webrootDir default when empty.
func NewManager(baseDir, email string, staging bool, webrootDir string) *Manager {
	if baseDir == "" {
		baseDir = DefaultBaseDir
	}
	if webrootDir == "" {
		webrootDir = DefaultWebrootDir
	}
	return &Manager{BaseDir: baseDir, Email: email, Staging: staging, WebrootDir: webrootDir}
}

// EnsurePlaceholder writes a self-signed cert ONLY if no cert exists yet, so
// Nginx can start (its config references the cert files) before the real
// Let's Encrypt cert is obtained over a now-running port 80.
func (m *Manager) EnsurePlaceholder(domain string) error {
	p := m.Paths(domain)
	if err := os.MkdirAll(p.Dir, 0o755); err != nil {
		return fmt.Errorf("create tls dir %q: %w", p.Dir, err)
	}
	if _, err := os.Stat(p.FullChain); err == nil {
		return nil // some cert (placeholder or real) already present
	}
	return m.ensureSelfSigned(domain, p)
}

// NeedsLetsEncrypt reports whether a real LE cert must be obtained or renewed:
// no cert yet, a self-signed placeholder, or within the renewal margin.
func (m *Manager) NeedsLetsEncrypt(domain string) bool {
	cert := loadLeafCert(m.Paths(domain).FullChain)
	if cert == nil {
		return true
	}
	if time.Now().Add(renewMargin).After(cert.NotAfter) {
		return true
	}
	return isSelfSigned(cert)
}

// Paths returns the on-disk cert paths for a domain (no I/O).
func (m *Manager) Paths(domain string) CertPaths {
	dir := filepath.Join(m.BaseDir, domain)
	return CertPaths{
		Dir:       dir,
		FullChain: filepath.Join(dir, "fullchain.pem"),
		PrivKey:   filepath.Join(dir, "privkey.pem"),
	}
}

// Ensure makes sure a usable certificate exists for domain in the given mode.
// It is idempotent: an existing, non-expiring, matching cert is left untouched.
func (m *Manager) Ensure(domain, mode string) (CertPaths, error) {
	p := m.Paths(domain)
	if err := os.MkdirAll(p.Dir, 0o755); err != nil {
		return p, fmt.Errorf("create tls dir %q: %w", p.Dir, err)
	}
	switch mode {
	case ModeSelfSigned, "":
		return p, m.ensureSelfSigned(domain, p)
	case ModeMkcert:
		return p, m.ensureMkcert(domain, p)
	case ModeLetsEncrypt:
		// Phase 1: ensure SOME cert exists so Nginx can start. The real cert is
		// obtained by ObtainLetsEncrypt once Nginx serves the HTTP-01 webroot.
		return p, m.EnsurePlaceholder(domain)
	default:
		return p, fmt.Errorf("unknown tls mode %q", mode)
	}
}

// --- self-signed -----------------------------------------------------------

func (m *Manager) ensureSelfSigned(domain string, p CertPaths) error {
	if certUsable(p.FullChain, domain) {
		return nil // valid, matching, not near expiry — idempotent skip.
	}

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generate ECDSA key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return fmt.Errorf("serial: %w", err)
	}

	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: domain, Organization: []string{"Brisk"}},
		NotBefore:             now.Add(-1 * time.Hour),
		NotAfter:              now.AddDate(1, 0, 0), // 1 year
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	// SAN: modern clients validate the SAN, not the CN.
	if ip := net.ParseIP(domain); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
	} else {
		tmpl.DNSNames = []string{domain}
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		return fmt.Errorf("create certificate: %w", err)
	}
	return writePair(p, der, priv)
}

// writePair writes the cert (DER) and ECDSA key to fullchain.pem / privkey.pem.
func writePair(p CertPaths, der []byte, priv *ecdsa.PrivateKey) error {
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(p.FullChain, certPEM, 0o644); err != nil {
		return fmt.Errorf("write %q: %w", p.FullChain, err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return fmt.Errorf("marshal key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(p.PrivKey, keyPEM, 0o600); err != nil {
		return fmt.Errorf("write %q: %w", p.PrivKey, err)
	}
	return nil
}

// --- mkcert ----------------------------------------------------------------

func (m *Manager) ensureMkcert(domain string, p CertPaths) error {
	if certUsable(p.FullChain, domain) {
		return nil
	}
	bin, err := exec.LookPath("mkcert")
	if err != nil {
		return fmt.Errorf("tls mode mkcert but mkcert not installed " +
			"(install it, then run: mkcert -install)")
	}
	out, err := exec.Command(bin, "-cert-file", p.FullChain, "-key-file", p.PrivKey, domain).CombinedOutput()
	if err != nil {
		return fmt.Errorf("mkcert: %w\n%s", err, out)
	}
	return nil
}

// --- shared helpers --------------------------------------------------------

// loadLeafCert parses the first (leaf) certificate from a PEM file, or nil.
func loadLeafCert(path string) *x509.Certificate {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil
	}
	return cert
}

// isSelfSigned reports whether a cert is self-issued (our placeholder), i.e. the
// issuer and subject match. We compare DNs rather than verifying the signature:
// CheckSignatureFrom enforces CA constraints (the placeholder is not a CA), so it
// would wrongly reject our own self-signed cert. A real Let's Encrypt leaf has a
// distinct issuer (the LE intermediate), so this cleanly tells them apart.
func isSelfSigned(cert *x509.Certificate) bool {
	return cert.Issuer.String() == cert.Subject.String()
}

// certUsable reports whether the PEM cert at path exists, parses, covers domain
// (SAN), and is not within renewMargin of expiry.
func certUsable(path, domain string) bool {
	cert := loadLeafCert(path)
	if cert == nil {
		return false
	}
	now := time.Now()
	if now.Before(cert.NotBefore) || now.Add(renewMargin).After(cert.NotAfter) {
		return false // expired or renewing soon
	}
	return cert.VerifyHostname(domain) == nil
}
