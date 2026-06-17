package selfupdate

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestVerifyBinary(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	orig := trustedKeysB64
	trustedKeysB64 = []string{base64.StdEncoding.EncodeToString(pub)} // inject a real test key
	defer func() { trustedKeysB64 = orig }()

	data := []byte("fake-agent-binary-bytes")
	sum := sha256.Sum256(data)
	shaHex := hexLower(sum[:])
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, sum[:]))

	if !VerifyBinary(data, shaHex, sig) {
		t.Fatal("valid signed binary must verify")
	}
	if VerifyBinary([]byte("tampered"), shaHex, sig) {
		t.Fatal("tampered bytes must NOT verify")
	}
	_, otherPriv, _ := ed25519.GenerateKey(rand.Reader)
	badSig := base64.StdEncoding.EncodeToString(ed25519.Sign(otherPriv, sum[:]))
	if VerifyBinary(data, shaHex, badSig) {
		t.Fatal("wrong-key signature must NOT verify")
	}
	if VerifyBinary(data, shaHex, "not-base64!!") {
		t.Fatal("garbage signature must NOT verify")
	}
}

func TestVerifyFailsClosedWithNoKeys(t *testing.T) {
	orig := trustedKeysB64
	trustedKeysB64 = []string{"REPLACE_WITH_PUBLIC_KEY_K1_BASE64"} // invalid → zero trusted keys
	defer func() { trustedKeysB64 = orig }()
	data := []byte("x")
	sum := sha256.Sum256(data)
	if VerifyBinary(data, hexLower(sum[:]), base64.StdEncoding.EncodeToString(make([]byte, 64))) {
		t.Fatal("with no trusted keys, nothing must verify (fail closed)")
	}
}

func tmpPaths(t *testing.T) Paths {
	d := t.TempDir()
	b := filepath.Join(d, "agent")
	if err := os.WriteFile(b, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	return Paths{Binary: b, Prev: b + ".prev", New: b + ".new", Marker: filepath.Join(d, "marker")}
}

func TestApplySwapsAndKeepsPrev(t *testing.T) {
	p := tmpPaths(t)
	exit, err := Apply(p, []byte("NEW"), "0.4.0")
	if err != nil || !exit {
		t.Fatalf("Apply: exit=%v err=%v", exit, err)
	}
	if b, _ := os.ReadFile(p.Binary); string(b) != "NEW" {
		t.Fatal("binary not swapped to NEW")
	}
	if b, _ := os.ReadFile(p.Prev); string(b) != "OLD" {
		t.Fatal(".prev did not keep OLD")
	}
	if _, err := os.Stat(p.Marker); err != nil {
		t.Fatal("marker missing after Apply")
	}
}

func TestSelfCheckCommitsOnHealthy(t *testing.T) {
	p := tmpPaths(t)
	Apply(p, []byte("NEW"), "0.4.0")
	if SelfCheckOnStart(p, 3, func() error { return nil }) {
		t.Fatal("healthy new binary must NOT roll back")
	}
	if _, err := os.Stat(p.Marker); err == nil {
		t.Fatal("marker must be cleared on commit")
	}
}

func TestSelfCheckRollsBackAfterMaxRestarts(t *testing.T) {
	p := tmpPaths(t)
	Apply(p, []byte("NEW"), "0.4.0")
	bad := func() error { return errors.New("unhealthy") }
	if SelfCheckOnStart(p, 3, bad) { // n 0→1
		t.Fatal("should not roll back yet (1/3)")
	}
	if SelfCheckOnStart(p, 3, bad) { // n 1→2
		t.Fatal("should not roll back yet (2/3)")
	}
	if !SelfCheckOnStart(p, 3, bad) { // n 2→3 → rollback
		t.Fatal("must roll back at maxRestarts")
	}
	if b, _ := os.ReadFile(p.Binary); string(b) != "OLD" {
		t.Fatal("binary not restored to OLD after rollback")
	}
}
