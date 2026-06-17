package signing

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
)

func TestSignVerifyRoundTrip(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	digest := []byte("0123456789abcdef0123456789abcdef") // 32-byte sha256 stand-in
	sig := Sign(priv, digest)
	if !Verify(pub, digest, sig) {
		t.Fatal("valid signature must verify")
	}
}

func TestVerifyRejectsTamperedDigest(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	sig := Sign(priv, []byte("0123456789abcdef0123456789abcdef"))
	if Verify(pub, []byte("XXX3456789abcdef0123456789abcdef"), sig) {
		t.Fatal("tampered digest must NOT verify")
	}
}

func TestVerifyRejectsWrongKey(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	otherPub, _, _ := ed25519.GenerateKey(rand.Reader)
	digest := []byte("0123456789abcdef0123456789abcdef")
	if Verify(otherPub, digest, Sign(priv, digest)) {
		t.Fatal("wrong key must NOT verify")
	}
}

func TestVerifyRejectsGarbageSignature(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	if Verify(pub, []byte("0123456789abcdef0123456789abcdef"), "not-base64!!") {
		t.Fatal("garbage signature must NOT verify")
	}
}

func TestVerifyAnyTrusted(t *testing.T) {
	pub1, priv1, _ := ed25519.GenerateKey(rand.Reader)
	pub2, _, _ := ed25519.GenerateKey(rand.Reader)
	digest := []byte("0123456789abcdef0123456789abcdef")
	sig := Sign(priv1, digest)
	if !VerifyAny([]ed25519.PublicKey{pub2, pub1}, digest, sig) {
		t.Fatal("must verify against any trusted key (rotation)")
	}
	if VerifyAny([]ed25519.PublicKey{pub2}, digest, sig) {
		t.Fatal("must fail when no trusted key matches")
	}
}
