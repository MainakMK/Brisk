// Package adminauth provides HUMAN authentication primitives (Phase 3.7 Step 3):
// argon2id password hashing, opaque session/CSRF/admin-token secrets, and a login
// rate limiter. It is deliberately separate from internal/auth (agent tokens):
// human passwords are low-entropy and need a slow KDF, agent tokens are 256-bit
// random and use a fast hash. Two threat models, two mechanisms.
package adminauth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// argon2id parameters (OWASP-aligned: 64 MiB, t=2, p=2). Encoded into the hash
// string so existing hashes still verify if these are tuned up later.
type argonParams struct {
	memory  uint32
	time    uint32
	threads uint8
	keyLen  uint32
	saltLen uint32
}

var defaultArgon = argonParams{memory: 64 * 1024, time: 2, threads: 2, keyLen: 32, saltLen: 16}

// HashPassword returns a PHC-formatted argon2id hash of pw (self-describing:
// "$argon2id$v=19$m=65536,t=2,p=2$<salt>$<hash>").
func HashPassword(pw string) (string, error) {
	salt := make([]byte, defaultArgon.saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	p := defaultArgon
	sum := argon2.IDKey([]byte(pw), salt, p.time, p.memory, p.threads, p.keyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.memory, p.time, p.threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(sum)), nil
}

// VerifyPassword reports whether pw matches the encoded argon2id hash, in constant
// time. Returns false on any malformed input (never panics).
func VerifyPassword(pw, encoded string) bool {
	parts := strings.Split(encoded, "$")
	// ["", "argon2id", "v=19", "m=..,t=..,p=..", <salt>, <hash>]
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false
	}
	var mem, t uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &mem, &t, &threads); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false
	}
	got := argon2.IDKey([]byte(pw), salt, t, mem, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}
