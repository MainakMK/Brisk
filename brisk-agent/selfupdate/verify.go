package selfupdate

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
)

// VerifyBinary is the ONLY gate before swapping in a new binary. It returns true only if:
//   - sha256(data) (hex) equals wantSHAHex (integrity), AND
//   - the base64 signature verifies against ANY trusted key (authenticity).
//
// Anything else — tampered bytes, wrong/garbage signature, no trusted key — returns false,
// so the agent refuses to install it.
func VerifyBinary(data []byte, wantSHAHex, sigB64 string) bool {
	sum := sha256.Sum256(data)
	if hexLower(sum[:]) != wantSHAHex {
		return false
	}
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		return false
	}
	for _, k := range trustedKeys() {
		if ed25519.Verify(k, sum[:], sig) {
			return true
		}
	}
	return false
}

func hexLower(b []byte) string {
	const h = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, c := range b {
		out[i*2] = h[c>>4]
		out[i*2+1] = h[c&0xf]
	}
	return string(out)
}
