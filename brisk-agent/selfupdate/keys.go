// Package selfupdate lets the agent replace its own binary with a newer, signed release
// pulled from the control plane: download → verify signature → swap → restart → self-check →
// auto-rollback on failure. The ed25519 signature (not the network channel) is the trust
// anchor — the agent runs a binary only if it is signed by a key listed here.
package selfupdate

import (
	"crypto/ed25519"
	"encoding/base64"
)

// trustedKeysB64 holds the base64 PUBLIC halves of YOUR signing keys.
//   - k1 = primary, k2 = rotation spare.
//   - Replace the placeholders with real values from `briskctl keygen` at Phase 8.
//
// Until replaced, the placeholders are not valid base64, so trustedKeys() returns an empty
// set and EVERY self-update is refused (fail closed) — the feature is inert by default.
var trustedKeysB64 = []string{
	"qBDcMheQ5P5nTrYMYWkEJNftwX45kfL8d5z0qYdkZbg=", // k1 — primary (briskctl keygen, Phase 8 2026-06-17)
	"REPLACE_WITH_PUBLIC_KEY_K2_BASE64",            // k2 — rotation spare (unset → fail closed)
}

// trustedKeys parses the configured public keys, skipping any that aren't valid 32-byte
// ed25519 keys (so a placeholder simply yields fewer/zero trusted keys → fail closed).
func trustedKeys() []ed25519.PublicKey {
	var out []ed25519.PublicKey
	for _, s := range trustedKeysB64 {
		if raw, err := base64.StdEncoding.DecodeString(s); err == nil && len(raw) == ed25519.PublicKeySize {
			out = append(out, ed25519.PublicKey(raw))
		}
	}
	return out
}
