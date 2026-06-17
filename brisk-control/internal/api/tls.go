package api

import (
	"context"
	"net/http"
	"time"
)

// tlsCertView is the dashboard-safe projection of a managed cert: metadata only,
// never the chain body or private key.
type tlsCertView struct {
	Name          string     `json:"name"`
	Domains       string     `json:"domains"`
	Issuer        string     `json:"issuer"`
	Serial        string     `json:"serial"`
	Staging       bool       `json:"staging"`
	NotBefore     *time.Time `json:"not_before,omitempty"`
	NotAfter      *time.Time `json:"not_after,omitempty"`
	DaysRemaining *int       `json:"days_remaining,omitempty"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// tlsStatus reports the managed-TLS configuration and the metadata of every
// control-plane-issued cert. Never returns key/chain material.
func (a *API) tlsStatus(w http.ResponseWriter, r *http.Request) {
	certs, err := a.store.ListTLSCerts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	views := make([]tlsCertView, 0, len(certs))
	for _, c := range certs {
		v := tlsCertView{
			Name: c.Name, Domains: c.Domains, Issuer: c.Issuer, Serial: c.Serial,
			Staging: c.Staging, NotBefore: c.NotBefore, NotAfter: c.NotAfter, UpdatedAt: c.UpdatedAt,
		}
		if c.NotAfter != nil {
			d := int(time.Until(*c.NotAfter).Hours() / 24)
			v.DaysRemaining = &d
		}
		views = append(views, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"managed":   a.cfg.TLSManaged,
		"staging":   a.cfg.TLSStaging,
		"domains":   a.cfg.TLSDomains,
		"cert_name": a.cfg.TLSCertName,
		"certs":     views,
	})
}

// tlsReissue forces an issue/renew check now (operator control during the
// staging->production cutover). Runs in the background — ACME can take ~a minute —
// and returns 202 immediately. 503 when managed TLS is not configured.
func (a *API) tlsReissue(w http.ResponseWriter, r *http.Request) {
	if a.tlsMgr == nil {
		writeError(w, http.StatusServiceUnavailable, "managed TLS not configured")
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		// EnsureOnce logs its own success/failure via the manager's logger.
		_, _ = a.tlsMgr.EnsureOnce(ctx)
	}()
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "reissue scheduled"})
}
