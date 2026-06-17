package adminauth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
)

// adminTokenScheme prefixes admin API tokens so they're visually + programmatically
// distinct from agent tokens ("brisk_"). The human authenticator only accepts
// tokens with this scheme as bearer admin tokens.
const adminTokenScheme = "brisk_admin_"

// AdminPrefixLen is the indexed-lookup prefix length: scheme + 8 chars.
const AdminPrefixLen = len(adminTokenScheme) + 8

// randB64 returns n random bytes as URL-safe base64 (no padding).
func randB64(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// NewSessionID returns an opaque 256-bit session id. The plaintext goes in the
// cookie; only Hash(id) is stored server-side.
func NewSessionID() (string, error) { return randB64(32) }

// NewCSRFToken returns an opaque 256-bit CSRF token (double-submit, bound to the session).
func NewCSRFToken() (string, error) { return randB64(32) }

// Hash is the at-rest hash for session ids / CSRF tokens / admin API tokens. These
// are all 256-bit random (high entropy), so a fast SHA-256 is appropriate (cf.
// argon2 for low-entropy passwords).
func Hash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// NewAdminToken returns a new opaque admin API token "brisk_admin_<base64>".
func NewAdminToken() (string, error) {
	s, err := randB64(32)
	if err != nil {
		return "", err
	}
	return adminTokenScheme + s, nil
}

// AdminPrefix returns the indexed lookup prefix of an admin token.
func AdminPrefix(tok string) string {
	if len(tok) < AdminPrefixLen {
		return tok
	}
	return tok[:AdminPrefixLen]
}

// IsAdminToken reports whether tok carries the admin scheme (so the human
// authenticator can tell admin tokens from agent tokens without a DB hit).
func IsAdminToken(tok string) bool { return strings.HasPrefix(tok, adminTokenScheme) }
