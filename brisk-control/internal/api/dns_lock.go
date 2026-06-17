package api

import (
	"net/http"
	"time"

	"brisk-control/internal/store"
)

// lockResponse is the lock state shape returned to the dashboard.
func lockResponse(s store.DNSSettings) map[string]any {
	now := time.Now().UTC()
	resp := map[string]any{
		"locked":               s.Locked,
		"state":                s.State(now),
		"unlock_delay_seconds": s.UnlockDelaySeconds,
		"updated_at":           s.UpdatedAt,
	}
	if s.UnlockRequestedAt != nil {
		resp["unlock_requested_at"] = *s.UnlockRequestedAt
		at := s.UnlockAvailableAt()
		resp["unlock_available_at"] = *at
		remaining := int(at.Sub(now).Seconds())
		if remaining < 0 {
			remaining = 0
		}
		resp["seconds_remaining"] = remaining
	}
	return resp
}

func (a *API) dnsLockStatus(w http.ResponseWriter, r *http.Request) {
	s, err := a.store.GetDNSSettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, lockResponse(s))
}

// dnsRequestUnlock starts the time-delayed unlock cooldown.
func (a *API) dnsRequestUnlock(w http.ResponseWriter, r *http.Request) {
	s, err := a.store.RequestDNSUnlock(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, lockResponse(s))
}

// dnsCancelUnlock cancels a pending unlock (stays locked).
func (a *API) dnsCancelUnlock(w http.ResponseWriter, r *http.Request) {
	s, err := a.store.CancelDNSUnlock(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, lockResponse(s))
}

// dnsRelock re-locks immediately (locking is always allowed — only unlocking is delayed).
func (a *API) dnsRelock(w http.ResponseWriter, r *http.Request) {
	s, err := a.store.LockDNS(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, lockResponse(s))
}

type setDelayInput struct {
	Seconds int `json:"seconds" validate:"required,min=10,max=86400"`
}

// dnsSetDelay changes the unlock cooldown length (10s..24h).
func (a *API) dnsSetDelay(w http.ResponseWriter, r *http.Request) {
	var in setDelayInput
	if !decode(w, r, &in) {
		return
	}
	s, err := a.store.SetDNSUnlockDelay(r.Context(), in.Seconds)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, lockResponse(s))
}

// requireDNSUnlocked enforces the deletion lock. Returns true if the caller may
// proceed with a destructive DNS op; otherwise writes 423 Locked and returns false.
func (a *API) requireDNSUnlocked(w http.ResponseWriter, r *http.Request) bool {
	s, err := a.store.GetDNSSettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return false
	}
	if s.IsUnlocked(time.Now().UTC()) {
		return true
	}
	// 423 Locked — surface the state so the UI can prompt the unlock flow.
	writeJSON(w, http.StatusLocked, map[string]any{
		"error": "DNS is locked. Request an unlock and wait the cooldown before deleting records.",
		"lock":  lockResponse(s),
	})
	return false
}
