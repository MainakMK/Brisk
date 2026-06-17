package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"brisk-control/internal/identity"
	"brisk-control/internal/store"

	"github.com/go-chi/chi/v5"
	"golang.org/x/net/publicsuffix"
)

// --- Custom domains + per-domain auto-TLS (Phase 4 Step 2) ---
//
// A tenant attaches THEIR OWN domain to a zone; Brisk verifies the CNAME lands on
// Brisk, auto-issues a per-domain HTTP-01 cert, fans it to edges, and serves it
// via SNI. These handlers manage the lifecycle rows; the customdomains.Manager
// does the verify/issue/renew work; the unauthenticated challenge handler answers
// the CA through the edges' challenge proxy.

// customDomainView is the dashboard-safe projection: the lifecycle row + CNAME
// guidance + cert metadata (never key/chain material).
type customDomainView struct {
	store.CustomDomain
	CNAMETarget   string     `json:"cname_target"`
	IsApex        bool       `json:"is_apex"`
	Instructions  string     `json:"instructions"`
	ZoneHostname  string     `json:"zone_hostname,omitempty"` // admin cross-tenant list
	CertIssuer    string     `json:"cert_issuer,omitempty"`
	CertStaging   *bool      `json:"cert_staging,omitempty"`
	CertNotAfter  *time.Time `json:"cert_not_after,omitempty"`
	DaysRemaining *int       `json:"days_remaining,omitempty"`
}

// buildDomainView enriches a lifecycle row with CNAME instructions (target = the
// zone's CDN hostname) and, when a cert exists, its metadata.
func (a *API) buildDomainView(r *http.Request, d store.CustomDomain, cdnHostname string) customDomainView {
	apex := isApex(d.Domain)
	v := customDomainView{
		CustomDomain: d,
		CNAMETarget:  cdnHostname,
		IsApex:       apex,
		Instructions: cnameInstruction(d.Domain, cdnHostname, apex),
		ZoneHostname: cdnHostname,
	}
	if name := strings.TrimSpace(d.CertName); name != "" {
		if c, err := a.store.GetTLSCert(r.Context(), name); err == nil {
			v.CertIssuer = c.Issuer
			st := c.Staging
			v.CertStaging = &st
			v.CertNotAfter = c.NotAfter
			if c.NotAfter != nil {
				days := int(time.Until(*c.NotAfter).Hours() / 24)
				v.DaysRemaining = &days
			}
		}
	}
	return v
}

type addDomainInput struct {
	Domain string `json:"domain" validate:"required,hostname"`
}

// addCustomDomain attaches a domain to a zone (tenant-scoped). It returns
// pending_dns + the exact CNAME record to create. Duplicate domain -> 409.
func (a *API) addCustomDomain(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	z, ok := a.scopeZone(w, r, id)
	if !ok {
		return
	}
	var in addDomainInput
	if !decode(w, r, &in) {
		return
	}
	domain := strings.ToLower(strings.TrimSpace(in.Domain))
	if domain == strings.ToLower(z.CDNHostname) {
		writeError(w, http.StatusBadRequest, "that is the zone's Brisk CDN hostname, not a custom domain")
		return
	}

	cd, err := a.store.CreateCustomDomain(r.Context(), z.ID, z.AccountID, domain)
	if err != nil {
		if store.IsUniqueViolation(err) {
			writeError(w, http.StatusConflict, "domain already attached to a zone")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Kick an immediate verification pass in the background (the scan loop would
	// otherwise pick it up within ~30s). Best-effort; never blocks the response.
	if a.customDomains != nil {
		go a.customDomains.CheckNow(context.Background(), cd.ID)
	}
	writeJSON(w, http.StatusCreated, a.buildDomainView(r, cd, z.CDNHostname))
}

// listZoneDomains lists a zone's custom domains with status + cert info.
func (a *API) listZoneDomains(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	z, ok := a.scopeZone(w, r, id)
	if !ok {
		return
	}
	domains, err := a.store.ListCustomDomainsByZone(r.Context(), z.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	views := make([]customDomainView, 0, len(domains))
	for _, d := range domains {
		views = append(views, a.buildDomainView(r, d, z.CDNHostname))
	}
	writeJSON(w, http.StatusOK, views)
}

// scopeDomain loads a custom domain + its parent zone and enforces tenant access.
func (a *API) scopeDomain(w http.ResponseWriter, r *http.Request) (store.CustomDomain, store.Zone, bool) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return store.CustomDomain{}, store.Zone{}, false
	}
	d, err := a.store.GetCustomDomain(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "domain not found")
		return store.CustomDomain{}, store.Zone{}, false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return store.CustomDomain{}, store.Zone{}, false
	}
	z, err := a.store.GetZone(r.Context(), d.ZoneID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return store.CustomDomain{}, store.Zone{}, false
	}
	cid, _ := identity.FromContext(r.Context())
	if identity.Authorize(cid, z.AccountID) != nil {
		writeError(w, http.StatusForbidden, "forbidden")
		return store.CustomDomain{}, store.Zone{}, false
	}
	return d, z, true
}

// verifyCustomDomain runs the "check now" verification (and issuance if it passes)
// synchronously and returns the refreshed row.
func (a *API) verifyCustomDomain(w http.ResponseWriter, r *http.Request) {
	d, z, ok := a.scopeDomain(w, r)
	if !ok {
		return
	}
	if a.customDomains == nil {
		writeError(w, http.StatusServiceUnavailable, "custom-domain TLS not configured")
		return
	}
	refreshed, err := a.customDomains.CheckNow(r.Context(), d.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, a.buildDomainView(r, refreshed, z.CDNHostname))
}

// deleteCustomDomain detaches a domain: removes the lifecycle row + its per-domain
// cert, then bumps the parent zone's config_version so edges drop the vhost.
func (a *API) deleteCustomDomain(w http.ResponseWriter, r *http.Request) {
	d, z, ok := a.scopeDomain(w, r)
	if !ok {
		return
	}
	if name := strings.TrimSpace(d.CertName); name != "" {
		_ = a.store.DeleteTLSCert(r.Context(), name) // stop fanning the cert out
	}
	if err := a.store.DeleteCustomDomain(r.Context(), d.ID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "domain not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = a.store.BumpZoneConfigVersion(r.Context(), z.ID) // edges re-pull -> vhost removed
	w.WriteHeader(http.StatusNoContent)
}

// adminListCustomDomains returns every custom domain across tenants (ops
// visibility: statuses, errors, renewal health). Admin-only.
func (a *API) adminListCustomDomains(w http.ResponseWriter, r *http.Request) {
	domains, err := a.store.ListAllCustomDomains(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Resolve each domain's zone hostname (small N; one map build).
	zones, err := a.store.ListZones(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	hostByZone := make(map[int64]string, len(zones))
	for _, z := range zones {
		hostByZone[z.ID] = z.CDNHostname
	}
	views := make([]customDomainView, 0, len(domains))
	for _, d := range domains {
		views = append(views, a.buildDomainView(r, d, hostByZone[d.ZoneID]))
	}
	writeJSON(w, http.StatusOK, views)
}

// acmeChallenge answers the ACME HTTP-01 validation. It is UNAUTHENTICATED and
// mounted at the root (the edges proxy :80 /.well-known/acme-challenge/* here over
// the agent tunnel). It returns the keyAuth for a live token, else 404.
func (a *API) acmeChallenge(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	if a.challengeStore == nil || token == "" {
		http.NotFound(w, r)
		return
	}
	keyAuth, ok := a.challengeStore.Get(token)
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(keyAuth))
}

// cnameInstruction builds the exact, copy-able DNS guidance for a domain. Apex
// domains can't carry a CNAME, so we steer to ALIAS/flattening or a subdomain —
// never per-edge A records (which would bypass Brisk's geo routing + failover).
func cnameInstruction(domain, target string, apex bool) string {
	if target == "" {
		target = "your zone's Brisk CDN hostname"
	}
	if apex {
		return "\"" + domain + "\" is a root/apex domain — DNS rules forbid a CNAME at the apex. " +
			"Use your DNS provider's ALIAS / ANAME / CNAME-flattening to point it at " + target + ", " +
			"or (recommended) add a subdomain like www." + domain + " with a normal CNAME to " + target + ". " +
			"Brisk does not hand out per-edge A records — that would bypass geo routing and failover."
	}
	return "Create this DNS record at your provider →  " + domain + "  CNAME  " + target +
		"  (propagation can take minutes to 48h; Brisk re-checks automatically)."
}

// isApex reports whether domain is a registrable apex (eTLD+1 == domain), using
// the public suffix list so co.uk-style suffixes are handled correctly.
func isApex(domain string) bool {
	domain = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")
	etld1, err := publicsuffix.EffectiveTLDPlusOne(domain)
	return err == nil && etld1 == domain
}
