// Package dns is a thin, typed client for the Bunny DNS Core API
// (https://api.bunny.net), used by the control plane to manage the DNS records
// that route traffic to Brisk edges.
//
// We hand-roll this client rather than using github.com/libdns/bunny on purpose:
// later Phase-3 steps need Bunny's Smart-Record + health-monitor fields
// (SmartRoutingType, LatencyZone, MonitorType/Status) which libdns abstracts
// away behind a generic record interface. A direct client keeps those fields
// first-class. Step 1 only exercises CRUD; routing/monitoring semantics are
// wired in Steps 3-4.
package dns

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const baseURL = "https://api.bunny.net"

// Record types (Bunny's numeric enum — A is 0, NOT a string).
const (
	TypeA     = 0
	TypeAAAA  = 1
	TypeCNAME = 2
	TypeTXT   = 3
)

// Smart-routing types (captured for Steps 3-4; not set in Step 1).
const (
	SmartNone        = 0
	SmartLatency     = 1
	SmartGeolocation = 2
)

// Monitor types (Bunny's native DNS record health monitor — the control-plane-
// independent failover backstop). Verified against Bunny's Add-DNS-Record API.
// Ping = 4 ICMP packets every 30s from 3 regions (≥1 reply within 2500ms passes);
// Http = GET on :80 every 30s (non-5xx within 10s passes). When a record fails,
// Bunny flips its (read-only) MonitorStatus to offline and drops it from DNS
// responses — WITHOUT deleting the record — so it keeps probing and re-includes
// it on recovery. This is Layer 2: it runs on Bunny's infra, so it pulls a dead
// edge even when Brisk's control plane is down. Brisk owns MonitorType (we set
// it); Bunny owns MonitorStatus (we never write it) — so the two never fight.
const (
	MonitorNone = 0
	MonitorPing = 1
	MonitorHTTP = 2
)

// MonitorTypeFromString maps a config string to the Bunny enum. Defaults to Ping
// (safest: just checks reachability — no dependence on what :80 returns).
func MonitorTypeFromString(s string) int {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "http":
		return MonitorHTTP
	case "none", "off", "":
		return MonitorNone
	default:
		return MonitorPing
	}
}

// CommentPrefix tags every Brisk-managed record so we can tell ours apart from
// manual records and never touch records we didn't create.
const CommentPrefix = "brisk:"

// Record mirrors a Bunny DNS record. Field names match the API's PascalCase
// JSON.
//
// IMPORTANT: Bunny's update (POST) is a partial MERGE — a field omitted from the
// body keeps its previous value. Verified with a live write+read-back: switching
// a record geographic->latency while omitting the coordinates left stale
// coordinates behind. So the Smart-Record fields (SmartRoutingType, LatencyZone,
// GeolocationLatitude/Longitude) and Weight deliberately have NO omitempty: the
// reconciler always writes the full desired state, which lets a mode switch clear
// stale fields (explicit 0 / "" overwrite). A plain manual A record simply sends
// SmartRoutingType:0 (none), zero coords, empty latency zone — all harmless.
type Record struct {
	ID               int64   `json:"Id,omitempty"`
	Type             int     `json:"Type"`
	TTL              int     `json:"Ttl,omitempty"`
	Value            string  `json:"Value"`
	Name             string  `json:"Name"`
	Weight           int     `json:"Weight"`
	Priority         int     `json:"Priority,omitempty"`
	Disabled         bool    `json:"Disabled"`
	Comment          string  `json:"Comment,omitempty"`
	SmartRoutingType int     `json:"SmartRoutingType"`
	LatencyZone      string  `json:"LatencyZone"`
	// MonitorType is Brisk-owned (the reconciler sets it). NO omitempty — like the
	// Smart fields above, the reconciler writes the full desired state so an explicit
	// 0 clears the monitor (turning BRISK_DNS_MONITOR off restores byte-identical
	// records). MonitorStatus is Bunny-owned (the live offline/online verdict): it
	// KEEPS omitempty and is never set in any desired Record, so our partial-merge
	// writes always omit it and Bunny's verdict survives — the two brains don't fight.
	MonitorType   int `json:"MonitorType"`
	MonitorStatus int `json:"MonitorStatus,omitempty"`
	GeoLatitude      float64 `json:"GeolocationLatitude"`
	GeoLongitude     float64 `json:"GeolocationLongitude"`
}

// IsBriskManaged reports whether Brisk created this record (by its comment tag).
func (r Record) IsBriskManaged() bool {
	return strings.HasPrefix(r.Comment, CommentPrefix)
}

// Zone is a Bunny DNS zone (subset of fields we use).
type Zone struct {
	ID      int64    `json:"Id"`
	Domain  string   `json:"Domain"`
	Records []Record `json:"Records"`
}

type zoneList struct {
	Items        []Zone `json:"Items"`
	TotalItems   int    `json:"TotalItems"`
	HasMoreItems bool   `json:"HasMoreItems"`
}

// APIError is a typed error carrying the HTTP status from Bunny.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("bunny dns api: %d: %s", e.Status, strings.TrimSpace(e.Body))
}

// IsAuth reports whether the error is an auth failure (bad/expired key).
func (e *APIError) IsAuth() bool { return e.Status == http.StatusUnauthorized }

// Client talks to one Bunny DNS zone.
type Client struct {
	apiKey   string
	zoneID   int64
	zoneName string
	http     *http.Client
}

// New builds a client and resolves the zone. Returns (nil, nil) when apiKey is
// empty, so the control plane runs fine without DNS configured. If zoneID is
// empty, zoneName is resolved to an id via ListZones.
func New(ctx context.Context, apiKey, zoneID, zoneName string) (*Client, error) {
	if apiKey == "" {
		return nil, nil // not configured — DNS integration off
	}
	c := &Client{
		apiKey:   apiKey,
		zoneName: zoneName,
		http:     &http.Client{Timeout: 15 * time.Second},
	}
	if zoneID != "" {
		id, err := strconv.ParseInt(zoneID, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid BUNNY_DNS_ZONE_ID %q: %w", zoneID, err)
		}
		c.zoneID = id
		// When only the numeric id is configured (BUNNY_DNS_ZONE_ID without
		// BUNNY_DNS_ZONE), resolve the zone's domain so FQDN-valued records — like
		// the wildcard CNAME target `<routing>.<zone>` — can be built. Best-effort:
		// the A-set reconcile doesn't need the name, so a hiccup here must not break
		// startup; EnsureWildcardCNAME guards against an empty name separately.
		if c.zoneName == "" {
			if z, zerr := c.GetZone(ctx); zerr == nil {
				c.zoneName = z.Domain
			} else {
				slog.Warn("bunny: could not resolve zone domain from id; FQDN-valued records may fail",
					"zone_id", c.zoneID, "err", zerr.Error())
			}
		}
	} else if zoneName != "" {
		id, err := c.resolveZone(ctx, zoneName)
		if err != nil {
			return nil, err
		}
		c.zoneID = id
	} else {
		return nil, errors.New("BUNNY_DNS_ZONE_ID or BUNNY_DNS_ZONE is required when BUNNY_API_KEY is set")
	}
	return c, nil
}

// ZoneID returns the resolved numeric zone id.
func (c *Client) ZoneID() int64 { return c.zoneID }

// ZoneName returns the configured zone domain (may be empty if only id given).
func (c *Client) ZoneName() string { return c.zoneName }

// resolveZone finds a zone id by domain name (paged).
func (c *Client) resolveZone(ctx context.Context, name string) (int64, error) {
	for page := 1; page <= 50; page++ {
		var zl zoneList
		if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/dnszone?page=%d&perPage=1000", page), nil, &zl); err != nil {
			return 0, err
		}
		for _, z := range zl.Items {
			if strings.EqualFold(z.Domain, name) {
				return z.ID, nil
			}
		}
		if !zl.HasMoreItems {
			break
		}
	}
	return 0, fmt.Errorf("dns zone %q not found in this Bunny account", name)
}

// ListZones returns the account's DNS zones (first page; for connectivity checks).
func (c *Client) ListZones(ctx context.Context) ([]Zone, error) {
	var zl zoneList
	if err := c.do(ctx, http.MethodGet, "/dnszone?page=1&perPage=1000", nil, &zl); err != nil {
		return nil, err
	}
	return zl.Items, nil
}

// GetZone fetches the configured zone (including its records).
func (c *Client) GetZone(ctx context.Context) (Zone, error) {
	var z Zone
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/dnszone/%d", c.zoneID), nil, &z)
	return z, err
}

// Ping verifies the key + zone are valid by fetching the zone. Returns the
// record count.
func (c *Client) Ping(ctx context.Context) (int, error) {
	z, err := c.GetZone(ctx)
	if err != nil {
		return 0, err
	}
	return len(z.Records), nil
}

// ListRecords returns the zone's records.
func (c *Client) ListRecords(ctx context.Context) ([]Record, error) {
	z, err := c.GetZone(ctx)
	if err != nil {
		return nil, err
	}
	return z.Records, nil
}

// FindRecord returns a record matching type+name+value (case-insensitive name),
// or nil. Used for idempotent writes.
func (c *Client) FindRecord(ctx context.Context, typ int, name, value string) (*Record, error) {
	recs, err := c.ListRecords(ctx)
	if err != nil {
		return nil, err
	}
	for i := range recs {
		r := recs[i]
		if r.Type == typ && strings.EqualFold(r.Name, name) && r.Value == value {
			return &r, nil
		}
	}
	return nil, nil
}

// AddRecord creates a record (PUT /dnszone/{id}/records) and returns it.
func (c *Client) AddRecord(ctx context.Context, rec Record) (Record, error) {
	var out Record
	err := c.do(ctx, http.MethodPut, fmt.Sprintf("/dnszone/%d/records", c.zoneID), rec, &out)
	return out, err
}

// UpdateRecord updates a record (POST /dnszone/{id}/records/{recordId}). Bunny
// returns 204 No Content, so there is no body to decode.
func (c *Client) UpdateRecord(ctx context.Context, id int64, rec Record) error {
	return c.do(ctx, http.MethodPost, fmt.Sprintf("/dnszone/%d/records/%d", c.zoneID, id), rec, nil)
}

// DeleteRecord deletes a record by id.
func (c *Client) DeleteRecord(ctx context.Context, id int64) error {
	return c.do(ctx, http.MethodDelete, fmt.Sprintf("/dnszone/%d/records/%d", c.zoneID, id), nil, nil)
}

// EnsureRecord is an idempotent add: if a record with the same type+name+value
// already exists it is returned unchanged (no duplicate); otherwise it is
// created. The returned bool reports whether a new record was created.
func (c *Client) EnsureRecord(ctx context.Context, rec Record) (Record, bool, error) {
	existing, err := c.FindRecord(ctx, rec.Type, rec.Name, rec.Value)
	if err != nil {
		return Record{}, false, err
	}
	if existing != nil {
		return *existing, false, nil
	}
	created, err := c.AddRecord(ctx, rec)
	return created, err == nil, err
}

// EnsureWildcardCNAME makes every tenant hostname under the routing record resolve
// with ONE record (Step 4.7 Part 2): a CNAME `*.<routingName>` -> `<routingName>.<zone>`
// (e.g. `*.cdn` -> `cdn.a2zjav.com`). Because the target is the geo-routed smart A-set,
// every `<tenant>.cdn.<zone>` inherits geo-routing + health automatically — no per-zone
// DNS writes. Guardrailed: create-if-missing only; if ANY CNAME already exists for that
// name (ours or not) it's left untouched (never clobber). Returns true if it created one.
// The `*` wildcard does NOT match the bare `<routingName>` label, so `cdn.<zone>` itself
// is unaffected. Returns (false, nil) when routingName is empty/"@" (no wildcard then).
func (c *Client) EnsureWildcardCNAME(ctx context.Context, routingName string, ttl int) (bool, error) {
	routingName = strings.TrimSpace(routingName)
	if routingName == "" || routingName == "@" {
		return false, nil
	}
	if c.zoneName == "" {
		// Without the zone domain we'd build a bare-label value (e.g. "cdn.") that
		// Bunny rejects ("not a valid hostname"). Refuse rather than emit garbage.
		return false, fmt.Errorf("EnsureWildcardCNAME: zone name unresolved; cannot build CNAME target for *.%s", routingName)
	}
	name := "*." + routingName
	recs, err := c.ListRecords(ctx)
	if err != nil {
		return false, err
	}
	for _, r := range recs {
		if r.Type == TypeCNAME && strings.EqualFold(r.Name, name) {
			return false, nil // already present — do not clobber
		}
	}
	value := routingName + "." + c.zoneName
	// Step 1 (Part 2 gate): never swallow this — log the full request (key is never
	// in the body) and, on failure, the APIError carries Bunny's status + raw body.
	created, err := c.AddRecord(ctx, Record{
		Type:    TypeCNAME,
		Name:    name,
		Value:   value,
		TTL:     ttl,
		Comment: CommentPrefix + "wildcard:" + routingName, // brisk:wildcard:cdn
	})
	if err != nil {
		slog.Warn("EnsureWildcardCNAME: bunny add-record failed",
			"name", name, "value", value, "type", TypeCNAME, "ttl", ttl,
			"zone_name", c.zoneName, "err", err.Error())
		return false, err
	}
	slog.Info("EnsureWildcardCNAME: created wildcard cname",
		"name", name, "value", value, "id", created.ID)
	return true, nil
}

// do performs an HTTP request with auth, JSON encoding/decoding, and retries on
// 429/5xx (respecting Retry-After). The API key is never included in errors/logs.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	const maxAttempts = 4
	var lastErr error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		var rdr io.Reader
		if body != nil {
			b, err := json.Marshal(body)
			if err != nil {
				return fmt.Errorf("encode request: %w", err)
			}
			rdr = bytes.NewReader(b)
		}

		req, err := http.NewRequestWithContext(ctx, method, baseURL+path, rdr)
		if err != nil {
			return err
		}
		req.Header.Set("AccessKey", c.apiKey)
		req.Header.Set("Accept", "application/json")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = err // network error — retryable
			sleep(ctx, backoff(attempt), "")
			continue
		}

		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()

		switch {
		case resp.StatusCode >= 200 && resp.StatusCode < 300:
			if out != nil && len(bytes.TrimSpace(respBody)) > 0 {
				if err := json.Unmarshal(respBody, out); err != nil {
					return fmt.Errorf("decode response: %w", err)
				}
			}
			return nil
		case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
			lastErr = &APIError{Status: resp.StatusCode, Body: string(respBody)}
			if attempt < maxAttempts {
				sleep(ctx, backoff(attempt), resp.Header.Get("Retry-After"))
				continue
			}
			return lastErr
		default:
			// 4xx (auth, validation, not-found) — not retryable.
			return &APIError{Status: resp.StatusCode, Body: string(respBody)}
		}
	}
	return lastErr
}

// backoff returns an exponential delay with jitter, capped.
func backoff(attempt int) time.Duration {
	base := time.Duration(1<<uint(attempt-1)) * 300 * time.Millisecond
	if base > 5*time.Second {
		base = 5 * time.Second
	}
	jitter := time.Duration(rand.Int63n(int64(250 * time.Millisecond)))
	return base + jitter
}

// sleep waits for d, or the Retry-After header (seconds) if larger, or until ctx
// is done.
func sleep(ctx context.Context, d time.Duration, retryAfter string) {
	if retryAfter != "" {
		if secs, err := strconv.Atoi(strings.TrimSpace(retryAfter)); err == nil {
			if ra := time.Duration(secs) * time.Second; ra > d {
				d = ra
			}
		}
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
