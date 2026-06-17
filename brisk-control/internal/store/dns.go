package store

import (
	"context"
	"time"
)

// DNSSettings is the singleton DNS deletion-protection lock state.
type DNSSettings struct {
	Locked             bool       `json:"locked"`
	UnlockRequestedAt  *time.Time `json:"unlock_requested_at,omitempty"`
	UnlockDelaySeconds int        `json:"unlock_delay_seconds"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

// UnlockAvailableAt returns when a pending unlock takes effect (nil if none).
func (s DNSSettings) UnlockAvailableAt() *time.Time {
	if s.UnlockRequestedAt == nil {
		return nil
	}
	t := s.UnlockRequestedAt.Add(time.Duration(s.UnlockDelaySeconds) * time.Second)
	return &t
}

// IsUnlocked reports whether destructive DNS ops are currently permitted: either
// the lock is off, or a requested unlock's cooldown has fully elapsed.
func (s DNSSettings) IsUnlocked(now time.Time) bool {
	if !s.Locked {
		return true
	}
	at := s.UnlockAvailableAt()
	return at != nil && !now.Before(*at)
}

// State returns "unlocked" | "pending" | "locked" for the UI.
func (s DNSSettings) State(now time.Time) string {
	if s.IsUnlocked(now) {
		return "unlocked"
	}
	if s.UnlockRequestedAt != nil {
		return "pending"
	}
	return "locked"
}

// GetDNSSettings returns the singleton settings row (created by migration).
func (st *Store) GetDNSSettings(ctx context.Context) (DNSSettings, error) {
	var s DNSSettings
	err := st.pool.QueryRow(ctx,
		`SELECT locked, unlock_requested_at, unlock_delay_seconds, updated_at
		   FROM dns_settings WHERE id = 1`).
		Scan(&s.Locked, &s.UnlockRequestedAt, &s.UnlockDelaySeconds, &s.UpdatedAt)
	return s, err
}

// RequestDNSUnlock starts the unlock cooldown (no-op if already unlocked).
func (st *Store) RequestDNSUnlock(ctx context.Context) (DNSSettings, error) {
	_, err := st.pool.Exec(ctx,
		`UPDATE dns_settings
		    SET unlock_requested_at = COALESCE(unlock_requested_at, now()),
		        locked = true,
		        updated_at = now()
		  WHERE id = 1`)
	if err != nil {
		return DNSSettings{}, err
	}
	return st.GetDNSSettings(ctx)
}

// LockDNS re-locks immediately and clears any pending unlock.
func (st *Store) LockDNS(ctx context.Context) (DNSSettings, error) {
	_, err := st.pool.Exec(ctx,
		`UPDATE dns_settings
		    SET locked = true, unlock_requested_at = NULL, updated_at = now()
		  WHERE id = 1`)
	if err != nil {
		return DNSSettings{}, err
	}
	return st.GetDNSSettings(ctx)
}

// CancelDNSUnlock clears a pending unlock request (stays locked).
func (st *Store) CancelDNSUnlock(ctx context.Context) (DNSSettings, error) {
	return st.LockDNS(ctx)
}

// SetDNSUnlockDelay updates the cooldown length (bounded by the caller).
func (st *Store) SetDNSUnlockDelay(ctx context.Context, seconds int) (DNSSettings, error) {
	_, err := st.pool.Exec(ctx,
		`UPDATE dns_settings SET unlock_delay_seconds = $1, updated_at = now() WHERE id = 1`, seconds)
	if err != nil {
		return DNSSettings{}, err
	}
	return st.GetDNSSettings(ctx)
}

// --- DNS reconcile audit (Step 2) ---

// DNSAudit is one recorded DNS change applied by the reconciler.
type DNSAudit struct {
	ID         int64     `json:"id"`
	Action     string    `json:"action"`
	EdgeID     string    `json:"edge_id"`
	RecordName string    `json:"record_name"`
	Value      string    `json:"value"`
	RecordID   *int64    `json:"record_id,omitempty"`
	Reason     string    `json:"reason"`
	CreatedAt  time.Time `json:"created_at"`
}

// AddDNSAudit records one applied DNS action.
func (st *Store) AddDNSAudit(ctx context.Context, a DNSAudit) error {
	_, err := st.pool.Exec(ctx,
		`INSERT INTO dns_audit (action, edge_id, record_name, value, record_id, reason)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		a.Action, a.EdgeID, a.RecordName, a.Value, a.RecordID, a.Reason)
	return err
}

// ListDNSAudit returns recent DNS audit entries, newest first.
func (st *Store) ListDNSAudit(ctx context.Context, limit int) ([]DNSAudit, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := st.pool.Query(ctx,
		`SELECT id, action, COALESCE(edge_id,''), COALESCE(record_name,''), COALESCE(value,''),
		        record_id, reason, created_at
		 FROM dns_audit ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DNSAudit{}
	for rows.Next() {
		var a DNSAudit
		if err := rows.Scan(&a.ID, &a.Action, &a.EdgeID, &a.RecordName, &a.Value, &a.RecordID, &a.Reason, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
