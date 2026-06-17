// Managed certificate path — the CONTROL PLANE issues the cert (lego Bunny
// DNS-01) and ships the PEM material to this edge over the config-pull channel
// (Phase 3.7 Step 2, Part 3). The agent does NOT run ACME for these zones; it
// just writes the supplied cert/key to the standard per-zone paths so the
// (mode-agnostic) Nginx template picks them up, then the caller reloads.
package tls

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
)

// WriteManaged installs a control-plane-supplied cert for domain. It validates
// the material BEFORE touching disk (parses, and the leaf must cover domain) so a
// bad shipment can never replace a working cert — on any error it returns the
// error and leaves the existing files untouched (additive-then-switch safety).
//
// It returns changed=true only when the on-disk cert actually differs, so the
// caller can skip a needless reload on every poll. Writes are atomic (temp+rename).
func (m *Manager) WriteManaged(domain, certPEM, keyPEM string) (changed bool, err error) {
	if certPEM == "" || keyPEM == "" {
		return false, fmt.Errorf("managed tls for %s: empty cert or key", domain)
	}
	leaf := parseLeafPEM([]byte(certPEM))
	if leaf == nil {
		return false, fmt.Errorf("managed tls for %s: cert does not parse", domain)
	}
	if err := leaf.VerifyHostname(domain); err != nil {
		return false, fmt.Errorf("managed tls for %s: cert does not cover host: %w", domain, err)
	}

	p := m.Paths(domain)
	if err := os.MkdirAll(p.Dir, 0o755); err != nil {
		return false, fmt.Errorf("create tls dir %q: %w", p.Dir, err)
	}

	// Skip if identical to what's already installed (idempotent; avoids reload).
	if cur, err := os.ReadFile(p.FullChain); err == nil && string(cur) == certPEM {
		if curKey, err := os.ReadFile(p.PrivKey); err == nil && string(curKey) == keyPEM {
			return false, nil
		}
	}

	if err := writeFileAtomic(p.FullChain, []byte(certPEM), 0o644); err != nil {
		return false, fmt.Errorf("write %q: %w", p.FullChain, err)
	}
	if err := writeFileAtomic(p.PrivKey, []byte(keyPEM), 0o600); err != nil {
		return false, fmt.Errorf("write %q: %w", p.PrivKey, err)
	}
	return true, nil
}

// parseLeafPEM parses the first (leaf) certificate from a PEM bundle, or nil.
func parseLeafPEM(pemBytes []byte) *x509.Certificate {
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

// writeFileAtomic writes data to a temp file in the same dir, then renames over
// path — so a reader (Nginx) never sees a half-written cert.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op if the rename succeeded
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
