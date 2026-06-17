// Command brisk-control is the Brisk control-plane API (Phase 2).
//
// On startup it loads config from the environment, runs the embedded goose
// migrations against Postgres/TimescaleDB, opens a pgx pool, and serves the
// REST API (chi) until SIGINT/SIGTERM, then shuts down gracefully.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"brisk-control/internal/acme"
	"brisk-control/internal/adminauth"
	"brisk-control/internal/api"
	"brisk-control/internal/auth"
	"brisk-control/internal/config"
	"brisk-control/internal/customdomains"
	"brisk-control/internal/dns"
	"brisk-control/internal/health"
	"brisk-control/internal/migrate"
	"brisk-control/internal/provision"
	"brisk-control/internal/purge"
	"brisk-control/internal/rollout"
	"brisk-control/internal/store"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("config", "err", err)
		os.Exit(1)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.SlogLevel()}))
	slog.SetDefault(logger)

	// 1) migrations (before serving) — idempotent.
	slog.Info("running migrations")
	if err := migrate.Up(cfg.DatabaseURL); err != nil {
		slog.Error("migrations failed", "err", err)
		os.Exit(1)
	}

	// 2) DB pool.
	ctx := context.Background()
	st, err := store.New(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("db connect failed", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	// 2b) Bootstrap the first admin (Phase 3.7 Step 3). Seeds account id 1's login
	// email + argon2 password hash from env ONLY when it has no password yet (so a
	// password later changed via the dashboard is never clobbered). No hardcoded
	// default; the password is never logged.
	if cfg.AdminEmail != "" && cfg.AdminPassword != "" {
		acc, err := st.GetAccountByID(ctx, 1)
		if err != nil {
			slog.Error("admin bootstrap: load account failed", "err", err)
			os.Exit(1)
		}
		if acc.PasswordHash != nil && *acc.PasswordHash != "" {
			slog.Info("admin account already has credentials; skipping bootstrap")
		} else {
			hash, herr := adminauth.HashPassword(cfg.AdminPassword)
			if herr != nil {
				slog.Error("admin bootstrap: hash failed", "err", herr)
				os.Exit(1)
			}
			email := strings.ToLower(strings.TrimSpace(cfg.AdminEmail))
			if err := st.SetAccountCredentials(ctx, 1, &email, hash); err != nil {
				slog.Error("admin bootstrap: persist failed", "err", err)
				os.Exit(1)
			}
			slog.Info("bootstrapped admin account", "email", email)
		}
	} else {
		slog.Warn("no BRISK_ADMIN_EMAIL/PASSWORD set — admin login unavailable until seeded")
	}

	// 3) NATS JetStream publisher for instant purge (optional — control plane
	// still serves everything else if NATS is down; the data plane is independent).
	pub, err := purge.New(ctx, cfg.NatsURL)
	if err != nil {
		slog.Error("nats connect failed", "err", err)
		os.Exit(1)
	}
	if pub != nil {
		defer pub.Close()
		slog.Info("nats purge channel ready", "url", cfg.NatsURL)
	} else {
		slog.Warn("NATS_URL not set — instant purge disabled")
	}

	// 3b) Bunny DNS client (Phase 3) — optional. The control plane runs fine
	// without it; a bad key/zone is surfaced via /dns/status, never a crash, and
	// the API key is never logged.
	dnsClient, err := dns.New(ctx, cfg.BunnyAPIKey, cfg.BunnyDNSZoneID, cfg.BunnyDNSZone)
	if err != nil {
		slog.Warn("bunny dns not initialized (will report via /dns/status)", "err", err.Error())
		dnsClient = nil
	}
	switch {
	case dnsClient == nil && cfg.BunnyAPIKey == "":
		slog.Info("bunny dns not configured (BUNNY_API_KEY unset)")
	case dnsClient != nil:
		if count, perr := dnsClient.Ping(ctx); perr != nil {
			slog.Warn("bunny dns configured but unreachable at startup", "zone_id", dnsClient.ZoneID())
		} else {
			slog.Info("bunny dns ready", "zone_id", dnsClient.ZoneID(), "records", count)
		}
	}

	// Load-feedback steering controller (#3). Declared before the syncer so the
	// endpoints closure can read its per-edge factor; constructed AFTER the syncer
	// (it needs syncer.Trigger). Nil until then + nil when steering is disabled —
	// LoadController.Factor is nil-safe (returns the neutral 1.0), so the closure is
	// always valid and a disabled fleet renders byte-identical DNS.
	var loadCtl *dns.LoadController

	// 3c) DNS auto-registration syncer (Phase 3 Step 2). Converges Bunny DNS to
	// the servers table on lifecycle events + a periodic safety reconcile. The
	// endpoints/audit closures keep the dns package store-free.
	// Bunny native DNS monitor (Layer-2 failover backstop) — GATED. When
	// BRISK_DNS_MONITOR is on, the reconciler stamps MonitorType on every managed
	// A record so Bunny's own infra probes each edge and drops a dead one even if
	// the control plane is down. Off (default) => MonitorType 0 (records unchanged).
	dnsMonitorType := dns.MonitorNone
	if cfg.BriskDNSMonitor {
		dnsMonitorType = dns.MonitorTypeFromString(cfg.BriskDNSMonitorType)
	}
	syncer := dns.NewSyncer(dnsClient, dns.SyncOpts{
		RoutingName: cfg.BriskCDNRecord,
		TTL:         cfg.BriskDNSTTL,
		StaleAfter:  60 * time.Second,
		Periodic:    2 * time.Minute,
		Debounce:    3 * time.Second,
		Mode:        dns.NormalizeMode(cfg.BriskDNSRoutingMode),
		MonitorType: dnsMonitorType,
	},
		func(ctx context.Context) ([]dns.Endpoint, error) {
			servers, err := st.ListServers(ctx)
			if err != nil {
				return nil, err
			}
			eps := make([]dns.Endpoint, 0, len(servers))
			for _, s := range servers {
				// Origin Shield (Phase 4 Step 3) + Hybrid (Build Spec Layer 2): a
				// PURE role=shield PoP (serve_public=false) is a mid-tier cache, NOT a
				// user-facing edge — keep it out of the geo DNS set. A HYBRID shield
				// (serve_public=true) DOES serve users, so it stays in the set.
				if strings.EqualFold(s.Role, "shield") && !s.ServePublic {
					continue
				}
				capMbps := 0
				if s.CapacityMbps != nil {
					capMbps = int(*s.CapacityMbps)
				}
				eps = append(eps, dns.Endpoint{
					EdgeID: s.EdgeID, Region: s.Region, IP: s.IP, Status: s.Status, LastSeen: s.LastSeen,
					Weight: int(s.RoutingWeight), RoutingOverride: s.RoutingOverride,
					Health: s.HealthStatus, Drained: s.Drained,
					CapacityMbps: capMbps, WeightByCapacity: s.WeightByCapacity,
					// Live load-feedback factor (#3): 1.0 when steering is off (nil-safe).
					LoadFactor: loadCtl.Factor(s.EdgeID),
				})
			}
			return eps, nil
		},
		// Per-zone DNS intent (Phase 4 Step 7 Part 2.5): map each active tenant zone
		// to its relative Bunny name + assigned edge ids (server_zones is the single
		// source of truth). The apex cdn.<zone> zone is excluded (owned by the apex
		// reconciler), as are hostnames not under our Bunny zone (custom domains).
		func(ctx context.Context) ([]dns.ZoneRouting, error) {
			zoneName := strings.ToLower(strings.TrimSpace(dnsClient.ZoneName()))
			routing := strings.ToLower(strings.TrimSpace(cfg.BriskCDNRecord))
			if zoneName == "" || routing == "" || routing == "@" {
				return nil, nil // can't derive tenant relative names without a zone domain
			}
			zones, err := st.ListActiveZones(ctx)
			if err != nil {
				return nil, err
			}
			suffix := "." + zoneName
			out := make([]dns.ZoneRouting, 0, len(zones))
			for _, z := range zones {
				host := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(z.CDNHostname), "."))
				if !strings.HasSuffix(host, suffix) {
					continue // hostname not under our Bunny zone (e.g. a custom domain)
				}
				rel := strings.TrimSuffix(host, suffix) // "testjim.cdn" or "cdn"
				if rel == routing {
					continue // the apex cdn.<zone> zone — owned by the apex reconciler
				}
				if !strings.HasSuffix(rel, "."+routing) {
					continue // only <label>.cdn.<zone> tenant hostnames get a per-zone A-set
				}
				servers, err := st.ListZoneServers(ctx, z.ID)
				if err != nil {
					return nil, err
				}
				ids := make([]string, 0, len(servers))
				for _, sv := range servers {
					if strings.EqualFold(sv.Role, "shield") && !sv.ServePublic {
						continue // PURE shields never serve users directly (hybrids do)
					}
					ids = append(ids, sv.EdgeID)
				}
				out = append(out, dns.ZoneRouting{ZoneID: z.ID, Name: rel, AssignedEdgeIDs: ids})
			}
			return out, nil
		},
		func(ctx context.Context, applied []dns.Action, reason string) {
			for _, act := range applied {
				var rid *int64
				if act.RecordID != 0 {
					id := act.RecordID
					rid = &id
				}
				_ = st.AddDNSAudit(ctx, store.DNSAudit{
					Action: act.Kind, EdgeID: act.EdgeID, RecordName: act.Name, Value: act.Value, RecordID: rid, Reason: reason,
				})
			}
		},
	)
	if syncer != nil {
		go syncer.Run(ctx)
		slog.Info("dns syncer running", "routing_name", cfg.BriskCDNRecord, "ttl", cfg.BriskDNSTTL,
			"routing_mode", dns.NormalizeMode(cfg.BriskDNSRoutingMode),
			"bunny_monitor", cfg.BriskDNSMonitor, "bunny_monitor_type", dnsMonitorType)
	}

	// 3c-2) Load-feedback steering (#3) — GATED, off by default. When enabled (and DNS
	// is configured), the controller samples each edge's live pressure (load ÷
	// capacity) on a timer and nudges its DNS weight via the endpoints closure above.
	// The LoadFunc reads the latest stats sample per online edge that has a capacity
	// set (capacity is needed to turn raw bandwidth into a utilization); edges without
	// capacity, or offline, contribute no pressure and stay at their full base weight.
	// Pressure = max(CPU utilization, bandwidth ÷ capacity), clamped to [0,1].
	if cfg.LoadSteerEnabled && syncer != nil {
		loadCtl = dns.NewLoadController(
			func(ctx context.Context) (map[string]float64, error) {
				servers, err := st.ListServers(ctx)
				if err != nil {
					return nil, err
				}
				out := make(map[string]float64, len(servers))
				for _, s := range servers {
					if !strings.EqualFold(s.Status, "online") || s.CapacityMbps == nil || *s.CapacityMbps <= 0 {
						continue // need an online edge with a known capacity to compute utilization
					}
					live, lerr := st.ServerLive(ctx, s.ID)
					if lerr != nil {
						continue // no recent sample — leave this edge unsteered this tick
					}
					capBps := float64(*s.CapacityMbps) * 1_000_000 // Mbps -> bits/sec
					bwUtil := 0.0
					if capBps > 0 {
						bwUtil = live.BandwidthBps / capBps
					}
					cpuUtil := 0.0
					if live.CPUPct != nil {
						cpuUtil = *live.CPUPct / 100.0
					}
					pressure := bwUtil
					if cpuUtil > pressure {
						pressure = cpuUtil
					}
					out[s.EdgeID] = pressure
				}
				return out, nil
			},
			syncer.Trigger,
			dns.LoadSteerOpts{
				Interval:  time.Duration(cfg.LoadSteerInterval) * time.Second,
				LowWater:  cfg.LoadSteerLowWater,
				HighWater: cfg.LoadSteerHighWater,
				MinFactor: cfg.LoadSteerMinFactor,
			},
		)
		go loadCtl.Run(ctx)
	} else {
		slog.Info("load steer disabled (BRISK_LOAD_STEER_ENABLED=false or DNS off)")
	}

	// 3d) Self-driven health checker (Phase 3 Step 4 — fast failover). Probes each
	// online edge; on a healthy<->unhealthy transition it persists the verdict and
	// triggers an immediate reconcile so the edge's DNS record is disabled/enabled
	// right away (not waiting on Bunny's ~30s monitor). DNS off => checker still
	// runs and records health, it just has no reconcile sink.
	healthChecker := health.New(
		health.Config{
			Enabled:       cfg.HealthEnabled,
			Interval:      time.Duration(cfg.HealthInterval) * time.Second,
			Timeout:       time.Duration(cfg.HealthTimeout) * time.Second,
			FailThreshold: cfg.HealthFailThreshold,
			RiseThreshold: cfg.HealthRiseThreshold,
			Path:          cfg.HealthPath,
			Scheme:        cfg.HealthScheme,
			Port:          cfg.HealthPort,
		},
		// targets: probe online edges by hostname (preferred) or IP.
		func(ctx context.Context) ([]health.Target, error) {
			servers, err := st.ListServers(ctx)
			if err != nil {
				return nil, err
			}
			targets := make([]health.Target, 0, len(servers))
			for _, s := range servers {
				if !strings.EqualFold(s.Status, "online") {
					continue // only edges that should be serving are probed
				}
				host := s.IP
				if s.Hostname != nil && *s.Hostname != "" {
					host = *s.Hostname
				}
				targets = append(targets, health.Target{
					ServerID: s.ID, EdgeID: s.EdgeID, Host: host,
					Persisted:       s.HealthStatus,
					Enabled:         s.HealthEnabled,
					IntervalSeconds: int(s.HealthIntervalSeconds),
					FailThreshold:   int(s.HealthFailThreshold),
					RiseThreshold:   int(s.HealthRiseThreshold),
				})
			}
			return targets, nil
		},
		// onTransition: persist the verdict, then reconcile immediately so DNS
		// follows. The reconcile audits the disable/enable with the health reason.
		func(ctx context.Context, t health.Target, healthy bool) {
			status, reason := "unhealthy", "health_unhealthy"
			if healthy {
				status, reason = "healthy", "health_recovered"
			}
			if err := st.SetServerHealth(ctx, t.ServerID, status); err != nil {
				slog.Warn("health: persist failed", "edge_id", t.EdgeID, "err", err.Error())
			}
			if syncer != nil {
				if _, err := syncer.Reconcile(ctx, false, reason); err != nil {
					slog.Warn("health: reconcile failed", "edge_id", t.EdgeID, "err", err.Error())
				}
			}
		},
	)
	if healthChecker != nil {
		go healthChecker.Run(ctx)
	} else {
		slog.Info("health checker disabled (BRISK_HEALTH_ENABLED=false)")
	}

	// 3e) Managed wildcard TLS (Phase 3.7 Step 2, Part 3). The control plane issues
	// + renews the wildcard cert centrally via lego Bunny DNS-01 (reusing the Bunny
	// key) and fans it to edges over the agent-config channel — retiring per-edge
	// acme.sh. Disabled unless BRISK_TLS_MANAGED=true with an email, domains, and a
	// Bunny key. The data plane is unaffected if this is off (edges keep their
	// existing certs); a bad ACME run is logged and retried, never a crash.
	var tlsMgr *acme.Manager
	switch {
	case !cfg.TLSManaged:
		slog.Info("managed TLS disabled (BRISK_TLS_MANAGED=false)")
	case cfg.BunnyAPIKey == "" || cfg.TLSEmail == "" || len(cfg.TLSDomains) == 0:
		slog.Warn("managed TLS requested but not fully configured — skipping",
			"have_bunny_key", cfg.BunnyAPIKey != "", "have_email", cfg.TLSEmail != "",
			"domains", len(cfg.TLSDomains))
	default:
		issuer := &acme.Issuer{
			Email:      cfg.TLSEmail,
			Staging:    cfg.TLSStaging,
			BunnyKey:   cfg.BunnyAPIKey,
			AccountDir: cfg.TLSAccountDir,
		}
		tlsMgr = acme.NewManager(issuer, st, cfg.TLSCertName, cfg.TLSDomains, cfg.TLSStaging, logger)
		go tlsMgr.Run(ctx)
		slog.Info("managed TLS running", "cert_name", cfg.TLSCertName, "domains", cfg.TLSDomains,
			"staging", cfg.TLSStaging)

		// Phase 4 Step 7 Part 3: the TENANT wildcard cert `*.<routing>.<zone>`
		// (e.g. *.cdn.a2zjav.com) so every <tenant>.cdn.<zone> serves valid TLS
		// regardless of its PoP set. ADDITIVE — a SECOND managed cert with its own
		// store key, issued + renewed independently; the live apex `*.<zone>` cert is
		// never touched. It flows to edges over the same channel: ListTLSCerts feeds
		// attachManagedCerts, and certCovers' one-label rule keeps the two wildcards
		// non-overlapping (apex covers cdn.<zone>; tenant covers <x>.cdn.<zone>). The
		// agent needs no change — it WriteManaged's whatever cert it's handed per zone.
		if dnsClient != nil && cfg.BriskCDNRecord != "" && cfg.BriskCDNRecord != "@" {
			zoneName := strings.TrimSpace(dnsClient.ZoneName())
			if zoneName != "" {
				tenantWildcard := "*." + cfg.BriskCDNRecord + "." + zoneName // *.cdn.a2zjav.com
				tenantName := cfg.BriskCDNRecord + "." + zoneName            // store key cdn.a2zjav.com
				tenantMgr := acme.NewManager(issuer, st, tenantName, []string{tenantWildcard}, cfg.TLSStaging, logger)
				go tenantMgr.Run(ctx)
				slog.Info("managed TLS running (tenant wildcard)", "cert_name", tenantName,
					"domains", []string{tenantWildcard}, "staging", cfg.TLSStaging)
			} else {
				slog.Warn("tenant wildcard cert skipped: zone name unresolved")
			}
		}
	}

	// 3f) Custom domains + per-domain auto-TLS (Phase 4 Step 2 — the commercial
	// gateway). A tenant CNAMEs their own domain at a Brisk zone; the control plane
	// verifies the DNS lands on Brisk, then auto-issues a per-domain cert via lego
	// HTTP-01 (answered by the edges' challenge proxy -> this process), stores it in
	// tls_certs, and fans it to edges over the agent-config channel (served via SNI).
	// The challenge store is shared by reference between the issuer (writer) and the
	// unauthenticated challenge handler (reader). Needs only an ACME email — HTTP-01
	// does NOT touch the customer's DNS, so no Bunny key required. The data plane is
	// unaffected when off (edges just don't get custom-domain vhosts).
	challengeStore := acme.NewChallengeStore()
	var cdMgr *customdomains.Manager
	if cfg.TLSEmail != "" {
		cdIssuer := &acme.Issuer{
			Email:      cfg.TLSEmail,
			Staging:    cfg.CustomTLSStaging, // independent of the wildcard's staging flag
			BunnyKey:   cfg.BunnyAPIKey,      // unused by HTTP-01; kept for a single issuer shape
			AccountDir: cfg.TLSAccountDir,
		}
		cdMgr = customdomains.NewManager(st, cdIssuer, challengeStore, cfg.CustomTLSStaging, logger)
		go cdMgr.Run(ctx)
		slog.Info("custom-domain TLS running", "staging", cfg.CustomTLSStaging)
	} else {
		slog.Info("custom-domain TLS disabled (no BRISK_TLS_EMAIL)")
	}

	// 3g) Rollout engine (self-service agent rollout). GATED by BRISK_ROLLOUT_ENABLED
	// (default true). Idle with no active rollout, so it's effectively a kill switch.
	// The edge-state source reads each PoP's live heartbeat (agent_version) + health:
	// Online = online and not drained, Healthy = not flagged unhealthy. The engine opens
	// one PoP's wave at a time and advances only after the soak window of health.
	if cfg.RolloutEnabled {
		edgeState := func(edgeID string) rollout.EdgeState {
			servers, err := st.ListServers(ctx)
			if err != nil {
				return rollout.EdgeState{}
			}
			for _, s := range servers {
				if s.EdgeID == edgeID {
					return rollout.EdgeState{
						Online:  strings.EqualFold(s.Status, "online") && !s.Drained,
						Version: s.AgentVersion,
						Healthy: !strings.EqualFold(s.HealthStatus, "unhealthy"),
					}
				}
			}
			return rollout.EdgeState{}
		}
		go rollout.NewEngine(st, edgeState).Run(ctx, 3*time.Second)

		// Release retention: keep the newest N agent releases, but never delete a version still
		// running on a live edge (so rollback always has a binary to fall back to). Runs hourly.
		go func() {
			const keep = 10
			t := time.NewTicker(time.Hour)
			defer t.Stop()
			prune := func() {
				protect := map[string]struct{}{}
				if servers, err := st.ListServers(ctx); err == nil {
					for _, s := range servers {
						if s.AgentVersion != "" {
							protect[s.AgentVersion] = struct{}{}
						}
					}
				}
				keys := make([]string, 0, len(protect))
				for v := range protect {
					keys = append(keys, v)
				}
				if err := st.PruneReleases(ctx, keep, keys); err != nil {
					slog.Warn("release prune failed", "err", err)
				}
			}
			prune() // once at boot
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					prune()
				}
			}
		}()
	} else {
		slog.Info("rollout engine disabled (BRISK_ROLLOUT_ENABLED=false)")
	}

	// 4) HTTP server.
	authr := auth.NewTokenAuthenticator(st)
	prov := provision.New(st, cfg.AgentControlPlaneURL, cfg.AgentNatsURL)
	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           api.New(st, cfg, prov, authr, pub, dnsClient, syncer, loadCtl, healthChecker, tlsMgr, challengeStore, cdMgr),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		slog.Info("brisk-control listening", "addr", cfg.ListenAddr, "env", cfg.Env)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	// 4) graceful shutdown.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown error", "err", err)
	}
}
