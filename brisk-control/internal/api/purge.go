package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"brisk-control/internal/auth"
	"brisk-control/internal/purge"
	"brisk-control/internal/store"
)

// purgeInput is the body for POST /zones/{id}/purge.
type purgeInput struct {
	Type   string `json:"type" validate:"required,oneof=url prefix zone"`
	Target string `json:"target"`
}

// purgeAllInput optionally restricts purge-all to specific servers (empty = all).
type purgeAllInput struct {
	ServerIDs []int64 `json:"server_ids"`
}

// purgeAckInput is the agent's completion report.
type purgeAckInput struct {
	JobID int64 `json:"job_id" validate:"required"`
}

// purgeZone records a purge job and publishes it over NATS to every edge serving
// the zone. Returns 202 Accepted with the job (the purge is applied async at the
// edge, in milliseconds). Sliced video is handled at the edge via wildcard purge.
func (a *API) purgeZone(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	var in purgeInput
	if !decode(w, r, &in) {
		return
	}
	if a.pub == nil {
		writeError(w, http.StatusServiceUnavailable, "purge channel unavailable (NATS not configured)")
		return
	}

	// Tenant scoping (admin = any zone; customer = its own only).
	z, ok := a.scopeZone(w, r, id)
	if !ok {
		return
	}

	// Normalize the target: accept a bare absolute path OR a full http(s):// URL
	// (the scheme + host are stripped to the path — see normalizePurgeTarget).
	target, terr := normalizePurgeTarget(in.Target, in.Type)
	if terr != nil {
		writeError(w, http.StatusBadRequest, terr.Error())
		return
	}

	hosts := zoneHosts(z)
	servers, err := a.store.ServersForZone(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	job, err := a.store.CreatePurgeJob(r.Context(), &id, in.Type, target, len(servers))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	msg := purge.Message{Type: in.Type, Hosts: hosts, Target: target, JobID: job.ID, ZoneID: id}
	a.publishToServers(r.Context(), servers, msg)

	writeJSON(w, http.StatusAccepted, job)
}

// purgeAll purges the entire cache (purge_all) on selected servers, or all
// servers when none are specified.
func (a *API) purgeAll(w http.ResponseWriter, r *http.Request) {
	var in purgeAllInput
	// body is optional; tolerate an empty body.
	if r.ContentLength != 0 {
		if !decode(w, r, &in) {
			return
		}
	}
	if a.pub == nil {
		writeError(w, http.StatusServiceUnavailable, "purge channel unavailable (NATS not configured)")
		return
	}

	all, err := a.store.ListServers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	servers := all
	if len(in.ServerIDs) > 0 {
		want := map[int64]bool{}
		for _, sid := range in.ServerIDs {
			want[sid] = true
		}
		servers = servers[:0]
		for _, s := range all {
			if want[s.ID] {
				servers = append(servers, s)
			}
		}
	}

	job, err := a.store.CreatePurgeJob(r.Context(), nil, "all", "*", len(servers))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	msg := purge.Message{Type: "all", JobID: job.ID}
	a.publishToServers(r.Context(), servers, msg)

	writeJSON(w, http.StatusAccepted, job)
}

// publishToServers fans a purge message out to each server's edge subject,
// best-effort (a publish error for one edge doesn't block the others).
func (a *API) publishToServers(ctx context.Context, servers []store.Server, msg purge.Message) {
	for _, s := range servers {
		if err := a.pub.Publish(ctx, s.EdgeID, msg); err != nil {
			_ = a.store.AddProvisionLog(ctx, s.ID, "warn", "purge publish failed: "+err.Error())
		}
	}
}

// listPurgeJobs returns recent purge jobs, optionally filtered by zone_id.
func (a *API) listPurgeJobs(w http.ResponseWriter, r *http.Request) {
	var zoneID *int64
	if zs := r.URL.Query().Get("zone_id"); zs != "" {
		z, err := strconv.ParseInt(zs, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid zone_id")
			return
		}
		zoneID = &z
	}
	limit := 0
	if ls := r.URL.Query().Get("limit"); ls != "" {
		limit, _ = strconv.Atoi(ls)
	}
	jobs, err := a.store.ListPurgeJobs(r.Context(), zoneID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, jobs)
}

// purgeAck is called by an authenticated agent after it applies a purge, so the
// control plane can advance the job's completion count.
func (a *API) purgeAck(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.ServerIDFromContext(r.Context()); !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var in purgeAckInput
	if !decode(w, r, &in) {
		return
	}
	job, err := a.store.MarkPurgeJobEdgeDone(r.Context(), in.JobID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "purge job not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, job)
}

// normalizePurgeTarget reduces a purge target to the edge cache-key PATH. It accepts
// either a bare absolute path ("/a/b?v=2") OR a full http(s):// URL, from which it
// strips the scheme + host and keeps only the path (+ query, since the cache key may
// include it). The host a user pastes is usually the ORIGIN (e.g.
// https://cdn.mainakghosh.com/wp-content/...) and is irrelevant: the edge keys cache
// by the ZONE's own hostname(s) + path, so only the path matters. Returns an error
// when the result still isn't a usable absolute path.
func normalizePurgeTarget(raw, typ string) (string, error) {
	t := strings.TrimSpace(raw)
	if typ == "zone" {
		return "/", nil // whole zone
	}
	if u, err := url.Parse(t); err == nil &&
		(strings.EqualFold(u.Scheme, "http") || strings.EqualFold(u.Scheme, "https")) && u.Host != "" {
		p := u.EscapedPath()
		if p == "" {
			p = "/"
		}
		if u.RawQuery != "" {
			p += "?" + u.RawQuery
		}
		t = p
	}
	if t == "" || !strings.HasPrefix(t, "/") {
		return "", fmt.Errorf(`target must be a path starting with "/" or a full http(s):// URL`)
	}
	return t, nil
}

// zoneHosts returns the hostnames a zone's content may be cached under (cdn
// hostname + custom domain), so a purge clears every $host variant.
func zoneHosts(z store.Zone) []string {
	hosts := []string{z.CDNHostname}
	if z.CustomDomain != nil && *z.CustomDomain != "" && *z.CustomDomain != z.CDNHostname {
		hosts = append(hosts, *z.CustomDomain)
	}
	return hosts
}
