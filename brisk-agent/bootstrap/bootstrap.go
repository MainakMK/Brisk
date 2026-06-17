// Package bootstrap performs the one-time, idempotent edge install (Step 6).
//
// `brisk-agent --bootstrap` turns a fresh Ubuntu 24.04 box into a working Brisk
// edge: installs Nginx + build deps, builds the dynamic modules (headers-more,
// ngx_brotli) ABI-locked to the exact Nginx version, applies kernel tuning,
// creates dirs, installs the binary, generates config + self-signed TLS, validates
// and starts Nginx, and (where systemd is present) enables nginx + brisk-agent so
// the edge auto-recovers on reboot.
//
// Every action checks before doing, so re-running is harmless.
//
// MODULE ABI LOCK: a dynamic module is bound to the EXACT Nginx version it was
// built against. On any Nginx upgrade the modules must be rebuilt or Nginx won't
// start. Each Ensure* records the built version in a stamp file and rebuilds when
// the running Nginx version differs.
package bootstrap

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"brisk-agent/config"
	"brisk-agent/nginx"
	"brisk-agent/tls"
)

const (
	// ModulesDir is where Nginx loads dynamic modules from (load_module modules/...).
	ModulesDir = "/etc/nginx/modules"
	// SrcDir is where module sources are fetched and built.
	SrcDir = "/usr/local/src"
	// NginxConfPath is the nginx.conf the agent owns.
	NginxConfPath = "/etc/nginx/nginx.conf"
	// AgentBinPath is where the agent binary is installed.
	AgentBinPath = "/usr/local/bin/brisk-agent"
	// SysctlPath is the kernel tuning drop-in.
	SysctlPath = "/etc/sysctl.d/99-brisk.conf"
	// SystemdUnitPath is the agent's systemd unit.
	SystemdUnitPath = "/etc/systemd/system/brisk-agent.service"

	HeadersMoreSO   = "ngx_http_headers_more_filter_module.so"
	HeadersMoreRepo = "https://github.com/openresty/headers-more-nginx-module.git"
	headersMoreDir  = "headers-more-nginx-module"
	headersMoreStmp = ".brisk-headers-more.version"

	BrotliRepo  = "https://github.com/google/ngx_brotli.git"
	brotliDir   = "ngx_brotli"
	brotliStamp = ".brisk-brotli.version"

	// Lua programmable edge (Phase 4 Step 5): NDK + lua-nginx-module (LuaJIT) as
	// dynamic modules + the lua-resty-core/lrucache libs. ABI-locked to the nginx
	// version like the others. BriskLuaDir holds the resty libs (lua_package_path).
	LuaSO       = "ngx_http_lua_module.so"
	luaStamp    = ".brisk-lua.version"
	BriskLuaDir = "/usr/local/brisk-lua"
	luaJITDir   = "/usr/local/luajit"

	// GeoIP (Phase 4 Step 6 Part 5): ngx_http_geoip2 dynamic module + the GeoLite2
	// country DB. The agent renders the geoip2 block + $brisk_country ONLY when BOTH
	// the module .so and the mmdb are present (geoipAvailable() in nginx.go) — so an
	// edge without GeoIP serves byte-identically (country "-"). The mmdb needs a
	// (free) MaxMind licence; supply BRISK_GEOIP_LICENSE_KEY to auto-download, or
	// drop the file at GeoIPDBPath out of band. Module build is licence-free.
	GeoIPSO     = "ngx_http_geoip2_module.so"
	GeoIP2Repo  = "https://github.com/leev/ngx_http_geoip2_module.git"
	geoip2Dir   = "ngx_http_geoip2_module"
	geoipStamp  = ".brisk-geoip2.version"
	GeoIPDir    = "/etc/brisk/geoip"
	GeoIPDBPath = "/etc/brisk/geoip/GeoLite2-Country.mmdb"
)

// Run executes the idempotent install. configPath is the agent.yaml to use
// (created with a placeholder if missing).
func Run(configPath string) error {
	if os.Geteuid() != 0 {
		log.Printf("bootstrap: WARNING not running as root — package install, sysctl, and systemd steps will likely fail")
	}

	if err := installNginx(); err != nil {
		return err
	}
	if err := EnsureHeadersMore(); err != nil {
		return err
	}
	if err := EnsureBrotli(); err != nil {
		return err
	}
	if err := EnsureLua(); err != nil {
		return err
	}
	if err := EnsureGeoIP(); err != nil {
		return err
	}
	if err := WriteSysctl(); err != nil {
		return err
	}
	if err := createDirs(); err != nil {
		return err
	}
	if err := installBinary(); err != nil {
		return err
	}
	cfg, err := ensureConfig(configPath)
	if err != nil {
		return err
	}
	if err := generateAndStart(cfg, configPath); err != nil {
		return err
	}
	if err := installSystemd(); err != nil {
		return err
	}
	return nil
}

// --- step 1: Nginx + build deps -------------------------------------------

// installNginx installs Nginx (official nginx.org stable, with a distro
// fallback) plus build dependencies. Skips the package install if Nginx is
// already present.
func installNginx() error {
	if _, err := DetectNginxVersion(); err == nil {
		log.Printf("bootstrap: nginx already installed — skipping package install")
	} else {
		log.Printf("bootstrap: installing nginx (trying official nginx.org repo)")
		// Try the official nginx.org repo; fall back to the distro package.
		addRepo := `set -e
export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get install -y --no-install-recommends ca-certificates curl gnupg
install -d /usr/share/keyrings
rm -f /usr/share/keyrings/nginx-archive-keyring.gpg
curl -fsSL https://nginx.org/keys/nginx_signing.key | gpg --dearmor -o /usr/share/keyrings/nginx-archive-keyring.gpg
. /etc/os-release
echo "deb [signed-by=/usr/share/keyrings/nginx-archive-keyring.gpg] http://nginx.org/packages/ubuntu ${VERSION_CODENAME} nginx" > /etc/apt/sources.list.d/nginx.list
apt-get update
apt-get install -y -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold nginx`
		if err := sh("add nginx.org repo + install", addRepo); err != nil {
			log.Printf("bootstrap: nginx.org repo failed (%v) — falling back to distro nginx", err)
			// Remove the (broken/partial) nginx.org source so it doesn't poison
			// apt-get update, then install the distro package.
			fallback := "rm -f /etc/apt/sources.list.d/nginx.list; " +
				"export DEBIAN_FRONTEND=noninteractive; apt-get update && " +
				"apt-get install -y -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold nginx"
			if err := sh("install distro nginx", fallback); err != nil {
				return err
			}
		}
	}
	// Build deps for the dynamic modules (PCRE2 on Ubuntu 24.04 / Debian trixie).
	deps := "export DEBIAN_FRONTEND=noninteractive; apt-get update && " +
		"apt-get install -y --no-install-recommends build-essential cmake wget tar git ca-certificates " +
		"libpcre2-dev zlib1g-dev libssl-dev"
	return sh("install build deps", deps)
}

// --- step 2: dynamic modules ----------------------------------------------

var nginxVerRe = regexp.MustCompile(`(\d+\.\d+\.\d+)`)

// DetectNginxVersion returns the installed Nginx version (e.g. "1.30.2").
func DetectNginxVersion() (string, error) {
	out, err := exec.Command("nginx", "-v").CombinedOutput() // prints to stderr
	if err != nil {
		return "", fmt.Errorf("nginx -v: %w\n%s", err, out)
	}
	m := nginxVerRe.FindString(string(out))
	if m == "" {
		return "", fmt.Errorf("could not parse nginx version from %q", string(out))
	}
	return m, nil
}

// DetectOSPretty returns the OS pretty name from /etc/os-release
// (e.g. "Ubuntu 24.04 LTS"). Best-effort; quasi-static tech info for the dashboard.
func DetectOSPretty() (string, error) {
	b, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return "", fmt.Errorf("read /etc/os-release: %w", err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "PRETTY_NAME="); ok {
			return strings.Trim(v, `"`), nil
		}
	}
	return "", fmt.Errorf("PRETTY_NAME not found in /etc/os-release")
}

// DetectKernel returns the running kernel release (e.g. "6.8.0-31-generic")
// via `uname -r`. Best-effort; quasi-static tech info for the dashboard.
func DetectKernel() (string, error) {
	out, err := exec.Command("uname", "-r").Output()
	if err != nil {
		return "", fmt.Errorf("uname -r: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// fetchNginxSource downloads + extracts the matching Nginx source if absent.
func fetchNginxSource(ver string) (string, error) {
	if err := os.MkdirAll(SrcDir, 0o755); err != nil {
		return "", fmt.Errorf("create %s: %w", SrcDir, err)
	}
	nginxSrc := filepath.Join(SrcDir, "nginx-"+ver)
	script := fmt.Sprintf("cd %s && [ -d %q ] || { wget -q https://nginx.org/download/nginx-%s.tar.gz && tar xzf nginx-%s.tar.gz; }",
		SrcDir, nginxSrc, ver, ver)
	if err := sh("fetch nginx source "+ver, script); err != nil {
		return "", err
	}
	return nginxSrc, nil
}

// EnsureHeadersMore builds + installs the headers-more module for the running
// Nginx version. Idempotent via the version stamp.
func EnsureHeadersMore() error {
	return ensureModule(moduleSpec{
		name:    "headers-more",
		repo:    HeadersMoreRepo,
		repoDir: headersMoreDir,
		clone:   "git clone --depth=1 %s %q",
		soGlob:  HeadersMoreSO,
		stamp:   headersMoreStmp,
		check:   filepath.Join(ModulesDir, HeadersMoreSO),
	})
}

// EnsureBrotli builds + installs both ngx_brotli modules. Idempotent via the stamp.
// ngx_brotli bundles the Brotli library as a submodule; its static libs must be
// compiled (cmake) before `make modules`, or the link fails on -lbrotlienc.
func EnsureBrotli() error {
	brotliDeps := filepath.Join(SrcDir, brotliDir, "deps", "brotli")
	return ensureModule(moduleSpec{
		name:    "ngx_brotli",
		repo:    BrotliRepo,
		repoDir: brotliDir,
		clone:   "git clone --depth=1 --recurse-submodules %s %q",
		preBuild: fmt.Sprintf("cd %q && mkdir -p out && cd out && "+
			"cmake -DCMAKE_BUILD_TYPE=Release -DBUILD_SHARED_LIBS=OFF -DCMAKE_C_FLAGS=-fPIC .. && "+
			"cmake --build . --config Release -j\"$(nproc)\"", brotliDeps),
		soGlob: "ngx_http_brotli_*.so",
		stamp:  brotliStamp,
		check:  filepath.Join(ModulesDir, "ngx_http_brotli_filter_module.so"),
	})
}

// EnsureLua builds + installs the Lua programmable-edge stack (Phase 4 Step 5):
// LuaJIT (OpenResty's luajit2 fork) + NDK + lua-nginx-module as dynamic modules,
// plus the lua-resty-core/lrucache libs into BriskLuaDir. ABI-locked to the running
// nginx version; idempotent via the version stamp. The agent only renders the Lua
// directives once these are present (luaAvailable() in nginx.go), so this is the
// gated one-edge-at-a-time enabler — until it runs, the edge serves byte-identically.
func EnsureLua() error {
	ver, err := DetectNginxVersion()
	if err != nil {
		return err
	}
	if upToDate(filepath.Join(ModulesDir, LuaSO), filepath.Join(ModulesDir, luaStamp), ver) {
		log.Printf("bootstrap: lua-nginx-module already built for nginx %s — skipping", ver)
		return nil
	}
	log.Printf("bootstrap: building lua-nginx-module (LuaJIT + NDK) for nginx %s", ver)

	nginxSrc, err := fetchNginxSource(ver)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(ModulesDir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", ModulesDir, err)
	}
	luajit := filepath.Join(SrcDir, "luajit2")
	ndk := filepath.Join(SrcDir, "ngx_devel_kit")
	luaMod := filepath.Join(SrcDir, "lua-nginx-module")
	restyCore := filepath.Join(SrcDir, "lua-resty-core")
	restyLru := filepath.Join(SrcDir, "lua-resty-lrucache")
	steps := []string{
		fmt.Sprintf("[ -d %q ] || git clone --depth=1 https://github.com/openresty/luajit2.git %q", luajit, luajit),
		fmt.Sprintf("[ -f %q/lib/libluajit-5.1.so.2 ] || { make -C %q -j\"$(nproc)\" && make -C %q install PREFIX=%q; }",
			luaJITDir, luajit, luajit, luaJITDir),
		fmt.Sprintf("[ -d %q ] || git clone --depth=1 https://github.com/vision5/ngx_devel_kit.git %q", ndk, ndk),
		fmt.Sprintf("[ -d %q ] || git clone --depth=1 https://github.com/openresty/lua-nginx-module.git %q", luaMod, luaMod),
		fmt.Sprintf("[ -d %q ] || git clone --depth=1 https://github.com/openresty/lua-resty-core.git %q", restyCore, restyCore),
		fmt.Sprintf("[ -d %q ] || git clone --depth=1 https://github.com/openresty/lua-resty-lrucache.git %q", restyLru, restyLru),
		fmt.Sprintf("cd %q && LUAJIT_LIB=%q/lib LUAJIT_INC=%q/include/luajit-2.1 "+
			"./configure --with-compat --add-dynamic-module=%q --add-dynamic-module=%q",
			nginxSrc, luaJITDir, luaJITDir, ndk, luaMod),
		fmt.Sprintf("cd %q && make modules", nginxSrc),
		fmt.Sprintf("cp %q/objs/ndk_http_module.so %q/objs/%s %q/", nginxSrc, nginxSrc, LuaSO, ModulesDir),
		fmt.Sprintf("mkdir -p %q && cp -r %q/lib/. %q/ && cp -r %q/lib/. %q/",
			BriskLuaDir, restyCore, BriskLuaDir, restyLru, BriskLuaDir),
		fmt.Sprintf("echo %q/lib > /etc/ld.so.conf.d/brisk-luajit.conf && ldconfig", luaJITDir),
	}
	for _, st := range steps {
		if err := sh("lua build", st); err != nil {
			return err
		}
	}
	return os.WriteFile(filepath.Join(ModulesDir, luaStamp), []byte(ver+"\n"), 0o644)
}

// EnsureGeoIP builds + installs the ngx_http_geoip2 dynamic module (Phase 4 Step 6
// Part 5) and, when a MaxMind licence key is supplied, downloads the GeoLite2 country
// DB. Idempotent via the module stamp + the DB file check. Like the other modules
// this is gated: the agent renders GeoIP directives only when BOTH the .so and the
// mmdb exist, so until both are present the edge serves byte-identically (country "-").
func EnsureGeoIP() error {
	// libmaxminddb is required to build AND to load the module at runtime.
	geoipModule := moduleSpec{
		name:     "ngx_http_geoip2",
		repo:     GeoIP2Repo,
		repoDir:  geoip2Dir,
		clone:    "git clone --depth=1 %s %q",
		preBuild: "export DEBIAN_FRONTEND=noninteractive; apt-get update && apt-get install -y --no-install-recommends libmaxminddb-dev libmaxminddb0",
		soGlob:   GeoIPSO,
		stamp:    geoipStamp,
		check:    filepath.Join(ModulesDir, GeoIPSO),
	}
	if err := ensureModule(geoipModule); err != nil {
		return err
	}
	if err := os.MkdirAll(GeoIPDir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", GeoIPDir, err)
	}
	// DB: present already => done. Else auto-download only if a licence key is set.
	if _, err := os.Stat(GeoIPDBPath); err == nil {
		log.Printf("bootstrap: GeoLite2 DB already present at %s", GeoIPDBPath)
		return nil
	}
	key := os.Getenv("BRISK_GEOIP_LICENSE_KEY")
	if key == "" {
		log.Printf("bootstrap: ngx_http_geoip2 built; GeoLite2 DB absent and BRISK_GEOIP_LICENSE_KEY unset "+
			"— country stays \"-\" until %s is supplied (see Control_Plane_Ops)", GeoIPDBPath)
		return nil
	}
	// MaxMind permalink download (tar.gz) -> extract the .mmdb to GeoIPDBPath.
	dl := fmt.Sprintf(
		"set -e; tmp=$(mktemp -d); "+
			"curl -fsSL -o \"$tmp/db.tgz\" "+
			"\"https://download.maxmind.com/app/geoip_download?edition_id=GeoLite2-Country&license_key=%s&suffix=tar.gz\"; "+
			"tar -xzf \"$tmp/db.tgz\" -C \"$tmp\"; "+
			"f=$(find \"$tmp\" -name 'GeoLite2-Country.mmdb' | head -n1); "+
			"install -m 0644 \"$f\" %q; rm -rf \"$tmp\"",
		key, GeoIPDBPath)
	if err := sh("geoip db download", dl); err != nil {
		// Non-fatal: the module is built; without the DB the edge just uses "-".
		log.Printf("bootstrap: GeoLite2 DB download failed (country stays \"-\"): %v", err)
	}
	return nil
}

type moduleSpec struct {
	name     string
	repo     string
	repoDir  string
	clone    string // printf with (repo, destDir)
	preBuild string // optional shell run after clone, before ./configure
	soGlob   string // .so name or glob to copy from objs/
	stamp    string // stamp filename in ModulesDir
	check    string // a .so path whose presence (with matching stamp) means up-to-date
}

func ensureModule(s moduleSpec) error {
	ver, err := DetectNginxVersion()
	if err != nil {
		return err
	}
	if upToDate(s.check, filepath.Join(ModulesDir, s.stamp), ver) {
		log.Printf("bootstrap: %s already built for nginx %s — skipping", s.name, ver)
		return nil
	}
	log.Printf("bootstrap: building %s for nginx %s", s.name, ver)

	nginxSrc, err := fetchNginxSource(ver)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(ModulesDir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", ModulesDir, err)
	}
	moduleSrc := filepath.Join(SrcDir, s.repoDir)

	steps := []string{
		fmt.Sprintf("[ -d %q ] || "+s.clone, moduleSrc, s.repo, moduleSrc),
	}
	if s.preBuild != "" {
		steps = append(steps, s.preBuild)
	}
	steps = append(steps,
		fmt.Sprintf("cd %q && ./configure --with-compat --add-dynamic-module=%q", nginxSrc, moduleSrc),
		fmt.Sprintf("cd %q && make modules", nginxSrc),
		fmt.Sprintf("cp %s %q/", filepath.Join(nginxSrc, "objs", s.soGlob), ModulesDir),
	)
	for _, st := range steps {
		if err := sh(s.name+" build", st); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(ModulesDir, s.stamp), []byte(ver+"\n"), 0o644); err != nil {
		return fmt.Errorf("write %s stamp: %w", s.name, err)
	}
	return nil
}

// upToDate reports whether soPath exists and stampPath records ver.
func upToDate(soPath, stampPath, ver string) bool {
	if _, err := os.Stat(soPath); err != nil {
		return false
	}
	stamp, err := os.ReadFile(stampPath)
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(stamp)) == ver
}

// --- step 3: kernel tuning -------------------------------------------------

const sysctlContent = `# Brisk edge tuning — managed by brisk-agent bootstrap.
# congestion control (Google BBR) + fair queueing
net.core.default_qdisc            = fq
net.ipv4.tcp_congestion_control   = bbr
# big autotuning buffers for 10 Gbps / high-bandwidth paths
net.core.rmem_max                 = 33554432
net.core.wmem_max                 = 33554432
net.ipv4.tcp_rmem                 = 4096 131072 33554432
net.ipv4.tcp_wmem                 = 4096 131072 33554432
# queues / backlogs for bursty connection storms
net.core.somaxconn                = 65535
net.ipv4.tcp_max_syn_backlog      = 65536
net.core.netdev_max_backlog       = 250000
# connection churn + fast open
net.ipv4.tcp_fastopen             = 3
net.ipv4.tcp_fin_timeout          = 15
net.ipv4.tcp_tw_reuse             = 1
net.ipv4.ip_local_port_range      = 1024 65000
net.ipv4.tcp_slow_start_after_idle= 0
fs.file-max                       = 2000000
`

// WriteSysctl writes the tuning drop-in and applies it. Applying BBR fails inside
// plain Docker (host kernel governs congestion control) — that is logged, not fatal
// (validate BBR on the VPS/WSL2).
func WriteSysctl() error {
	if err := os.WriteFile(SysctlPath, []byte(sysctlContent), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", SysctlPath, err)
	}
	if err := sh("apply sysctl", "sysctl --system >/dev/null 2>&1"); err != nil {
		log.Printf("bootstrap: sysctl --system did not fully apply (%v) — expected inside Docker; validate BBR on VPS/WSL2", err)
	}
	return nil
}

// --- step 4: dirs ----------------------------------------------------------

func createDirs() error {
	for _, d := range []string{"/var/cache/brisk", "/etc/brisk/tls", "/var/log/nginx", "/etc/brisk", tls.DefaultWebrootDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", d, err)
		}
	}
	return nil
}

// --- step 5: install binary ------------------------------------------------

func installBinary() error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate self: %w", err)
	}
	if abs, _ := filepath.EvalSymlinks(self); abs == AgentBinPath {
		log.Printf("bootstrap: agent already installed at %s", AgentBinPath)
		return nil
	}
	if err := copyFile(self, AgentBinPath, 0o755); err != nil {
		return fmt.Errorf("install binary: %w", err)
	}
	log.Printf("bootstrap: installed agent to %s", AgentBinPath)
	return nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// --- step 6: config + TLS + start -----------------------------------------

// ensureConfig loads the config, writing a placeholder if none exists.
func ensureConfig(path string) (*config.Config, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		log.Printf("bootstrap: no config at %s — writing a placeholder (edit it for real zones)", path)
		def := &config.Config{
			EdgeID:   "EDGE-01",
			Mode:     config.ModeLocal,
			CacheDir: config.DefaultCacheDir,
			TLSMode:  config.TLSModeSelfSigned,
			Zones: []config.Zone{{
				Domain: "localhost",
				Origin: "http://127.0.0.1:8080",
				TLS:    config.TLSModeSelfSigned,
			}},
		}
		if err := def.Save(path); err != nil {
			return nil, err
		}
	}
	return config.Load(path)
}

// generateAndStart provisions TLS (two-phase for Let's Encrypt), renders
// nginx.conf, validates, and starts Nginx.
func generateAndStart(cfg *config.Config, configPath string) error {
	mgr, err := nginx.NewManager("nginx", NginxConfPath)
	if err != nil {
		return err
	}
	tlsMgr := tls.NewManager("", cfg.LetsEncryptEmail, cfg.LetsEncryptStaging, "")
	if err := mgr.ApplyWithTLS(cfg, tlsMgr); err != nil {
		return err
	}
	log.Printf("bootstrap: nginx.conf generated and validated; nginx serving")
	return nil
}

// --- step 7: systemd -------------------------------------------------------

const systemdUnit = `[Unit]
Description=Brisk Edge Agent
After=network-online.target nginx.service
Wants=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/bin/brisk-agent --config /etc/brisk/agent.yaml
Restart=always
RestartSec=3
LimitNOFILE=1000000

[Install]
WantedBy=multi-user.target
`

// installSystemd installs + enables the agent and nginx units so the edge
// auto-starts on boot. If systemd is not the init system (e.g. plain Docker),
// it logs guidance instead of failing.
func installSystemd() error {
	if !systemdAvailable() {
		log.Printf("bootstrap: systemd not detected (e.g. plain Docker) — skipping service enable. " +
			"Nginx was started directly. Test reboot survival on WSL2 (systemd=true) or the VPS.")
		return nil
	}
	if err := os.WriteFile(SystemdUnitPath, []byte(systemdUnit), 0o644); err != nil {
		return fmt.Errorf("write systemd unit: %w", err)
	}
	if err := sh("systemctl daemon-reload", "systemctl daemon-reload"); err != nil {
		return err
	}
	if err := sh("enable nginx", "systemctl enable --now nginx"); err != nil {
		log.Printf("bootstrap: could not enable nginx service: %v", err)
	}
	if err := sh("enable brisk-agent", "systemctl enable --now brisk-agent"); err != nil {
		return err
	}
	log.Printf("bootstrap: systemd units enabled (nginx + brisk-agent) — edge will auto-start on boot")
	return nil
}

func systemdAvailable() bool {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return false
	}
	// systemd as PID 1 exposes /run/systemd/system.
	if _, err := os.Stat("/run/systemd/system"); err != nil {
		return false
	}
	return true
}

// --- shell helper ----------------------------------------------------------

func run(desc, name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w\n%s", desc, err, out)
	}
	return nil
}

func sh(desc, script string) error { return run(desc, "sh", "-c", script) }
