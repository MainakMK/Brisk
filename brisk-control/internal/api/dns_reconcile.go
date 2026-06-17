package api

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"brisk-control/internal/dns"
	"brisk-control/internal/store"
)

// triggerDNSReconcile schedules a debounced reconcile (no-op if DNS off).
func (a *API) triggerDNSReconcile(reason string) {
	if a.sync != nil {
		a.sync.Trigger(reason)
	}
}

// removeServerDNS removes a server's routing record directly (lifecycle delete).
// This bypasses the deletion lock on purpose: deleting the *server* is an
// explicit, authorized action. Best-effort — it never fails the server delete.
func (a *API) removeServerDNS(ctx context.Context, edgeID, reason string) {
	if a.dns == nil || edgeID == "" {
		return
	}
	recs, err := a.dns.ListRecords(ctx)
	if err != nil {
		slog.Warn("dns: list failed during server delete", "edge_id", edgeID, "err", err.Error())
		return
	}
	tag := dns.ServerTag(edgeID)
	for _, rec := range recs {
		if rec.Comment != tag {
			continue
		}
		if err := a.dns.DeleteRecord(ctx, rec.ID); err != nil {
			slog.Warn("dns: remove record failed during server delete", "edge_id", edgeID, "err", err.Error())
			return
		}
		id := rec.ID
		_ = a.store.AddDNSAudit(ctx, store.DNSAudit{
			Action: "remove", EdgeID: edgeID, RecordName: rec.Name, Value: rec.Value, RecordID: &id, Reason: reason,
		})
		slog.Info("dns: removed routing record for deleted server", "edge_id", edgeID)
	}
}

// dnsReconcile runs a reconcile now. ?dry_run=true returns the diff without
// applying. Returns the plan (actions).
func (a *API) dnsReconcile(w http.ResponseWriter, r *http.Request) {
	if a.sync == nil {
		writeError(w, http.StatusServiceUnavailable, "DNS not configured")
		return
	}
	dryRun := r.URL.Query().Get("dry_run") == "true"
	plan, err := a.sync.Reconcile(r.Context(), dryRun, "manual")
	if err != nil {
		writeError(w, http.StatusBadGateway, dnsErrMsg(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"dry_run":   dryRun,
		"changes":   len(plan.Actions),
		"actions":   plan.Actions,
		"converged": plan.Empty(),
	})
}

// dnsReconcilePreview is a dry-run diff (planned adds/enables/disables/removes).
func (a *API) dnsReconcilePreview(w http.ResponseWriter, r *http.Request) {
	if a.sync == nil {
		writeError(w, http.StatusServiceUnavailable, "DNS not configured")
		return
	}
	plan, err := a.sync.Reconcile(r.Context(), true, "preview")
	if err != nil {
		writeError(w, http.StatusBadGateway, dnsErrMsg(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"changes":   len(plan.Actions),
		"actions":   plan.Actions,
		"converged": plan.Empty(),
	})
}

// dnsAuditList returns the recent DNS reconcile audit trail.
func (a *API) dnsAuditList(w http.ResponseWriter, r *http.Request) {
	entries, err := a.store.ListDNSAudit(r.Context(), 100)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, entries)
}

type setStatusInput struct {
	Status string `json:"status" validate:"required,oneof=online offline disabled drained active"`
}

// setServerStatus changes a server's lifecycle status (e.g. disable/drain) and
// triggers a DNS reconcile so the routing record follows (disabled-but-kept).
func (a *API) setServerStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	var in setStatusInput
	if !decode(w, r, &in) {
		return
	}
	status := strings.ToLower(in.Status)
	if status == "active" {
		status = "online"
	}
	if err := a.store.UpdateServerStatus(r.Context(), id, status); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.triggerDNSReconcile("status_change")
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": status})
}
