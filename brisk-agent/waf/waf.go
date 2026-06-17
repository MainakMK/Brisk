package waf

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"brisk-agent/config"

	"github.com/corazawaf/coraza/v3"
	"github.com/corazawaf/coraza/v3/types"
)

// Engine holds the compiled per-zone WAFs and routes each request to its zone.
// It is reloaded on every config apply; unchanged zones keep their (expensive to
// compile) CRS engine via a config fingerprint, so an unrelated config change
// (a cert renewal, a different zone) never recompiles a zone's ruleset.
type Engine struct {
	mu    sync.RWMutex
	zones map[string]*zoneWAF // keyed by served host (lowercased)
	buf   *EventBuffer        // nil = standalone (events only logged, not shipped)
}

// zoneWAF is one zone's compiled protection.
type zoneWAF struct {
	host        string
	mode        string // detect | block
	failOpen    bool
	managed     bool       // owasp_crs enabled
	waf         coraza.WAF // compiled CRS (nil when managed off)
	rules       []compiledRule
	fingerprint string
}

// compiledRule is a custom rule with its regex/CIDR pre-compiled.
type compiledRule struct {
	ruleID     string // custom:<id> or wp:<name>
	field      string // ip|country|path|method|header|user_agent
	op         string // eq|prefix|regex|cidr
	value      string
	headerName string
	action     string // block|challenge|log|allow
	re         *regexp.Regexp
	cidr       *net.IPNet
}

// InspectReq is one request to evaluate (built from the nginx auth_request subrequest).
type InspectReq struct {
	Host      string
	Method    string
	URI       string // path + query (raw $request_uri)
	ClientIP  string
	UserAgent string
	Country   string // from a geo source if present (else "")
	Header    http.Header
}

// Decision is the WAF verdict. Block => nginx returns 403.
type Decision struct {
	Block bool
}

// NewEngine returns an empty engine. buf may be nil (standalone: events logged only).
func NewEngine(buf *EventBuffer) *Engine {
	return &Engine{zones: map[string]*zoneWAF{}, buf: buf}
}

// Protecting reports how many zones currently have WAF compiled (for /healthz).
func (e *Engine) Protecting() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.zones)
}

// Reload rebuilds the per-zone WAF map from the applied config. WAF-disabled zones
// are dropped (no inspection). Compilation errors leave that zone unprotected but
// never crash the agent (a broken ruleset must not blackhole a tenant).
func (e *Engine) Reload(zones []config.Zone) {
	e.mu.RLock()
	old := e.zones
	e.mu.RUnlock()

	next := make(map[string]*zoneWAF)
	enabled := 0
	for _, z := range zones {
		if z.WAF == nil || !z.WAF.Enabled {
			continue
		}
		enabled++
		host := strings.ToLower(z.Domain)
		fp := fingerprint(z.WAF)
		if prev := old[host]; prev != nil && prev.fingerprint == fp {
			next[host] = prev // unchanged -> reuse the compiled CRS engine
			continue
		}
		zw, err := buildZoneWAF(host, z.WAF)
		if err != nil {
			log.Printf("waf: zone=%s compile failed, leaving WAF OFF for it: %v", host, err)
			continue
		}
		next[host] = zw
	}

	e.mu.Lock()
	e.zones = next
	e.mu.Unlock()
	if enabled > 0 {
		log.Printf("waf: reloaded — %d zone(s) protected", len(next))
	}
}

// Inspect evaluates a request: custom rules first (terminating short-circuit),
// then the managed CRS. In detect mode a would-block returns allow + a logged
// "detect" event; in block mode it returns Block. Rate limiting is Nginx-native
// and runs before this (documented in Control_Plane_Ops).
func (e *Engine) Inspect(req InspectReq) Decision {
	e.mu.RLock()
	zw := e.zones[strings.ToLower(req.Host)]
	e.mu.RUnlock()
	if zw == nil {
		return Decision{} // not configured here -> allow (nginx only calls us when on)
	}
	path := pathOnly(req.URI)

	// 1) custom rules (ordered). A terminating action short-circuits.
	for _, cr := range zw.rules {
		if !cr.matches(req, path) {
			continue
		}
		switch cr.action {
		case "allow":
			return Decision{} // explicit allowlist -> skip the managed ruleset
		case "log":
			e.emit(zw, req, path, Event{RuleID: cr.ruleID, RuleType: "custom", Action: "log", Message: "custom rule matched (log)"})
			continue
		default: // block | challenge
			return e.decide(zw, req, path, "custom", cr.ruleID, cr.action, "custom rule matched")
		}
	}

	// 2) managed CRS (OWASP). Coraza decides; we apply mode + fail policy.
	if zw.managed && zw.waf != nil {
		it, err := corazaInspect(zw.waf, req)
		if err != nil {
			if zw.failOpen {
				log.Printf("waf: zone=%s engine error (FAIL-OPEN, allowing): %v", zw.host, err)
				e.emit(zw, req, path, Event{RuleID: "engine_error", RuleType: "managed", Action: "log", Message: "waf engine error: " + err.Error()})
				return Decision{}
			}
			log.Printf("waf: zone=%s engine error (FAIL-CLOSED, blocking): %v", zw.host, err)
			e.emit(zw, req, path, Event{RuleID: "engine_error", RuleType: "managed", Action: "block", Message: "waf engine error: " + err.Error()})
			return Decision{Block: true}
		}
		if it != nil {
			return e.decide(zw, req, path, "managed", "crs:"+strconv.Itoa(it.RuleID), "block", crsMessage(it))
		}
	}
	return Decision{}
}

// decide applies the zone mode to a matched terminating rule: block mode enforces
// (403); detect mode logs the would-block but allows the request.
func (e *Engine) decide(zw *zoneWAF, req InspectReq, path, rtype, ruleID, action, msg string) Decision {
	ev := Event{RuleID: ruleID, RuleType: rtype, Message: msg}
	if zw.mode == "block" {
		ev.Action = action // block | challenge
		e.emit(zw, req, path, ev)
		return Decision{Block: true}
	}
	ev.Action = "detect" // would-block, not enforced (tuning view)
	e.emit(zw, req, path, ev)
	return Decision{}
}

// emit finalizes + records a security event (buffer for shipping + a concise log
// line so the standalone lab can observe blocks/would-blocks via agent logs).
func (e *Engine) emit(zw *zoneWAF, req InspectReq, path string, ev Event) {
	ev.TS = time.Now().UTC()
	ev.Zone = zw.host
	ev.Mode = zw.mode
	ev.ClientIP = req.ClientIP
	ev.Country = req.Country
	ev.Path = path
	ev.Method = req.Method
	ev.UA = req.UserAgent
	if e.buf != nil {
		e.buf.Add(ev)
	}
	log.Printf("waf: zone=%s action=%s type=%s rule=%s ip=%s %s %s",
		zw.host, ev.Action, ev.RuleType, ev.RuleID, ev.ClientIP, ev.Method, path)
}

// --- compilation ---

func buildZoneWAF(host string, zw *config.ZoneWAF) (*zoneWAF, error) {
	z := &zoneWAF{
		host:        host,
		mode:        modeOr(zw.Mode),
		failOpen:    zw.FailOpen,
		managed:     strings.EqualFold(zw.ManagedRuleset, "owasp_crs"),
		fingerprint: fingerprint(zw),
	}
	if z.managed {
		w, err := compileCoraza(zw.Paranoia)
		if err != nil {
			return nil, err
		}
		z.waf = w
	}
	// WordPress preset rules run first (highest priority): block /xmlrpc.php +
	// known scanner user-agents. The /wp-login.php rate limit is Nginx-native
	// (rendered by the agent's nginx template), not here.
	if zw.WpPreset {
		z.rules = append(z.rules, wpPresetRules()...)
	}
	sorted := append([]config.ZoneWAFRule(nil), zw.Rules...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Priority < sorted[j].Priority })
	for _, cr := range sorted {
		if !cr.Enabled {
			continue
		}
		c, err := compileRule(cr)
		if err != nil {
			log.Printf("waf: zone=%s skipping bad rule %d (%s %s): %v", host, cr.ID, cr.Field, cr.Op, err)
			continue
		}
		z.rules = append(z.rules, c)
	}
	return z, nil
}

func compileRule(cr config.ZoneWAFRule) (compiledRule, error) {
	c := compiledRule{
		ruleID:     "custom:" + strconv.FormatInt(cr.ID, 10),
		field:      cr.Field,
		op:         cr.Op,
		value:      cr.Value,
		headerName: cr.HeaderName,
		action:     cr.Action,
	}
	switch cr.Op {
	case "regex":
		re, err := regexp.Compile(cr.Value)
		if err != nil {
			return c, err
		}
		c.re = re
	case "cidr":
		_, ipnet, err := net.ParseCIDR(cr.Value)
		if err != nil {
			return c, err
		}
		c.cidr = ipnet
	}
	return c, nil
}

// wpPresetRules are the WordPress hardening custom rules (block /xmlrpc.php + known
// scanner UAs). Kept narrow so legitimate clients (incl. curl in tests) pass.
func wpPresetRules() []compiledRule {
	badUA := regexp.MustCompile(`(?i)(sqlmap|nikto|nmap|masscan|wpscan|nessus|fimap|acunetix|whatweb|libwww-perl)`)
	return []compiledRule{
		{ruleID: "wp:xmlrpc", field: "path", op: "eq", value: "/xmlrpc.php", action: "block"},
		{ruleID: "wp:bad_ua", field: "user_agent", op: "regex", value: badUA.String(), action: "block", re: badUA},
	}
}

// --- matching ---

func (r compiledRule) matches(req InspectReq, path string) bool {
	switch r.field {
	case "ip":
		return r.matchIP(req.ClientIP)
	case "country":
		if req.Country == "" {
			return false // no geo source -> never matches (documented)
		}
		return r.matchStr(req.Country)
	case "path":
		return r.matchStr(path)
	case "method":
		return r.matchStr(req.Method)
	case "user_agent":
		return r.matchStr(req.UserAgent)
	case "header":
		return r.matchStr(req.Header.Get(r.headerName))
	}
	return false
}

func (r compiledRule) matchStr(s string) bool {
	switch r.op {
	case "eq":
		return strings.EqualFold(s, r.value)
	case "prefix":
		return strings.HasPrefix(s, r.value)
	case "regex":
		return r.re != nil && r.re.MatchString(s)
	}
	return false
}

func (r compiledRule) matchIP(ip string) bool {
	switch r.op {
	case "cidr":
		if r.cidr == nil {
			return false
		}
		p := net.ParseIP(ip)
		return p != nil && r.cidr.Contains(p)
	case "eq":
		return ip == r.value
	case "prefix":
		return strings.HasPrefix(ip, r.value)
	case "regex":
		return r.re != nil && r.re.MatchString(ip)
	}
	return false
}

// --- coraza glue ---

// corazaInspect runs the request through Coraza (headers only; body is not
// forwarded — the body-inspect cap). Wrapped in recover so an engine panic is
// reported as an error (the caller applies the fail-open/closed policy).
func corazaInspect(waf coraza.WAF, req InspectReq) (it *types.Interruption, err error) {
	defer func() {
		if r := recover(); r != nil {
			it, err = nil, fmt.Errorf("coraza panic: %v", r)
		}
	}()
	tx := waf.NewTransaction()
	defer func() {
		tx.ProcessLogging()
		_ = tx.Close()
	}()

	ip := req.ClientIP
	if ip == "" {
		ip = "0.0.0.0"
	}
	tx.ProcessConnection(ip, 0, "0.0.0.0", 0)
	uri := req.URI
	if uri == "" {
		uri = "/"
	}
	method := req.Method
	if method == "" {
		method = "GET"
	}
	tx.ProcessURI(uri, method, "HTTP/1.1")
	if req.Host != "" {
		tx.AddRequestHeader("Host", req.Host)
	}
	for k, vs := range req.Header {
		if skipHeader(k) {
			continue
		}
		for _, v := range vs {
			tx.AddRequestHeader(k, v)
		}
	}
	if itx := tx.ProcessRequestHeaders(); itx != nil {
		return itx, nil
	}
	itx, berr := tx.ProcessRequestBody()
	if berr != nil {
		return nil, berr
	}
	if itx != nil {
		return itx, nil
	}
	return tx.Interruption(), nil
}

// skipHeader drops the WAF control headers (added by the auth_request location) and
// hop-by-hop headers so Coraza inspects only the genuine client headers.
func skipHeader(k string) bool {
	switch strings.ToLower(k) {
	case "host", "content-length", "connection",
		"x-brisk-waf-zone", "x-brisk-waf-uri", "x-brisk-waf-method", "x-brisk-waf-ip", "x-brisk-waf-country":
		return true
	}
	return false
}

func crsMessage(it *types.Interruption) string {
	msg := fmt.Sprintf("CRS rule %d", it.RuleID)
	if it.Data != "" {
		msg += ": " + it.Data
	}
	return msg
}

// --- helpers ---

func modeOr(m string) string {
	if strings.EqualFold(m, "block") {
		return "block"
	}
	return "detect"
}

func pathOnly(uri string) string {
	if i := strings.IndexByte(uri, '?'); i >= 0 {
		return uri[:i]
	}
	return uri
}

// fingerprint hashes the engine-relevant WAF config (not rate limits — those are
// Nginx-native) so Reload can skip recompiling an unchanged zone's CRS.
func fingerprint(zw *config.ZoneWAF) string {
	b, _ := json.Marshal(struct {
		Mode, MR string
		P        int
		FO, WP   bool
		R        []config.ZoneWAFRule
	}{zw.Mode, zw.ManagedRuleset, zw.Paranoia, zw.FailOpen, zw.WpPreset, zw.Rules})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
