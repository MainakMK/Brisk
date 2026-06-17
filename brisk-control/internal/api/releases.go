package api

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"brisk-control/internal/signing"
	"brisk-control/internal/store"
)

// trustedPubKeys parses the configured base64 ed25519 public keys (BRISK_AGENT_PUBKEYS).
// These MUST match the keys compiled into the agent. Empty => uploads fail closed.
func (a *API) trustedPubKeys() []ed25519.PublicKey {
	var out []ed25519.PublicKey
	for _, s := range a.cfg.AgentPubKeys {
		if raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s)); err == nil && len(raw) == ed25519.PublicKeySize {
			out = append(out, ed25519.PublicKey(raw))
		}
	}
	return out
}

type uploadReleaseInput struct {
	Version   string `json:"version"`
	SHA256    string `json:"sha256"`
	Signature string `json:"signature"`
	SignedBy  string `json:"signed_by"`
	Notes     string `json:"notes"`
	BinaryB64 string `json:"binary_b64"`
}

// uploadRelease stores a new signed agent release. The server recomputes the sha256 and
// verifies the ed25519 signature against a trusted public key BEFORE storing — the upload
// channel is authenticated, but the signature is the real trust anchor.
func (a *API) uploadRelease(w http.ResponseWriter, r *http.Request) {
	var in uploadReleaseInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<20)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "bad json")
		return
	}
	if in.Version == "" {
		writeError(w, http.StatusBadRequest, "version required")
		return
	}
	bin, err := base64.StdEncoding.DecodeString(in.BinaryB64)
	if err != nil || len(bin) == 0 {
		writeError(w, http.StatusBadRequest, "bad binary_b64")
		return
	}
	sum := sha256.Sum256(bin)
	if fmt.Sprintf("%x", sum) != in.SHA256 {
		writeError(w, http.StatusBadRequest, "sha256 mismatch")
		return
	}
	trusted := a.trustedPubKeys()
	if len(trusted) == 0 {
		writeError(w, http.StatusServiceUnavailable, "no trusted signing keys configured (BRISK_AGENT_PUBKEYS)")
		return
	}
	if !signing.VerifyAny(trusted, sum[:], in.Signature) {
		writeError(w, http.StatusBadRequest, "signature not trusted")
		return
	}
	signedBy := in.SignedBy
	if signedBy == "" {
		signedBy = "k1"
	}
	rel := store.Release{Version: in.Version, SHA256: in.SHA256, Signature: in.Signature, SignedBy: signedBy, Notes: in.Notes}
	if err := a.store.InsertRelease(r.Context(), rel, bin); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	_ = a.store.WriteAudit(r.Context(), a.actorID(r), "release.upload", in.Version, fmt.Sprintf("%d bytes, signed_by=%s", len(bin), signedBy))
	slog.Info("agent release uploaded", "version", in.Version, "size_bytes", len(bin), "signed_by", signedBy)
	writeJSON(w, http.StatusCreated, map[string]any{"version": in.Version, "size_bytes": len(bin), "signed_by": signedBy})
}

// listReleases returns release metadata (no binary), newest first.
func (a *API) listReleases(w http.ResponseWriter, r *http.Request) {
	rs, err := a.store.ListReleases(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rs)
}

// getReleaseBinary streams the signed binary for one version (agent download path).
// http.ServeContent gives Range/resume support; the ETag is the sha256.
func (a *API) getReleaseBinary(w http.ResponseWriter, r *http.Request) {
	bin, sha, err := a.store.GetReleaseBinary(r.Context(), chi.URLParam(r, "version"))
	if err != nil {
		writeError(w, http.StatusNotFound, "no such release")
		return
	}
	w.Header().Set("ETag", `"`+sha+`"`)
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeContent(w, r, "brisk-agent", time.Time{}, bytes.NewReader(bin))
}

// deleteRelease removes a release (admin). The currently-running-version guard + retention
// pruning land in Phase 7; for now this is a plain admin delete.
func (a *API) deleteRelease(w http.ResponseWriter, r *http.Request) {
	version := chi.URLParam(r, "version")
	if err := a.store.DeleteRelease(r.Context(), version); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": version})
}
