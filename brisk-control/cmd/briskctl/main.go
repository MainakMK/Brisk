// Command briskctl is the operator CLI for the Brisk agent rollout.
//
// It talks ONLY to the control-plane API (via BRISK_CP_URL + BRISK_ADMIN_TOKEN) — it never
// SSHes into an edge. Subcommands:
//
//	briskctl keygen
//	    Print a fresh ed25519 keypair. PRIVATE → GitHub Actions secret BRISK_AGENT_SIGNING_KEY
//	    (and/or a local key file); PUBLIC → brisk-agent/selfupdate/keys.go trustedKeysB64.
//
//	briskctl release push --version vX --bin ./brisk-agent --key ./signing.key [--notes "..."]
//	    sha256 the binary, ed25519-sign the digest with the key file, and upload to the control
//	    plane's release store. The server re-verifies the signature before storing.
//
// (deploy / rollback subcommands are added in Phase 5 once the rollout API exists.)
package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"brisk-control/internal/signing"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: briskctl <keygen|release> ...")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "keygen":
		keygen()
	case "release":
		if len(os.Args) < 3 || os.Args[2] != "push" {
			fmt.Println("usage: briskctl release push --version vX --bin path --key keyfile [--notes ...]")
			os.Exit(2)
		}
		releasePush(os.Args[3:])
	default:
		fmt.Println("unknown command:", os.Args[1])
		os.Exit(2)
	}
}

// keygen prints a fresh ed25519 keypair. You own it; nobody else generates it.
func keygen() {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		fmt.Fprintln(os.Stderr, "keygen:", err)
		os.Exit(1)
	}
	fmt.Println("# PRIVATE KEY — store as GitHub secret BRISK_AGENT_SIGNING_KEY (and/or a local key file). KEEP SECRET.")
	fmt.Println(base64.StdEncoding.EncodeToString(priv))
	fmt.Println()
	fmt.Println("# PUBLIC KEY — paste into brisk-agent/selfupdate/keys.go trustedKeysB64 (k1 or k2).")
	fmt.Println(base64.StdEncoding.EncodeToString(pub))
}

// signDigest is the shared helper for the push path (and mirrors what CI does):
// sha256(binary) → hex digest + base64 ed25519 signature.
func signDigest(binary []byte, privB64 string) (shaHex, sig string, err error) {
	raw, err := base64.StdEncoding.DecodeString(privB64)
	if err != nil {
		return "", "", fmt.Errorf("decode private key: %w", err)
	}
	if len(raw) != ed25519.PrivateKeySize {
		return "", "", fmt.Errorf("private key must be %d bytes, got %d", ed25519.PrivateKeySize, len(raw))
	}
	sum := sha256.Sum256(binary)
	return fmt.Sprintf("%x", sum), signing.Sign(ed25519.PrivateKey(raw), sum[:]), nil
}

func releasePush(args []string) {
	var version, binPath, keyPath, notes string
	for i := 0; i+1 < len(args); i += 2 {
		switch args[i] {
		case "--version":
			version = args[i+1]
		case "--bin":
			binPath = args[i+1]
		case "--key":
			keyPath = args[i+1]
		case "--notes":
			notes = args[i+1]
		}
	}
	if version == "" || binPath == "" || keyPath == "" {
		fmt.Println("usage: briskctl release push --version vX --bin path --key keyfile [--notes ...]")
		os.Exit(2)
	}
	bin, err := os.ReadFile(binPath)
	must(err)
	priv, err := os.ReadFile(keyPath)
	must(err)
	shaHex, sig, err := signDigest(bin, string(bytes.TrimSpace(priv)))
	must(err)

	body, _ := json.Marshal(map[string]any{
		"version":    version,
		"sha256":     shaHex,
		"signature":  sig,
		"signed_by":  "k1",
		"notes":      notes,
		"binary_b64": base64.StdEncoding.EncodeToString(bin),
	})
	cp := os.Getenv("BRISK_CP_URL")
	if cp == "" {
		fmt.Fprintln(os.Stderr, "BRISK_CP_URL not set")
		os.Exit(1)
	}
	req, err := http.NewRequest(http.MethodPost, cp+"/api/v1/releases", bytes.NewReader(body))
	must(err)
	req.Header.Set("Authorization", "Bearer "+os.Getenv("BRISK_ADMIN_TOKEN"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	must(err)
	defer resp.Body.Close()
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	fmt.Printf("upload %s: %d %s\n", version, resp.StatusCode, bytes.TrimSpace(msg))
	if resp.StatusCode >= 300 {
		os.Exit(1)
	}
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
