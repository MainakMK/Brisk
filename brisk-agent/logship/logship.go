// Package logship tails the edge's structured JSON access log and ships entries to
// the control plane (Phase 4 Step 6). It reuses the stats shipping discipline:
// bounded, drop-oldest buffer; flush on the ticker; never block request serving;
// survive control-plane/tunnel drops (buffer + retry). Logs are high-volume, so the
// buffer is capped and the oldest are dropped under pressure.
package logship

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

// maxBuffered bounds the in-memory log buffer (~seconds of traffic). Oldest dropped
// when full so a down control plane never grows the agent unbounded.
const maxBuffered = 10000

// Entry is one shipped access-log row (matches the control plane's /agent/logs JSON).
type Entry struct {
	TS           time.Time `json:"ts"`
	Zone         string    `json:"zone"`
	ClientIP     string    `json:"client_ip"`
	Country      string    `json:"country"`
	Method       string    `json:"method"`
	Path         string    `json:"path"`
	Status       int32     `json:"status"`
	Bytes        int64     `json:"bytes"`
	CacheStatus  string    `json:"cache_status"`
	RequestTime  float64   `json:"request_time"`
	UpstreamTime float64   `json:"upstream_time"`
	Referer      string    `json:"referer"`
	UserAgent    string    `json:"user_agent"`
	RequestID    string    `json:"request_id"`
}

// nginxLine is the raw JSON the edge log_format writes (brisk_json).
type nginxLine struct {
	TS      string `json:"ts"`
	IP      string `json:"ip"`
	Country string `json:"country"`
	Method  string `json:"method"`
	Host    string `json:"host"`
	Path    string `json:"path"`
	Status  int32  `json:"status"`
	Bytes   int64  `json:"bytes"`
	Cache   string `json:"cache"`
	RT      string `json:"rt"`  // request_time (string; always present)
	URT     string `json:"urt"` // upstream_response_time (may be "" or a list)
	Ref     string `json:"ref"`
	UA      string `json:"ua"`
	RID     string `json:"rid"`
}

// ShipFunc posts a JSON array of entries to the control plane.
type ShipFunc func(ctx context.Context, body []byte) error

// Shipper tails path and ships parsed entries every interval.
type Shipper struct {
	path     string
	ship     ShipFunc
	interval time.Duration
	buf      []Entry
	offset   int64
	started  bool
}

// New returns a Shipper for the JSON access log at path.
func New(path string, ship ShipFunc, interval time.Duration) *Shipper {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	return &Shipper{path: path, ship: ship, interval: interval}
}

// Run tails + ships until ctx is cancelled.
func (s *Shipper) Run(ctx context.Context) {
	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.readNew()
			s.flush(ctx)
		}
	}
}

// readNew appends complete new lines from the log to the buffer. On the first read
// it skips to EOF (don't ship historical); on truncation/rotation it resets.
func (s *Shipper) readNew() {
	f, err := os.Open(s.path)
	if err != nil {
		return // log not created yet (no traffic) — fine
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return
	}
	if fi.Size() < s.offset {
		s.offset = 0 // truncated / rotated
	}
	if !s.started {
		s.offset = fi.Size()
		s.started = true
		return
	}
	if fi.Size() == s.offset {
		return
	}
	if _, err := f.Seek(s.offset, io.SeekStart); err != nil {
		return
	}
	data := make([]byte, fi.Size()-s.offset)
	n, _ := io.ReadFull(f, data)
	data = data[:n]
	last := bytes.LastIndexByte(data, '\n')
	if last < 0 {
		return // no complete line yet
	}
	for _, line := range bytes.Split(data[:last], []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		if e, ok := parseLine(line); ok {
			s.buf = append(s.buf, e)
			if len(s.buf) > maxBuffered {
				s.buf = s.buf[len(s.buf)-maxBuffered:]
			}
		}
	}
	s.offset += int64(last + 1)
}

func (s *Shipper) flush(ctx context.Context) {
	if len(s.buf) == 0 {
		return
	}
	batch := s.buf
	body, err := json.Marshal(batch)
	if err != nil {
		s.buf = s.buf[:0]
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	err = s.ship(cctx, body)
	cancel()
	if err != nil {
		log.Printf("logship: ship failed (buffering %d): %v", len(batch), err)
		return // keep buffer for retry
	}
	s.buf = s.buf[:0]
}

// parseLine maps one nginx JSON log line to a shipped Entry.
func parseLine(line []byte) (Entry, bool) {
	var n nginxLine
	if err := json.Unmarshal(line, &n); err != nil {
		return Entry{}, false
	}
	ts, err := time.Parse(time.RFC3339, n.TS)
	if err != nil {
		ts = time.Now().UTC()
	}
	country := n.Country
	if country == "-" {
		country = "" // GeoIP placeholder
	}
	return Entry{
		TS: ts, Zone: n.Host, ClientIP: n.IP, Country: country, Method: n.Method, Path: n.Path,
		Status: n.Status, Bytes: n.Bytes, CacheStatus: n.Cache,
		RequestTime: parseFloat(n.RT), UpstreamTime: parseFloat(n.URT),
		Referer: n.Ref, UserAgent: n.UA, RequestID: n.RID,
	}, true
}

// parseFloat parses a timing value; "" -> 0, a comma/space list -> the first value.
func parseFloat(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "-" {
		return 0
	}
	if i := strings.IndexAny(s, ", "); i >= 0 {
		s = s[:i]
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}
