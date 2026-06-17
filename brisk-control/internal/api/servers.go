package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"

	"brisk-control/internal/provision"
	"brisk-control/internal/store"
	"brisk-control/internal/token"
)

type createServerInput struct {
	Name          string  `json:"name" validate:"required"`
	Region        string  `json:"region" validate:"required"`
	IP            string  `json:"ip" validate:"required,ip"`
	EdgeID        string  `json:"edge_id"` // optional; auto-generated if empty
	Hostname         *string `json:"hostname"`
	CapacityMbps     *int32  `json:"capacity_mbps"`
	WeightByCapacity bool    `json:"weight_by_capacity"` // opt-in: derive DNS weight from capacity
	SSHUser          string  `json:"ssh_user" validate:"required"`
	SSHPort       int     `json:"ssh_port"`
	SSHPassword   string  `json:"ssh_password"`
	SSHPrivateKey string  `json:"ssh_private_key"`
}

func (a *API) listServers(w http.ResponseWriter, r *http.Request) {
	servers, err := a.store.ListServers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, servers)
}

// createServer creates a server, issues its agent token (shown ONCE), and
// provisions it over SSH.
func (a *API) createServer(w http.ResponseWriter, r *http.Request) {
	var in createServerInput
	if !decode(w, r, &in) {
		return
	}
	if in.SSHPassword == "" && in.SSHPrivateKey == "" {
		writeError(w, http.StatusBadRequest, "ssh_password or ssh_private_key is required")
		return
	}
	edgeID := in.EdgeID
	if edgeID == "" {
		edgeID = generateEdgeID(in.Region)
	}

	srv, err := a.store.CreateServer(r.Context(), store.CreateServerParams{
		Name: in.Name, Region: in.Region, IP: in.IP, Hostname: in.Hostname,
		EdgeID: edgeID, CapacityMbps: in.CapacityMbps, WeightByCapacity: in.WeightByCapacity,
		SSHUser: &in.SSHUser, SSHPort: int32(in.SSHPort),
	})
	if err != nil {
		if store.IsUniqueViolation(err) {
			writeError(w, http.StatusConflict, "edge_id already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	tok, err := a.issueToken(r.Context(), srv.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	creds := provision.SSHCreds{
		User: in.SSHUser, Port: in.SSHPort,
		Password: in.SSHPassword, PrivateKey: in.SSHPrivateKey,
	}
	// Provision in the background (SSH + bootstrap takes minutes; the request
	// must not block). Status flips: pending -> provisioning -> online (heartbeat).
	_ = a.store.UpdateServerStatus(r.Context(), srv.ID, "provisioning")
	go a.runProvision(srv, creds, tok)

	writeJSON(w, http.StatusCreated, map[string]any{
		"server":      a.mustGet(r.Context(), srv.ID),
		"agent_token": tok, // shown ONCE
		"status":      "provisioning",
		"note":        "provisioning over SSH; poll /provision-log and the server status",
	})
}

func (a *API) getServer(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	s, err := a.store.GetServer(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s)
}

func (a *API) deleteServer(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	// Capture the edge_id before deletion so we can pull its DNS record.
	srv, _ := a.store.GetServer(r.Context(), id)
	if err := a.store.DeleteServer(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "server not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Lifecycle delete: removing the server is an explicit, authorized action,
	// so its routing record is removed too — bypassing the deletion lock (which
	// only guards ad-hoc DNS record deletes). Best-effort + a reconcile to catch
	// any other drift.
	a.removeServerDNS(r.Context(), srv.EdgeID, "server_delete")
	a.triggerDNSReconcile("server_delete")
	w.WriteHeader(http.StatusNoContent)
}

// reprovision re-runs provisioning over the installed control-plane key (no
// password needed). It issues a fresh token in the process.
func (a *API) reprovision(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	srv, err := a.store.GetServer(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	tok, err := a.issueToken(r.Context(), srv.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	creds := provision.SSHCreds{User: deref(srv.SSHUser, "root"), Port: int(srv.SSHPort)}
	_ = a.store.UpdateServerStatus(r.Context(), srv.ID, "provisioning")
	go a.runProvision(srv, creds, tok)
	writeJSON(w, http.StatusAccepted, map[string]any{"agent_token": tok, "status": "provisioning"})
}

// rotateToken revokes existing tokens, issues a new one, and pushes it to the
// agent (key auth, restart) without a full re-bootstrap.
func (a *API) rotateToken(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	srv, err := a.store.GetServer(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Revoke old, mint new (old tokens now fail auth immediately).
	if err := a.store.RevokeServerTokens(r.Context(), srv.ID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	tok, err := token.Generate()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := a.store.CreateAgentToken(r.Context(), srv.ID, token.Prefix(tok), token.Hash(tok)); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Old token already fails auth (revoked). Push the new token to the agent in
	// the background (SSH restart) so it keeps checking in.
	go func() {
		if err := a.prov.UpdateToken(context.Background(), srv, tok); err != nil {
			_ = a.store.AddProvisionLog(context.Background(), srv.ID, "error", "token push failed: "+err.Error())
		}
	}()
	writeJSON(w, http.StatusOK, map[string]any{"agent_token": tok})
}

func (a *API) provisionLog(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	logs, err := a.store.ListProvisionLogs(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, logs)
}

// issueToken revokes any existing tokens for the server and mints a new one,
// returning the plaintext token (shown to the caller exactly once).
func (a *API) issueToken(ctx context.Context, serverID int64) (string, error) {
	if err := a.store.RevokeServerTokens(ctx, serverID); err != nil {
		return "", err
	}
	tok, err := token.Generate()
	if err != nil {
		return "", err
	}
	if err := a.store.CreateAgentToken(ctx, serverID, token.Prefix(tok), token.Hash(tok)); err != nil {
		return "", err
	}
	return tok, nil
}

// runProvision performs the SSH provisioning flow in the background (its own
// context, independent of the HTTP request). Progress lands in provision_logs;
// the server flips to online when the agent heartbeats.
func (a *API) runProvision(srv store.Server, creds provision.SSHCreds, tok string) {
	ctx := context.Background()
	if err := a.prov.Provision(ctx, srv, creds, tok); err != nil {
		_ = a.store.UpdateServerStatus(ctx, srv.ID, "offline")
	}
}

// --- helpers ---

func (a *API) mustGet(ctx context.Context, id int64) store.Server {
	s, _ := a.store.GetServer(ctx, id)
	return s
}

func deref(p *string, def string) string {
	if p != nil && *p != "" {
		return *p
	}
	return def
}

// generateEdgeID builds a unique-ish, Brisk-branded edge id from the region, e.g.
// "Brisk-IN-DEL-3f9a". The "Brisk-" prefix keeps the fallback on-brand (the id shows in
// the X-Brisk-Edge response header) so an operator who omits a custom id — or an API
// caller — never gets a raw, unbranded name. Operators can still set their own id
// (e.g. "Brisk-FRA-01") via the dashboard/API; this only fires when it's left blank.
func generateEdgeID(region string) string {
	r := strings.ToUpper(strings.TrimSpace(region))
	r = strings.Map(func(c rune) rune {
		if (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' {
			return c
		}
		return '-'
	}, r)
	if r == "" {
		r = "EDGE"
	}
	b := make([]byte, 2)
	_, _ = rand.Read(b)
	return "Brisk-" + r + "-" + hex.EncodeToString(b)
}
