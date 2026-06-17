// Package signing wraps ed25519 detached signatures over a release's sha256 digest.
//
// The PRIVATE key lives only in CI (a GitHub Actions secret) or a local key file used for
// manual `briskctl` signing — never in the repo, on a laptop, or in chat. Agents verify with
// PUBLIC keys compiled into the binary (see brisk-agent/selfupdate). The signature — not the
// network channel — is the real trust anchor: an edge runs a binary only if it is signed by a
// key the agent trusts, so even a compromised control plane cannot push a malicious agent.
package signing

import (
	"crypto/ed25519"
	"encoding/base64"
)

// Sign returns a base64 (std) detached signature of digest. digest is the 32-byte sha256 of the
// agent binary; signing the digest (not the whole binary) keeps signatures tiny and constant-size.
func Sign(priv ed25519.PrivateKey, digest []byte) string {
	return base64.StdEncoding.EncodeToString(ed25519.Sign(priv, digest))
}

// Verify checks a base64 signature of digest against a single public key.
func Verify(pub ed25519.PublicKey, digest []byte, sigB64 string) bool {
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return false
	}
	return ed25519.Verify(pub, digest, sig)
}

// VerifyAny returns true if the signature verifies against ANY trusted key. Carrying two trusted
// keys lets you rotate: sign with the spare (which the fleet already trusts) and retire the old one
// without a fleet redeploy just to start trusting the replacement.
func VerifyAny(trusted []ed25519.PublicKey, digest []byte, sigB64 string) bool {
	for _, k := range trusted {
		if Verify(k, digest, sigB64) {
			return true
		}
	}
	return false
}
