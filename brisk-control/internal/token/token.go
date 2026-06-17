// Package token generates and verifies agent API tokens.
//
// API tokens are HIGH-entropy (256-bit random), so they are hashed with a FAST
// hash (SHA-256), not bcrypt/argon2: brute-forcing a 256-bit secret is infeasible
// regardless of hash speed, and SHA-256 keeps per-request verification cheap.
// (bcrypt/argon2's deliberate slowness is for low-entropy human passwords.)
//
// Storage model: keep an indexed PREFIX for fast lookup + the SHA-256 HASH of the
// full token. The plaintext token is shown once at creation and never stored or logged.
package token

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
)

// PrefixLen is the number of leading characters stored for indexed lookup.
// "brisk_" (6) + 8 base64url chars = 14.
const PrefixLen = 14

// Generate returns a new token "brisk_<base64url(32 random bytes)>".
func Generate() (string, error) {
	raw := make([]byte, 32) // 256 bits
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "brisk_" + base64.RawURLEncoding.EncodeToString(raw), nil
}

// Prefix returns the indexed lookup prefix of a token.
func Prefix(tok string) string {
	if len(tok) < PrefixLen {
		return tok
	}
	return tok[:PrefixLen]
}

// Hash returns the hex-encoded SHA-256 of the token.
func Hash(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

// Verify reports whether tok matches the stored hex SHA-256 hash, using a
// constant-time comparison to avoid timing attacks.
func Verify(tok, storedHash string) bool {
	return subtle.ConstantTimeCompare([]byte(Hash(tok)), []byte(storedHash)) == 1
}
