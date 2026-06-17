// Command brisk-agent is the Brisk edge control agent.
//
// Modes:
//   - standalone (Phase 1): control_plane_url empty -> config from local agent.yaml.
//   - pull (Phase 2 Step 3): control_plane_url + token set -> config pulled from
//     brisk-control, cached locally as last-known-good. The edge loads the
//     last-known-good first (so it serves even if the control plane is down at
//     boot), then polls (conditional GET / ETag) to converge to the latest.
//
// It also heartbeats (Step 2) and renews Let's Encrypt certs (Phase 1 Step 7).
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"brisk-agent/bootstrap"
	"brisk-agent/client"
	"brisk-agent/config"
	"brisk-agent/logship"
	"brisk-agent/nginx"
	"brisk-agent/purge"
	"brisk-agent/selfupdate"
	"brisk-agent/stats"
	"brisk-agent/tls"
	"brisk-agent/waf"
)

const (
	renewInterval        = 12 * time.Hour
	heartbeatInterval    = 30 * time.Second
	releaseCheckInterval = 30 * time.Second
	minBackoff           = 2 * time.Second
	maxBackoff           = 2 * time.Minute
)

// agentVersion is the running agent's version. It defaults to the value baked here, but CI
// stamps the git tag in at build time via `-ldflags "-X main.agentVersion=<tag>"` so a
// pushed release reports the new version (which is what the self-update loop compares against).
// Must be a var (not const) for -X to take effect.
var agentVersion = "0.3.0"

func main() {
	configPath := flag.String("config", "/etc/brisk/agent.yaml", "path to agent.yaml")
	nginxBin := flag.String("nginx-bin", "nginx", "nginx executable (name on PATH or absolute path)")
	nginxConf := flag.String("nginx-conf", "/etc/nginx/nginx.conf", "path to the nginx.conf the agent owns")
	oneshot := flag.Bool("oneshot", false, "render + apply once, then exit (useful for tests)")
	doBootstrap := flag.Bool("bootstrap", false, "run the one-time idempotent edge install, then exit")
	doRenew := flag.Bool("renew", false, "force a Let's Encrypt re-issue for all LE zones, then exit")
	doRender := flag.Bool("render", false, "print the nginx.conf this agent would generate, then exit (no disk/nginx/TLS side effects)")
	flag.Parse()

	log.SetPrefix("brisk-agent: ")
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)

	if *doBootstrap {
		if err := bootstrap.Run(*configPath); err != nil {
			log.Fatalf("bootstrap failed: %v", err)
		}
		log.Printf("bootstrap complete")
		return
	}

	mgr, err := nginx.NewManager(*nginxBin, *nginxConf)
	if err != nil {
		log.Fatalf("init nginx manager: %v", err)
	}

	// Load the local base config once (agent-level settings + fallback zones).
	base, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	// --render: dump the generated nginx.conf and exit. Pure (no disk writes, no
	// nginx exec, no cert provisioning) — handy for reviewing/diffing the config a
	// given agent.yaml would produce, incl. multi-tenant vhosts (Phase 4 Step 1).
	if *doRender {
		out, rerr := mgr.Render(base)
		if rerr != nil {
			log.Fatalf("render failed: %v", rerr)
		}
		os.Stdout.Write(out)
		return
	}

	if *doRenew {
		if err := mgr.RenewTLS(base, tlsManagerFor(base)); err != nil {
			log.Fatalf("renew failed: %v", err)
		}
		log.Printf("certificates renewed and nginx reloaded")
		return
	}

	// Pick the config source. Pull mode overlays control-plane zones onto the
	// local agent-level settings; standalone reads agent.yaml directly.
	var src config.Source
	var cpSrc *config.ControlPlaneSource
	if base.PullMode() {
		cpSrc = config.NewControlPlaneSource(base, base.ConfigCachePath())
		src = cpSrc
	} else {
		src = config.NewFileSource(*configPath)
	}

	// WAF engine (Phase 4 Step 4): the agent owns a loopback Coraza service that
	// nginx auth_request calls per request. Built before the first apply so the
	// initial config compiles each WAF-enabled zone's ruleset; reapply reloads it
	// after every successful apply. In pull mode the firewall log ships to the
	// control plane; standalone (lab) it is only logged. The service + ship loop run
	// regardless of mode (nginx only calls it when a zone has WAF on).
	var wafShip waf.ShipFunc
	if base.PullMode() {
		cp := client.New(base.ControlPlaneURL, base.EffectiveToken(), base.EdgeID, agentVersion, "", "", "", "")
		wafShip = cp.ShipSecurityEvents
	}
	wafBuf := waf.NewEventBuffer(wafShip, base.StatsIntervalDuration())
	wafEngine := waf.NewEngine(wafBuf)
	go startWAFService(base, wafEngine)
	go wafBuf.Run(context.Background())

	// Startup: serve the last-known-good FIRST (cache or local) — the edge comes
	// up even if the control plane is unreachable at boot.
	if err := reapply(src, mgr, wafEngine); err != nil {
		if *oneshot {
			log.Fatalf("initial apply failed: %v", err)
		}
		// Daemon mode: do NOT crash-loop on a bad initial config (e.g. a poisoned
		// last-known-good cache that fails validation). nginx keeps its current
		// config; the pull loop will fetch a good config and re-apply. Exiting here
		// only blackholes the box — the exact BLR crash-loop we hit. Stay up.
		log.Printf("initial apply failed (staying up; pull loop will retry): %v", err)
	} else {
		log.Printf("nginx config applied (%s mode)", modeName(base))
	}

	if *oneshot {
		return
	}

	// Self-update commit/rollback: if a rollout just swapped THIS binary in, prove we're
	// healthy now; if not (after a few restart attempts) restore the previous binary. systemd
	// (Restart=always) relaunches us either way. A no-op on a normal boot (no update marker).
	if selfupdate.SelfCheckOnStart(selfupdate.DefaultPaths(), 3, agentSelfCheck) {
		log.Printf("self-update: new binary failed its health check — rolled back to previous; exiting for restart")
		selfupdate.RestartSelf()
	}

	// Periodic re-apply so Let's Encrypt certs renew within margin (both modes).
	go renewLoop(src, mgr, wafEngine)

	if cpSrc != nil {
		go heartbeatLoop(base)
		go configPollLoop(cpSrc, mgr, base, wafEngine) // converge to the latest control-plane config
		go startStats(base)                 // collect + ship metrics (Step 4)
		go startPurge(base)                 // instant purge over NATS (Step 5)
		go startLogShip(base)               // tail + ship structured request logs (Step 6)
		go releaseLoop(base)                // self-update to a newer signed agent when a rollout opens this edge's wave
	} else {
		log.Printf("standalone mode (no control plane configured)")
	}

	// Service loop: reload on SIGHUP, exit on SIGINT/SIGTERM.
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)
	for sig := range sigs {
		switch sig {
		case syscall.SIGHUP:
			log.Printf("SIGHUP: re-applying config")
			if err := reapply(src, mgr, wafEngine); err != nil {
				log.Printf("reload failed (keeping previous config): %v", err)
			} else {
				log.Printf("config reloaded")
			}
		case syscall.SIGINT, syscall.SIGTERM:
			log.Printf("%s: shutting down", sig)
			return
		}
	}
}

// reapply fetches the current config from the source and applies it to Nginx
// (validate before reload, roll back on failure). On success it reloads the WAF
// engine so each zone's Coraza ruleset matches the just-applied config. The engine
// reload happens AFTER a successful nginx apply so the two never diverge (a
// rolled-back config keeps the previous WAF rules too).
func reapply(src config.Source, mgr *nginx.Manager, wafEngine *waf.Engine) error {
	cfg, err := src.Fetch()
	if err != nil {
		return err
	}
	if err := mgr.ApplyWithTLS(cfg, tlsManagerFor(cfg)); err != nil {
		return err
	}
	if wafEngine != nil {
		wafEngine.Reload(cfg.Zones)
	}
	return nil
}

// startWAFService runs the loopback Coraza WAF HTTP service that nginx auth_request
// calls per request (Phase 4 Step 4). Loopback-only; nginx proxies /_waf to it.
func startWAFService(base *config.Config, engine *waf.Engine) {
	addr := base.WAFListen
	if addr == "" {
		addr = "127.0.0.1:9555"
	}
	srv := &http.Server{Addr: addr, Handler: waf.NewServer(engine)}
	log.Printf("waf: inspection service listening on %s", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Printf("waf: service stopped: %v", err)
	}
}

// tlsManagerFor builds a TLS manager from the agent config.
func tlsManagerFor(cfg *config.Config) *tls.Manager {
	return tls.NewManager("", cfg.LetsEncryptEmail, cfg.LetsEncryptStaging, "")
}

// renewLoop periodically re-applies so Let's Encrypt certs renew before expiry.
func renewLoop(src config.Source, mgr *nginx.Manager, wafEngine *waf.Engine) {
	t := time.NewTicker(renewInterval)
	defer t.Stop()
	for range t.C {
		if err := reapply(src, mgr, wafEngine); err != nil {
			log.Printf("renewal cycle failed (keeping previous config): %v", err)
		}
	}
}

// configPollLoop polls the control plane (conditional GET) and applies changes.
// Unchanged polls get a 304 (no work). On error it keeps serving the
// last-known-good and backs off (capped) with jitter.
func configPollLoop(src *config.ControlPlaneSource, mgr *nginx.Manager, base *config.Config, wafEngine *waf.Engine) {
	cp := client.New(base.ControlPlaneURL, base.EffectiveToken(), base.EdgeID, agentVersion, "", "", "", "")
	interval := base.PollIntervalDuration()
	etag := src.CachedETag()
	backoff := minBackoff

	for {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		status, body, newETag, err := cp.PullConfig(ctx, etag)
		cancel()

		next := interval
		switch {
		case err != nil && status != http.StatusOK:
			log.Printf("config pull failed (keeping last-known-good): %v", err)
			next = backoff
			backoff = nextBackoff(backoff)
		case status == http.StatusNotModified:
			backoff = minBackoff // unchanged; nothing to do
		case status == http.StatusOK:
			backoff = minBackoff
			if werr := src.WriteCache(body); werr != nil {
				log.Printf("cache write failed: %v", werr)
				break
			}
			etag = newETag
			if aerr := reapply(src, mgr, wafEngine); aerr != nil {
				log.Printf("pulled config apply failed (rolled back, keeping previous): %v", aerr)
			} else {
				log.Printf("config updated from control plane (etag %s)", etag)
			}
		}
		time.Sleep(jitter(next))
	}
}

// agentSelfCheck is the post-swap liveness gate: nginx must be serving (/healthz 200). Used by
// SelfCheckOnStart to decide commit-vs-rollback after a self-update.
func agentSelfCheck() error {
	c := &http.Client{Timeout: 4 * time.Second}
	resp, err := c.Get("http://127.0.0.1/healthz")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz returned %d", resp.StatusCode)
	}
	return nil
}

// releaseLoop polls the control plane for THIS edge's desired agent version. When a rollout opens
// this edge's wave with a newer version, it downloads the signed binary, verifies the signature,
// swaps it in, and restarts (systemd relaunches the new binary; the boot self-check then commits
// or rolls back). Inert until a rollout targets this edge.
func releaseLoop(base *config.Config) {
	nginxVer, _ := bootstrap.DetectNginxVersion()
	osPretty, _ := bootstrap.DetectOSPretty()
	kernel, _ := bootstrap.DetectKernel()
	cp := client.New(base.ControlPlaneURL, base.EffectiveToken(), base.EdgeID, agentVersion, nginxVer, osPretty, kernel, runtime.Version())
	t := time.NewTicker(releaseCheckInterval)
	defer t.Stop()
	for range t.C {
		// Generous budget: this one context covers FetchRelease (quick) AND DownloadBinary, which
		// streams a ~20MB signed binary over the reverse SSH tunnel. 6m comfortably exceeds the
		// download client's 5m timeout so a slow tunnel doesn't abort a legitimate update.
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
		ri, err := cp.FetchRelease(ctx)
		if err != nil {
			cancel()
			continue
		}
		if ri.TargetVersion == "" || ri.TargetVersion == agentVersion {
			cancel() // up to date / no wave open for this edge
			continue
		}
		log.Printf("self-update: control plane assigned version %s (running %s) — downloading", ri.TargetVersion, agentVersion)
		data, derr := cp.DownloadBinary(ctx, ri.TargetVersion)
		cancel()
		if derr != nil {
			log.Printf("self-update: download %s failed: %v", ri.TargetVersion, derr)
			continue
		}
		if !selfupdate.VerifyBinary(data, ri.SHA256, ri.Signature) {
			log.Printf("self-update: REFUSED %s — signature/sha verification failed", ri.TargetVersion)
			continue
		}
		if _, aerr := selfupdate.Apply(selfupdate.DefaultPaths(), data, ri.TargetVersion); aerr != nil {
			log.Printf("self-update: apply %s failed: %v", ri.TargetVersion, aerr)
			continue
		}
		log.Printf("self-update: swapped to %s — restarting", ri.TargetVersion)
		selfupdate.RestartSelf()
	}
}

// heartbeatLoop posts an authenticated heartbeat so the control plane marks this
// edge online. It never logs the token.
func heartbeatLoop(base *config.Config) {
	nginxVer, _ := bootstrap.DetectNginxVersion()
	osPretty, _ := bootstrap.DetectOSPretty()
	kernel, _ := bootstrap.DetectKernel()
	goVer := runtime.Version()
	cp := client.New(base.ControlPlaneURL, base.EffectiveToken(), base.EdgeID, agentVersion, nginxVer, osPretty, kernel, goVer)
	send := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := cp.Heartbeat(ctx); err != nil {
			log.Printf("heartbeat failed: %v", err)
			return
		}
		log.Printf("heartbeat ok -> %s", base.ControlPlaneURL)
	}
	send()
	t := time.NewTicker(heartbeatInterval)
	defer t.Stop()
	for range t.C {
		send()
	}
}

// startStats collects edge metrics and ships them to the control plane. The
// stub_status endpoint and access log are local to this host; cache-disk usage
// is reported for base.CacheDir.
func startStats(base *config.Config) {
	cp := client.New(base.ControlPlaneURL, base.EffectiveToken(), base.EdgeID, agentVersion, "", "", "", "")
	coll := stats.NewCollector("http://127.0.0.1:8081/brisk_status", "/var/log/nginx/brisk.access.log", base.CacheDir)
	rep := stats.NewReporter(coll, cp.ShipStats, base.StatsIntervalDuration())
	rep.Run(context.Background())
}

// startPurge runs the instant-purge consumer: it subscribes to this edge's NATS
// JetStream subject and applies purges to the local Nginx cache in milliseconds,
// independent of the (slower) config poll loop. With no nats_url it is a no-op
// (standalone/Phase-1 behavior: no network purge channel). It retries on
// connection failure so a NATS outage self-heals without dropping the agent.
func startPurge(base *config.Config) {
	if base.NatsURL == "" {
		log.Printf("purge: nats_url not set — instant purge disabled")
		return
	}
	cp := client.New(base.ControlPlaneURL, base.EffectiveToken(), base.EdgeID, agentVersion, "", "", "", "")
	purger := purge.NewFilePurger(base.CacheDir) // delete matching cache files
	cons := purge.NewConsumer(base.NatsURL, base.EdgeID, purger, cp.AckPurge)
	for {
		if err := cons.Run(context.Background()); err != nil {
			log.Printf("purge: consumer stopped (%v) — retrying in %s", err, minBackoff)
		}
		time.Sleep(minBackoff)
	}
}

// startLogShip tails the edge's structured JSON access log and ships entries to the
// control plane (Phase 4 Step 6). Bounded + retry like stats; never blocks serving.
func startLogShip(base *config.Config) {
	cp := client.New(base.ControlPlaneURL, base.EffectiveToken(), base.EdgeID, agentVersion, "", "", "", "")
	sh := logship.New("/var/log/nginx/brisk.requests.log", cp.ShipLogs, base.StatsIntervalDuration())
	sh.Run(context.Background())
}

func nextBackoff(b time.Duration) time.Duration {
	b *= 2
	if b > maxBackoff {
		b = maxBackoff
	}
	return b
}

// jitter returns d adjusted by a random ±20% to avoid synchronized polling.
func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return d
	}
	delta := (rand.Float64()*2 - 1) * 0.2 * float64(d)
	return d + time.Duration(delta)
}

func modeName(base *config.Config) string {
	if base.PullMode() {
		return "pull"
	}
	return "standalone"
}
