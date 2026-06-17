// Package stats collects edge metrics (cache hit ratio, bandwidth, CPU/RAM/disk)
// and ships them to the control plane (Phase 2 Step 4).
//
// Sources:
//   - Nginx access log (brisk format), tailed incrementally per zone (host).
//   - Nginx stub_status (active connections), localhost only.
//   - System metrics via gopsutil (cpu/ram and the cache-disk usage).
package stats

import (
	"bufio"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
)

// defaultInterval sizes the first bandwidth rate (before a real inter-sample delta).
const defaultInterval = 10 * time.Second

// Sample is one metric record sent to the control plane. Zone == "" marks the
// server-level summary (carries cpu/ram/disk); a non-empty Zone is per-host.
type Sample struct {
	Time         time.Time `json:"time"`
	Zone         string    `json:"zone,omitempty"`
	Requests     int64     `json:"requests"`
	Hits         int64     `json:"hits"`
	Misses       int64     `json:"misses"`
	BytesSent    int64     `json:"bytes_sent"`
	BandwidthBps int64     `json:"bandwidth_bps"`
	CPUPct       *float64  `json:"cpu_pct,omitempty"`
	RAMPct       *float64  `json:"ram_pct,omitempty"`
	DiskPct      *float64  `json:"disk_pct,omitempty"`
	ActiveConns  *int64    `json:"active_conns,omitempty"`
	// Total physical RAM + cache-disk capacity (bytes); quasi-static, sent on the
	// server-level summary so the dashboard shows absolute "X free of Y".
	RAMTotalBytes  *int64 `json:"ram_total_bytes,omitempty"`
	DiskTotalBytes *int64 `json:"disk_total_bytes,omitempty"`
}

type agg struct {
	req, hit, miss, bytes int64
}

// Collector gathers metrics. Collect is called from a single goroutine.
type Collector struct {
	statusURL string
	accessLog string
	cacheDir  string
	http      *http.Client

	mu       sync.Mutex
	offset   int64
	inited   bool
	lastTime time.Time
}

// NewCollector builds a collector for the given stub_status URL, access log, and
// cache directory (whose disk usage is reported).
func NewCollector(statusURL, accessLog, cacheDir string) *Collector {
	return &Collector{
		statusURL: statusURL,
		accessLog: accessLog,
		cacheDir:  cacheDir,
		http:      &http.Client{Timeout: 3 * time.Second},
	}
}

// Collect returns the samples for this interval: one per active zone plus a
// server-level summary (always present, so cpu/ram/disk land every interval).
func (c *Collector) Collect() ([]Sample, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now().UTC()
	elapsed := c.interval(now)
	bw := func(bytes int64) int64 {
		if elapsed <= 0 {
			return 0
		}
		return int64(float64(bytes) * 8 / elapsed.Seconds())
	}

	zones, total := c.readAccessLog()

	samples := make([]Sample, 0, len(zones)+1)
	for host, a := range zones {
		samples = append(samples, Sample{
			Time: now, Zone: host,
			Requests: a.req, Hits: a.hit, Misses: a.miss,
			BytesSent: a.bytes, BandwidthBps: bw(a.bytes),
		})
	}
	// server-level summary with system metrics
	samples = append(samples, Sample{
		Time: now, Zone: "",
		Requests: total.req, Hits: total.hit, Misses: total.miss,
		BytesSent: total.bytes, BandwidthBps: bw(total.bytes),
		CPUPct: cpuPercent(), RAMPct: ramPercent(), DiskPct: diskPercent(c.cacheDir),
		ActiveConns:    c.scrapeStub(),
		RAMTotalBytes:  ramTotalBytes(),
		DiskTotalBytes: diskTotalBytes(c.cacheDir),
	})
	return samples, nil
}

func (c *Collector) interval(now time.Time) time.Duration {
	if c.lastTime.IsZero() {
		c.lastTime = now
		return defaultInterval
	}
	d := now.Sub(c.lastTime)
	c.lastTime = now
	if d <= 0 {
		d = time.Second
	}
	return d
}

// readAccessLog reads only the bytes appended since the last call. On first call
// it starts at EOF (no historical backfill). A shrunk file means rotation -> reset.
func (c *Collector) readAccessLog() (map[string]*agg, *agg) {
	zones := map[string]*agg{}
	total := &agg{}

	f, err := os.Open(c.accessLog)
	if err != nil {
		return zones, total
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return zones, total
	}
	size := fi.Size()

	if !c.inited {
		c.offset = size // skip everything already in the log
		c.inited = true
		return zones, total
	}
	if size < c.offset {
		c.offset = 0 // truncated / rotated
	}
	if size == c.offset {
		return zones, total // nothing new
	}
	if _, err := f.Seek(c.offset, 0); err != nil {
		return zones, total
	}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		host, cache, _, bytes, ok := parseLine(sc.Text())
		if !ok {
			continue
		}
		a := zones[host]
		if a == nil {
			a = &agg{}
			zones[host] = a
		}
		a.req++
		total.req++
		a.bytes += bytes
		total.bytes += bytes
		switch cache {
		case "HIT":
			a.hit++
			total.hit++
		case "MISS":
			a.miss++
			total.miss++
		}
	}
	c.offset = size
	return zones, total
}

// parseLine parses: `IP host cache [time] "request" status bytes rt=...`
func parseLine(line string) (host, cache string, status int, bytes int64, ok bool) {
	f := strings.Fields(line)
	if len(f) < 3 {
		return "", "", 0, 0, false
	}
	host, cache = f[1], f[2]
	i := strings.IndexByte(line, '"')
	if i < 0 {
		return "", "", 0, 0, false
	}
	j := strings.IndexByte(line[i+1:], '"')
	if j < 0 {
		return "", "", 0, 0, false
	}
	rest := strings.Fields(strings.TrimSpace(line[i+1+j+1:]))
	if len(rest) < 2 {
		return "", "", 0, 0, false
	}
	status, _ = strconv.Atoi(rest[0])
	bytes, _ = strconv.ParseInt(rest[1], 10, 64)
	return host, cache, status, bytes, true
}

// scrapeStub fetches "Active connections" from stub_status (best-effort).
func (c *Collector) scrapeStub() *int64 {
	resp, err := c.http.Get(c.statusURL)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "Active connections:") {
			v := strings.TrimSpace(strings.TrimPrefix(line, "Active connections:"))
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				return &n
			}
		}
	}
	return nil
}

func cpuPercent() *float64 {
	p, err := cpu.Percent(0, false) // since last call; non-blocking
	if err != nil || len(p) == 0 {
		return nil
	}
	v := p[0]
	return &v
}

func ramPercent() *float64 {
	m, err := mem.VirtualMemory()
	if err != nil {
		return nil
	}
	return &m.UsedPercent
}

func diskPercent(path string) *float64 {
	u, err := disk.Usage(path)
	if err != nil {
		if u, err = disk.Usage("/"); err != nil {
			return nil
		}
	}
	return &u.UsedPercent
}

// ramTotalBytes reports total physical RAM (bytes), nil on error.
func ramTotalBytes() *int64 {
	m, err := mem.VirtualMemory()
	if err != nil {
		return nil
	}
	v := int64(m.Total)
	return &v
}

// diskTotalBytes reports the cache disk's total capacity (bytes), falling back to "/",
// nil on error. Mirrors diskPercent's path handling so the % and total agree.
func diskTotalBytes(path string) *int64 {
	u, err := disk.Usage(path)
	if err != nil {
		if u, err = disk.Usage("/"); err != nil {
			return nil
		}
	}
	v := int64(u.Total)
	return &v
}
