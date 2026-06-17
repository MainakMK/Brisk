package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"brisk-control/internal/auth"
	"brisk-control/internal/identity"
	"brisk-control/internal/store"
)

// actorID returns the calling admin's account id for the audit trail ("" if somehow absent).
func (a *API) actorID(r *http.Request) string {
	id, _ := identity.FromContext(r.Context())
	if id.AccountID == 0 {
		return ""
	}
	return strconv.FormatInt(id.AccountID, 10)
}

// listAudit returns the recent deploy/pause/rollback/upload audit trail (newest first).
func (a *API) listAudit(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	entries, err := a.store.ListAudit(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if entries == nil {
		entries = []store.AuditEntry{}
	}
	writeJSON(w, http.StatusOK, entries)
}

// --- agent-facing: what version should THIS edge run right now? ---

type agentReleaseResp struct {
	TargetVersion string `json:"target_version"`
	URL           string `json:"url,omitempty"`
	SHA256        string `json:"sha256,omitempty"`
	Signature     string `json:"signature,omitempty"`
	SignedBy      string `json:"signed_by,omitempty"`
}

// agentRelease tells the calling edge which version it should be on. It returns the new version
// (with the signed download details) ONLY when this edge's wave is open in the active rollout;
// otherwise it returns the edge's current version (a no-op). The agent verifies the signature
// itself before swapping — the control plane just points at the release.
func (a *API) agentRelease(w http.ResponseWriter, r *http.Request) {
	serverID, ok := auth.ServerIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	srv, err := a.store.GetServer(r.Context(), serverID)
	if err != nil {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	current := srv.AgentVersion

	rollout, active, err := a.store.ActiveRollout(r.Context())
	if err != nil || !active || rollout.Status != "running" {
		writeJSON(w, http.StatusOK, agentReleaseResp{TargetVersion: current})
		return
	}
	targets, err := a.store.ListTargets(r.Context(), rollout.ID)
	if err != nil {
		writeJSON(w, http.StatusOK, agentReleaseResp{TargetVersion: current})
		return
	}
	for _, t := range targets {
		if t.EdgeID == srv.EdgeID && (t.State == "in_progress" || t.State == "soaking") {
			rel, err := a.store.GetReleaseMeta(r.Context(), t.ToVersion)
			if err != nil {
				break
			}
			writeJSON(w, http.StatusOK, agentReleaseResp{
				TargetVersion: t.ToVersion,
				URL:           "/api/v1/releases/" + t.ToVersion + "/binary",
				SHA256:        rel.SHA256,
				Signature:     rel.Signature,
				SignedBy:      rel.SignedBy,
			})
			return
		}
	}
	writeJSON(w, http.StatusOK, agentReleaseResp{TargetVersion: current})
}

// --- admin-facing: create / inspect / control a rollout ---

type startRolloutInput struct {
	Version     string     `json:"version"`
	Pops        []string   `json:"pops"`
	SoakSeconds int        `json:"soak_seconds"`
	ScheduledAt *time.Time `json:"scheduled_at,omitempty"`
}

func (a *API) startRollout(w http.ResponseWriter, r *http.Request) {
	var in startRolloutInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "bad json")
		return
	}
	if in.Version == "" || len(in.Pops) == 0 {
		writeError(w, http.StatusBadRequest, "version and pops are required")
		return
	}
	if _, err := a.store.GetReleaseMeta(r.Context(), in.Version); err != nil {
		writeError(w, http.StatusBadRequest, "no such release version")
		return
	}
	if _, active, err := a.store.ActiveRollout(r.Context()); err == nil && active {
		writeError(w, http.StatusConflict, "a rollout is already in progress")
		return
	}
	// record each target's current (from) version so the dashboard + rollback know it.
	from := map[string]string{}
	if servers, err := a.store.ListServers(r.Context()); err == nil {
		for _, s := range servers {
			from[s.EdgeID] = s.AgentVersion
		}
	}
	id, err := a.store.CreateRollout(r.Context(), store.CreateRolloutParams{
		ReleaseVersion: in.Version, TargetPops: in.Pops, SoakSeconds: in.SoakSeconds,
		ScheduledAt: in.ScheduledAt, FromVersions: from,
	})
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	_ = a.store.WriteAudit(r.Context(), a.actorID(r), "rollout.start", in.Version, "")
	slog.Info("rollout started", "id", id, "version", in.Version, "pops", in.Pops)
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

type rolloutDetail struct {
	Rollout store.Rollout         `json:"rollout"`
	Targets []store.RolloutTarget `json:"targets"`
}

func (a *API) activeRollout(w http.ResponseWriter, r *http.Request) {
	rollout, ok, err := a.store.ActiveRollout(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeJSON(w, http.StatusOK, nil)
		return
	}
	targets, _ := a.store.ListTargets(r.Context(), rollout.ID)
	writeJSON(w, http.StatusOK, rolloutDetail{Rollout: rollout, Targets: targets})
}

func (a *API) getRolloutByID(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	rollout, err := a.store.GetRollout(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "no such rollout")
		return
	}
	targets, _ := a.store.ListTargets(r.Context(), id)
	writeJSON(w, http.StatusOK, rolloutDetail{Rollout: rollout, Targets: targets})
}

// pauseRollout / resumeRollout / cancelRollout flip the active rollout's status; the engine
// respects it on its next tick.
func (a *API) pauseRollout(w http.ResponseWriter, r *http.Request)  { a.setRolloutStatus(w, r, "paused", "running") }
func (a *API) resumeRollout(w http.ResponseWriter, r *http.Request) { a.setRolloutStatus(w, r, "running", "paused") }
func (a *API) cancelRollout(w http.ResponseWriter, r *http.Request) { a.setRolloutStatus(w, r, "cancelled", "") }

func (a *API) setRolloutStatus(w http.ResponseWriter, r *http.Request, to, requireFrom string) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	rollout, err := a.store.GetRollout(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "no such rollout")
		return
	}
	if requireFrom != "" && rollout.Status != requireFrom {
		writeError(w, http.StatusConflict, "rollout is not "+requireFrom)
		return
	}
	if err := a.store.SetRolloutStatus(r.Context(), id, to, ""); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = a.store.WriteAudit(r.Context(), a.actorID(r), "rollout."+to, strconv.FormatInt(id, 10), "")
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": to})
}

type rollbackInput struct {
	Version string `json:"version"` // the version to roll back TO (usually the previous release)
}

// rollbackRollout cancels the active rollout and starts a new one taking the same PoPs back to
// the requested version. Same gated, one-at-a-time, health-soaked engine in reverse.
func (a *API) rollbackRollout(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var in rollbackInput
	_ = json.NewDecoder(r.Body).Decode(&in)
	rollout, err := a.store.GetRollout(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "no such rollout")
		return
	}
	to := in.Version
	if to == "" {
		// default: the version the targets came from (their from_version).
		if targets, terr := a.store.ListTargets(r.Context(), id); terr == nil {
			for _, t := range targets {
				if t.FromVersion != "" {
					to = t.FromVersion
					break
				}
			}
		}
	}
	if to == "" {
		writeError(w, http.StatusBadRequest, "could not determine a version to roll back to; pass {\"version\":\"x\"}")
		return
	}
	if _, err := a.store.GetReleaseMeta(r.Context(), to); err != nil {
		writeError(w, http.StatusBadRequest, "rollback target release not found: "+to)
		return
	}
	// stop the current rollout, then start the reverse one.
	if rollout.Status == "running" || rollout.Status == "paused" || rollout.Status == "scheduled" {
		_ = a.store.SetRolloutStatus(r.Context(), id, "cancelled", "rolled back")
	}
	from := map[string]string{}
	if servers, err := a.store.ListServers(r.Context()); err == nil {
		for _, s := range servers {
			from[s.EdgeID] = s.AgentVersion
		}
	}
	newID, err := a.store.CreateRollout(r.Context(), store.CreateRolloutParams{
		ReleaseVersion: to, TargetPops: rollout.TargetPops, SoakSeconds: rollout.SoakSeconds, FromVersions: from,
	})
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	_ = a.store.WriteAudit(r.Context(), a.actorID(r), "rollout.rollback", to, "")
	writeJSON(w, http.StatusCreated, map[string]any{"id": newID, "version": to})
}
