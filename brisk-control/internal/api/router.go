// Package api wires the chi router, middleware, and HTTP handlers.
package api

import (
	"log/slog"
	"net/http"
	"time"

	"brisk-control/internal/acme"
	"brisk-control/internal/adminauth"
	"brisk-control/internal/auth"
	"brisk-control/internal/config"
	"brisk-control/internal/customdomains"
	"brisk-control/internal/dns"
	"brisk-control/internal/health"
	"brisk-control/internal/provision"
	"brisk-control/internal/purge"
	"brisk-control/internal/store"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
)

// API holds dependencies shared by handlers.
type API struct {
	store *store.Store
	cfg   *config.Config
	prov  *provision.Provisioner
	auth  auth.Authenticator
	pub           *purge.Publisher // nil when NATS is not configured
	dns           *dns.Client         // nil when Bunny DNS is not configured
	sync          *dns.Syncer         // nil when Bunny DNS is not configured
	loadSteer     *dns.LoadController // nil when load-feedback steering is disabled (#3)
	healthChecker *health.Checker     // nil when health checking is disabled
	tlsMgr        *acme.Manager       // nil when managed TLS is not configured

	// Custom domains + per-domain auto-TLS (Phase 4 Step 2). challengeStore is the
	// shared HTTP-01 keyAuth source the unauthenticated challenge handler serves;
	// customDomains drives verify/issue/renew. Both nil when custom-domain TLS is
	// not configured (the row-management endpoints still work; verify returns 503).
	challengeStore *acme.ChallengeStore
	customDomains  *customdomains.Manager

	// Human (dashboard/API) auth — Phase 3.7 Step 3. Agent auth (a.auth) is separate.
	loginLimiter *adminauth.LoginLimiter
	cookieSecure bool
}

// New builds the HTTP handler with the full middleware stack and routes.
func New(st *store.Store, cfg *config.Config, prov *provision.Provisioner, authr auth.Authenticator, pub *purge.Publisher, dnsClient *dns.Client, syncer *dns.Syncer, loadSteer *dns.LoadController, healthChecker *health.Checker, tlsMgr *acme.Manager, challengeStore *acme.ChallengeStore, customDomainsMgr *customdomains.Manager) http.Handler {
	a := &API{store: st, cfg: cfg, prov: prov, auth: authr, pub: pub, dns: dnsClient, sync: syncer, loadSteer: loadSteer, healthChecker: healthChecker, tlsMgr: tlsMgr,
		challengeStore: challengeStore, customDomains: customDomainsMgr,
		// 5 bad logins/min per IP+email -> locked 15m. Tune via env later if needed.
		loginLimiter: adminauth.NewLoginLimiter(5, time.Minute, 15*time.Minute),
		cookieSecure: cfg.CookieSecure,
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(slogLogger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(15 * time.Second))
	// Credentialed CORS: the dashboard sends the session cookie cross-origin
	// (localhost:5173 -> :8080), so we must reflect a SPECIFIC origin (never "*"
	// with credentials) and allow credentials + the CSRF header.
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   cfg.DashboardOrigins(),
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Content-Type", "Authorization", "X-CSRF-Token"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	r.Get("/health", a.health)

	// ACME HTTP-01 challenge — UNAUTHENTICATED, at the root (Phase 4 Step 2). The
	// edges proxy :80 /.well-known/acme-challenge/* here over the agent tunnel, so
	// whichever geo-routed edge the CA hits, the control plane answers the keyAuth.
	r.Get("/.well-known/acme-challenge/{token}", a.acmeChallenge)

	r.Route("/api/v1", func(r chi.Router) {
		// --- public auth endpoints (rate-limited login; logout is idempotent) ---
		r.Post("/auth/login", a.login)
		r.Post("/auth/logout", a.logout)

		// --- human-authed admin/dashboard routes (Phase 3.7 Step 3) ---
		// requireAuth resolves a session cookie OR bearer admin token to an identity
		// and enforces CSRF on cookie-based writes. 401 unauthenticated.
		r.Group(func(r chi.Router) {
			r.Use(a.requireAuth)

			r.Get("/auth/me", a.authMe)
			r.Post("/auth/refresh", a.refresh)
			r.Post("/admin/password", a.changePassword)
			r.Get("/admin/tokens", a.listAdminTokens)
			r.Post("/admin/tokens", a.createAdminToken)
			r.Delete("/admin/tokens/{id}", a.revokeAdminToken)
			r.Delete("/admin/tokens/{id}/purge", a.deleteAdminToken) // hard-delete an already-revoked token

			// account-scoped resources: zones (tenant scoping enforced in handlers;
			// admin sees all, a customer only its own account_id).
			r.Get("/zones", a.listZones)
			r.Post("/zones", a.createZone)
			r.Get("/zones/{id}", a.getZone)
			r.Put("/zones/{id}", a.updateZone)
			r.Delete("/zones/{id}", a.deleteZone)
			r.Get("/zones/{id}/rules", a.listRules)
			r.Post("/zones/{id}/rules", a.createRule)
			r.Put("/zones/{id}/rules/{rid}", a.updateRule)      // atomic edit (Phase 4 Step 6)
			r.Post("/zones/{id}/rules/reorder", a.reorderRules) // no-churn reorder
			r.Delete("/zones/{id}/rules/{rid}", a.deleteRule)
			r.Get("/zones/{id}/servers", a.listZoneServers) // inverse lookup
			r.Post("/zones/{id}/purge", a.purgeZone)

			// custom domains + per-domain auto-TLS (Phase 4 Step 2). Tenant-scoped:
			// a customer manages only its own zones' domains (enforced in handlers).
			r.Get("/zones/{id}/domains", a.listZoneDomains)
			r.Post("/zones/{id}/domains", a.addCustomDomain)
			r.Post("/domains/{id}/verify", a.verifyCustomDomain)
			r.Delete("/domains/{id}", a.deleteCustomDomain)

			// per-zone origin shield toggle (Phase 4 Step 3); tenant-scoped.
			r.Post("/zones/{id}/shield", a.setZoneShield)

			// per-zone WAF + rate limiting (Phase 4 Step 4); tenant-scoped (a
			// customer manages only its own zones' security; admin all).
			r.Get("/zones/{id}/waf", a.getZoneWAF)
			r.Put("/zones/{id}/waf", a.setZoneWAF)
			r.Get("/zones/{id}/waf/rules", a.listWAFRules)
			r.Post("/zones/{id}/waf/rules", a.createWAFRule)
			r.Delete("/zones/{id}/waf/rules/{rid}", a.deleteWAFRule)
			r.Get("/zones/{id}/waf/ratelimits", a.listWAFRateLimits)
			r.Post("/zones/{id}/waf/ratelimits", a.createWAFRateLimit)
			r.Delete("/zones/{id}/waf/ratelimits/{rid}", a.deleteWAFRateLimit)
			r.Get("/zones/{id}/security-events", a.listZoneSecurityEvents)
			r.Get("/zones/{id}/logs", a.listZoneLogs)                   // real edge request logs (Phase 4 Step 6)
			r.Get("/zones/{id}/logs/analytics", a.zoneLogsAnalytics) // real offload/status/latency/top-paths (Parts 3+4)

			// per-zone header transforms (Phase 4 Step 5 — Lua edge logic);
			// tenant-scoped. Cache rules already live under /zones/{id}/rules.
			r.Get("/zones/{id}/header-transforms", a.listHeaderTransforms)
			r.Post("/zones/{id}/header-transforms", a.createHeaderTransform)
			r.Delete("/zones/{id}/header-transforms/{tid}", a.deleteHeaderTransform)

			// per-zone Cache Settings (Bunny-style Smart Cache controls); tenant-
			// scoped. GET is unnecessary — getZone returns cache_settings inline.
			r.Put("/zones/{id}/cache-settings", a.setZoneCacheSettings)

			// per-zone Hotlink Protection (Referer allowlist); tenant-scoped. GET is
			// unnecessary — getZone returns the hotlink_* fields inline.
			r.Put("/zones/{id}/hotlink", a.setZoneHotlink)

			// per-zone custom 502/504 error page (Bunny-style); tenant-scoped. GET is
			// unnecessary — getZone returns error_5xx_html inline.
			r.Put("/zones/{id}/error-page", a.setZoneErrorPage)

			// per-zone Blocked-IP denylist (Bunny-style); tenant-scoped. GET is
			// unnecessary — getZone returns blocked_ips inline.
			r.Put("/zones/{id}/blocked-ips", a.setZoneBlockedIPs)

			// per-zone access toggles (block root path / block POST); tenant-scoped.
			// GET is unnecessary — getZone returns block_root_path/block_post inline.
			r.Put("/zones/{id}/access-flags", a.setZoneAccessFlags)

			// infrastructure resources: admin-only regardless of account.
			r.Group(func(r chi.Router) {
				r.Use(a.requireAdmin)
				a.adminInfraRoutes(r)
			})
		})

		// --- agent routes (bearer-token auth; mTLS-swappable later) ---
		// UNTOUCHED by Step 3 — the 3 live agents keep using their per-agent tokens.
		r.Group(func(r chi.Router) {
			r.Use(auth.Middleware(a.auth))
			r.Post("/agent/heartbeat", a.heartbeat)
			r.Get("/agent/config", a.agentConfig)
			r.Post("/agent/stats", a.statsIngest)
			r.Post("/agent/security-events", a.securityEventsIngest) // WAF firewall log (Phase 4 Step 4)
			r.Post("/agent/logs", a.logsIngest)                     // structured request logs (Phase 4 Step 6)
			r.Post("/agent/purge/ack", a.purgeAck)
			// Self-update binary download (signed release; agent verifies sig before swapping).
			r.Get("/releases/{version}/binary", a.getReleaseBinary)
			// Per-edge desired version (no-op unless this edge's rollout wave is open).
			r.Get("/agent/release", a.agentRelease)
		})
	})

	return r
}

// adminInfraRoutes registers the admin-only infrastructure endpoints (servers,
// DNS, stats, health, network purge). Split out so the route table above reads as
// auth layers, not one giant block.
func (a *API) adminInfraRoutes(r chi.Router) {
	{
		// --- admin routes ---
		r.Get("/servers", a.listServers)
		r.Post("/servers", a.createServer)
		r.Get("/servers/{id}", a.getServer)
		r.Delete("/servers/{id}", a.deleteServer)
		r.Post("/servers/{id}/reprovision", a.reprovision)
		r.Post("/servers/{id}/status", a.setServerStatus)
		r.Post("/servers/{id}/routing", a.setServerRouting)
		r.Post("/servers/{id}/role", a.setServerRole) // edge|shield (Phase 4 Step 3)
		r.Post("/servers/{id}/health", a.setServerHealth)
		// Drain / maintenance mode + rotation state (Phase 3 Step 5)
		r.Post("/servers/{id}/drain", a.drainServer)
		r.Post("/servers/{id}/undrain", a.undrainServer)
		r.Get("/servers/{id}/rotation", a.serverRotation)
		r.Post("/regions/{region}/drain", a.drainRegion)
		r.Post("/regions/{region}/undrain", a.undrainRegion)
		r.Post("/servers/{id}/token/rotate", a.rotateToken)
		r.Get("/servers/{id}/provision-log", a.provisionLog)
		r.Get("/servers/{id}/zones", a.listServerZones)
		r.Post("/servers/{id}/zones", a.assignZone)
		r.Delete("/servers/{id}/zones/{zoneId}", a.unassignZone)
		r.Get("/servers/{id}/live", a.serverLive)

		// (zones + per-zone purge are account-scoped -> registered in the authed
		// group above, not here. Network-wide purge stays admin-only.)
		r.Post("/purge/all", a.purgeAll)
		r.Get("/purge/jobs", a.listPurgeJobs)

		// stats query endpoints (dashboard, Step 6)
		r.Get("/overview", a.overview)
		r.Get("/stats", a.statsSeries)
		r.Get("/stats/network", a.networkStats) // server-side network aggregate (Phase 4 Step 6)

		// Bunny DNS — thin test plumbing (Phase 3 Step 1; UI lands in Step 5)
		r.Get("/dns/status", a.dnsStatus)
		r.Get("/dns/records", a.dnsListRecords)
		r.Post("/dns/records", a.dnsAddRecord)
		r.Delete("/dns/records/{id}", a.dnsDeleteRecord)
		// DNS deletion-protection lock (time-delayed unlock)
		r.Get("/dns/lock", a.dnsLockStatus)
		r.Post("/dns/lock/request-unlock", a.dnsRequestUnlock)
		r.Post("/dns/lock/cancel", a.dnsCancelUnlock)
		r.Post("/dns/lock/relock", a.dnsRelock)
		r.Post("/dns/lock/delay", a.dnsSetDelay)
		// DNS auto-registration reconciler (Phase 3 Step 2)
		r.Post("/dns/reconcile", a.dnsReconcile)
		r.Get("/dns/reconcile/preview", a.dnsReconcilePreview)
		r.Get("/dns/audit", a.dnsAuditList)
			// Smart-Record geo/latency routing (Phase 3 Step 3)
			r.Get("/dns/routing", a.dnsRouting)
			// Server-side IP geolocation for the Add-Server region auto-detect (no
			// browser CORS; the control plane proxies a keyless GeoIP lookup).
			r.Get("/geoip", a.geoIP)
			// Self-driven health checks + fast failover (Phase 3 Step 4)
			r.Get("/health/status", a.healthStatus)
			r.Get("/health/config", a.healthConfig)
			// Managed wildcard TLS (Phase 3.7 Step 2, Part 3 — lego Bunny DNS-01)
			r.Get("/tls/status", a.tlsStatus)
			r.Post("/tls/reissue", a.tlsReissue)
			// Custom domains across all tenants (Phase 4 Step 2 — ops visibility:
			// statuses, last_error, renewal health). Admin-only.
			r.Get("/domains", a.adminListCustomDomains)
			// Security events across all tenants (Phase 4 Step 4 — WAF firewall
			// log + the top-attacked-zones / top-blocked-IPs overview). Admin-only.
			r.Get("/security-events", a.adminSecurityEvents)
			r.Get("/security-events/summary", a.adminSecuritySummary)
			// Request logs across all tenants (Phase 4 Step 6). Admin-only.
			r.Get("/logs", a.adminLogs)
			r.Get("/logs/analytics", a.adminLogsAnalytics) // network offload/status/latency/top-paths

			// Agent releases (self-service rollout): upload a signed binary, list, delete.
			// Signature is verified server-side on upload (internal/signing). The binary
			// itself is served to agents on the agent-token route below, not here.
			r.Post("/releases", a.uploadRelease)
			r.Get("/releases", a.listReleases)
			r.Delete("/releases/{version}", a.deleteRelease)

			// Rollouts: start a deploy, watch live progress, pause/resume/cancel/rollback.
			r.Post("/rollouts", a.startRollout)
			r.Get("/rollouts/active", a.activeRollout)
			r.Get("/rollouts/{id}", a.getRolloutByID)
			r.Post("/rollouts/{id}/pause", a.pauseRollout)
			r.Post("/rollouts/{id}/resume", a.resumeRollout)
			r.Post("/rollouts/{id}/cancel", a.cancelRollout)
			r.Post("/rollouts/{id}/rollback", a.rollbackRollout)

			// Deploy/pause/rollback/upload audit trail (newest first; ?limit=N).
			r.Get("/audit", a.listAudit)
	}
}

// slogLogger logs each request via log/slog with method, path, status, duration.
func slogLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		slog.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", ww.Status(),
			"bytes", ww.BytesWritten(),
			"dur_ms", time.Since(start).Milliseconds(),
			"reqid", middleware.GetReqID(r.Context()),
		)
	})
}
