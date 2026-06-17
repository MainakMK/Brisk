package waf

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"
)

// Event is one firewall-log entry. JSON tags match the control plane's
// /agent/security-events ingest (zone is the served host -> resolved to zone_id).
type Event struct {
	TS       time.Time `json:"ts"`
	Zone     string    `json:"zone"`
	ClientIP string    `json:"client_ip"`
	Country  string    `json:"country"`
	RuleID   string    `json:"rule_id"`
	RuleType string    `json:"rule_type"` // managed | custom | ratelimit
	Action   string    `json:"action"`    // block | detect | log | challenge
	Mode     string    `json:"mode"`      // detect | block
	Path     string    `json:"path"`
	Method   string    `json:"method"`
	UA       string    `json:"ua"`
	Message  string    `json:"message"`
}

// ShipFunc ships a JSON array of events to the control plane (decoupled from the
// client package to avoid an import cycle, like stats.ShipFunc).
type ShipFunc func(ctx context.Context, body []byte) error

// maxBufferedEvents bounds the in-memory firewall-log buffer. Under a flood the
// oldest are dropped (drop-oldest) so a down control plane never makes the agent
// grow unbounded or slow request serving. Same discipline as the stats reporter.
const maxBufferedEvents = 5000

// EventBuffer is a bounded, drop-oldest buffer of security events with a periodic
// shipping loop. ship==nil (standalone/lab) => events are only logged by the engine.
type EventBuffer struct {
	mu       sync.Mutex
	buf      []Event
	max      int
	ship     ShipFunc
	interval time.Duration
}

// NewEventBuffer wires a ship function on the given interval. ship may be nil.
func NewEventBuffer(ship ShipFunc, interval time.Duration) *EventBuffer {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	return &EventBuffer{max: maxBufferedEvents, ship: ship, interval: interval}
}

// Add appends an event, dropping the oldest if the buffer is full.
func (b *EventBuffer) Add(ev Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, ev)
	if len(b.buf) > b.max {
		b.buf = b.buf[len(b.buf)-b.max:]
	}
}

// Run ships buffered events every interval until ctx is cancelled. A no-op when
// no ship func is configured (standalone).
func (b *EventBuffer) Run(ctx context.Context) {
	if b.ship == nil {
		return
	}
	t := time.NewTicker(b.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			b.flush(ctx)
		}
	}
}

func (b *EventBuffer) flush(ctx context.Context) {
	b.mu.Lock()
	if len(b.buf) == 0 {
		b.mu.Unlock()
		return
	}
	batch := b.buf
	b.buf = nil
	b.mu.Unlock()

	body, err := json.Marshal(batch)
	if err != nil {
		return // malformed batch dropped (should never happen)
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	err = b.ship(cctx, body)
	cancel()
	if err != nil {
		// Requeue at the front (preserve order), bounded to max.
		b.mu.Lock()
		b.buf = append(batch, b.buf...)
		if len(b.buf) > b.max {
			b.buf = b.buf[len(b.buf)-b.max:]
		}
		b.mu.Unlock()
		log.Printf("waf: ship security events failed (buffering %d): %v", len(batch), err)
	}
}

// --- HTTP service (nginx auth_request target) ---

// Server is the loopback HTTP service nginx auth_request calls per request.
type Server struct {
	engine *Engine
}

// NewServer returns the WAF HTTP handler: POST/GET /inspect (the auth decision)
// and GET /healthz (liveness + zones-protected count).
func NewServer(engine *Engine) http.Handler {
	s := &Server{engine: engine}
	mux := http.NewServeMux()
	mux.HandleFunc("/inspect", s.inspect)
	mux.HandleFunc("/healthz", s.healthz)
	return mux
}

// inspect maps the nginx auth_request subrequest to a WAF decision. 200 => allow,
// 403 => block (nginx returns 403 to the client). The original request line comes
// via X-Brisk-WAF-* headers (the subrequest URI is /inspect); the client headers
// (User-Agent, Cookie, etc.) are forwarded by auth_request and inspected directly.
func (s *Server) inspect(w http.ResponseWriter, r *http.Request) {
	country := r.Header.Get("X-Brisk-WAF-Country")
	if country == "-" {
		country = "" // GeoIP-off placeholder -> no country (don't match country rules)
	}
	req := InspectReq{
		Host:      r.Header.Get("X-Brisk-WAF-Zone"),
		Method:    r.Header.Get("X-Brisk-WAF-Method"),
		URI:       r.Header.Get("X-Brisk-WAF-URI"),
		ClientIP:  r.Header.Get("X-Brisk-WAF-IP"),
		UserAgent: r.Header.Get("User-Agent"),
		Country:   country,
		Header:    r.Header,
	}
	if s.engine.Inspect(req).Block {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "zones_protected": s.engine.Protecting()})
}
