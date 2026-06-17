package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Server is an edge node (PoP). The SSH password is NEVER stored — it is used
// once during provisioning and discarded.
type Server struct {
	ID            int64      `json:"id"`
	Name          string     `json:"name"`
	Region        string     `json:"region"`
	IP            string     `json:"ip"`
	Hostname      *string    `json:"hostname,omitempty"`
	EdgeID        string     `json:"edge_id"`
	CapacityMbps  *int32     `json:"capacity_mbps,omitempty"`
	Status        string     `json:"status"`
	SSHUser       *string    `json:"ssh_user,omitempty"`
	SSHPort       int32      `json:"ssh_port"`
	HostKey       *string    `json:"-"` // TOFU-pinned; internal use only
	LastSeen      *time.Time `json:"last_seen,omitempty"`
	ProvisionedAt *time.Time `json:"provisioned_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`

	// Origin Shield (Phase 4 Step 3). 'edge' (default; user-facing, in the geo DNS
	// set) | 'shield' (mid-tier cache in front of origins; NOT in DNS rotation).
	Role string `json:"role"`

	// Hybrid Shield+PoP (Build Spec, Layer 1): when role=shield, serve_public=true
	// keeps the box in the public geo DNS set AND acting as the parent/shield (a
	// "hybrid" PoP); serve_public=false is a pure shield (excluded from DNS — the
	// original Step-3 behavior). Ignored for role=edge (edges are always public).
	ServePublic bool `json:"serve_public"`

	// Smart-Record routing (Phase 3 Step 3).
	RoutingWeight   int32  `json:"routing_weight"`   // 0-100; default 100
	RoutingOverride string `json:"routing_override"` // "" | geographic | latency

	// Capacity-weighted routing (opt-in, default false). When true, the DNS reconciler
	// derives this PoP's Smart-Record weight from CapacityMbps (normalized to the fleet's
	// biggest capacity-weighted box) instead of RoutingWeight. False => uses RoutingWeight.
	WeightByCapacity bool `json:"weight_by_capacity"`

	// Health checking (Phase 3 Step 4). 0 on a threshold/interval means "inherit
	// the network-wide default".
	HealthStatus          string     `json:"health_status"` // unknown | healthy | unhealthy
	HealthCheckedAt       *time.Time `json:"health_checked_at,omitempty"`
	HealthEnabled         bool       `json:"health_enabled"`
	HealthIntervalSeconds int32      `json:"health_interval_seconds"`
	HealthFailThreshold   int32      `json:"health_fail_threshold"`
	HealthRiseThreshold   int32      `json:"health_rise_threshold"`

	// Admin drain / maintenance (Phase 3 Step 5). Drained wins over health in the
	// reconciler precedence (drain > health > heartbeat).
	Drained     bool       `json:"drained"`
	DrainedAt   *time.Time `json:"drained_at,omitempty"`
	DrainReason string     `json:"drain_reason"`

	// Physical resources reported by the agent (bytes): total RAM + total cache-disk
	// capacity. Quasi-static; the dashboard derives live "available" from total × the
	// live used%. Nil until an agent that reports them checks in.
	RAMTotalBytes  *int64 `json:"ram_total_bytes,omitempty"`
	DiskTotalBytes *int64 `json:"disk_total_bytes,omitempty"`

	// Tech / runtime stack the agent reports about this PoP (migration 00029),
	// refreshed on every heartbeat. Empty until an agent that reports them checks in.
	NginxVersion string `json:"nginx_version,omitempty"`
	AgentVersion string `json:"agent_version,omitempty"`
	OSPretty     string `json:"os_pretty,omitempty"`
	Kernel       string `json:"kernel,omitempty"`
	GoVersion    string `json:"go_version,omitempty"`
}

// CreateServerParams are the inputs for creating a server.
type CreateServerParams struct {
	Name         string
	Region       string
	IP           string
	Hostname     *string
	EdgeID           string
	CapacityMbps     *int32
	WeightByCapacity bool
	SSHUser          *string
	SSHPort          int32
}

const serverCols = `id, name, region, host(ip) AS ip, hostname, edge_id, capacity_mbps,
	status, ssh_user, ssh_port, host_key, last_seen, provisioned_at, created_at,
	role,
	routing_weight, routing_override,
	health_status, health_checked_at, health_enabled,
	health_interval_seconds, health_fail_threshold, health_rise_threshold,
	drained, drained_at, drain_reason,
	serve_public, weight_by_capacity,
	ram_total_bytes, disk_total_bytes,
	nginx_version, agent_version, os_pretty, kernel, go_version`

func scanServer(row pgx.Row) (Server, error) {
	var s Server
	err := row.Scan(&s.ID, &s.Name, &s.Region, &s.IP, &s.Hostname, &s.EdgeID, &s.CapacityMbps,
		&s.Status, &s.SSHUser, &s.SSHPort, &s.HostKey, &s.LastSeen, &s.ProvisionedAt, &s.CreatedAt,
		&s.Role,
		&s.RoutingWeight, &s.RoutingOverride,
		&s.HealthStatus, &s.HealthCheckedAt, &s.HealthEnabled,
		&s.HealthIntervalSeconds, &s.HealthFailThreshold, &s.HealthRiseThreshold,
		&s.Drained, &s.DrainedAt, &s.DrainReason,
		&s.ServePublic, &s.WeightByCapacity,
		&s.RAMTotalBytes, &s.DiskTotalBytes,
		&s.NginxVersion, &s.AgentVersion, &s.OSPretty, &s.Kernel, &s.GoVersion)
	return s, err
}

// SetServerResources records the agent-reported total RAM + total cache-disk capacity
// (bytes). Both optional (nil = leave unchanged), so a stats report missing one doesn't
// wipe it. No-op when both are nil. Called from the stats-ingest path.
func (st *Store) SetServerResources(ctx context.Context, id int64, ramTotal, diskTotal *int64) error {
	if ramTotal == nil && diskTotal == nil {
		return nil
	}
	_, err := st.pool.Exec(ctx,
		`UPDATE servers
		    SET ram_total_bytes  = COALESCE($2, ram_total_bytes),
		        disk_total_bytes = COALESCE($3, disk_total_bytes)
		  WHERE id = $1`, id, ramTotal, diskTotal)
	return err
}

// SetServerTech records the agent-reported tech/runtime stack (migration 00029).
// Called from the heartbeat path. Each column keeps its stored value when the
// incoming string is empty, so an older agent (which omits the newer fields) never
// wipes them — and a no-op when every value is blank.
func (st *Store) SetServerTech(ctx context.Context, id int64, nginx, agent, osPretty, kernel, goVersion string) error {
	if nginx == "" && agent == "" && osPretty == "" && kernel == "" && goVersion == "" {
		return nil
	}
	_, err := st.pool.Exec(ctx,
		`UPDATE servers
		    SET nginx_version = CASE WHEN $2 = '' THEN nginx_version ELSE $2 END,
		        agent_version = CASE WHEN $3 = '' THEN agent_version ELSE $3 END,
		        os_pretty     = CASE WHEN $4 = '' THEN os_pretty     ELSE $4 END,
		        kernel        = CASE WHEN $5 = '' THEN kernel        ELSE $5 END,
		        go_version    = CASE WHEN $6 = '' THEN go_version    ELSE $6 END
		  WHERE id = $1`, id, nginx, agent, osPretty, kernel, goVersion)
	return err
}

// CreateServer inserts a server (status defaults to 'pending') and returns it.
func (st *Store) CreateServer(ctx context.Context, p CreateServerParams) (Server, error) {
	port := p.SSHPort
	if port == 0 {
		port = 22
	}
	row := st.pool.QueryRow(ctx, `
		INSERT INTO servers (name, region, ip, hostname, edge_id, capacity_mbps, ssh_user, ssh_port, weight_by_capacity)
		VALUES ($1, $2, $3::inet, $4, $5, $6, $7, $8, $9)
		RETURNING `+serverCols,
		p.Name, p.Region, p.IP, p.Hostname, p.EdgeID, p.CapacityMbps, p.SSHUser, port, p.WeightByCapacity)
	return scanServer(row)
}

// ListServers returns all servers.
func (st *Store) ListServers(ctx context.Context) ([]Server, error) {
	rows, err := st.pool.Query(ctx, `SELECT `+serverCols+` FROM servers ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Server{}
	for rows.Next() {
		s, err := scanServer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// GetServer returns one server or ErrNotFound.
func (st *Store) GetServer(ctx context.Context, id int64) (Server, error) {
	row := st.pool.QueryRow(ctx, `SELECT `+serverCols+` FROM servers WHERE id = $1`, id)
	s, err := scanServer(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Server{}, ErrNotFound
	}
	return s, err
}

// DeleteServer removes a server; ErrNotFound if it did not exist.
func (st *Store) DeleteServer(ctx context.Context, id int64) error {
	ct, err := st.pool.Exec(ctx, `DELETE FROM servers WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateServerStatus sets the server status.
func (st *Store) UpdateServerStatus(ctx context.Context, id int64, status string) error {
	_, err := st.pool.Exec(ctx, `UPDATE servers SET status = $2 WHERE id = $1`, id, status)
	return err
}

// UpdateServerRouting sets per-server Smart-Record routing (weight 0-100 and an
// optional mode override ""|geographic|latency). Returns the updated server.
func (st *Store) UpdateServerRouting(ctx context.Context, id int64, weight int32, override string, capacityMbps *int32, weightByCapacity *bool) (Server, error) {
	// capacityMbps / weightByCapacity are optional (nil = leave unchanged) so the
	// routing card can edit a PoP's bandwidth + capacity-weighting toggle in the same
	// save, while a plain weight/override change doesn't disturb them.
	row := st.pool.QueryRow(ctx,
		`UPDATE servers
		    SET routing_weight = $2, routing_override = $3,
		        capacity_mbps = COALESCE($4, capacity_mbps),
		        weight_by_capacity = COALESCE($5, weight_by_capacity)
		  WHERE id = $1
		  RETURNING `+serverCols, id, weight, override, capacityMbps, weightByCapacity)
	s, err := scanServer(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Server{}, ErrNotFound
	}
	return s, err
}

// SetServerHealth persists the last-known health verdict + probe time. Called by
// the health checker on a state transition (and seeded on probe) so a restart
// restores rotation state instead of blackholing or thrashing.
func (st *Store) SetServerHealth(ctx context.Context, id int64, status string) error {
	_, err := st.pool.Exec(ctx,
		`UPDATE servers SET health_status = $2, health_checked_at = now() WHERE id = $1`, id, status)
	return err
}

// UpdateServerHealthConfig sets per-server health-check overrides (0 threshold/
// interval = inherit network default). Returns the updated server.
func (st *Store) UpdateServerHealthConfig(ctx context.Context, id int64, enabled bool, intervalSec, failThreshold, riseThreshold int32) (Server, error) {
	row := st.pool.QueryRow(ctx,
		`UPDATE servers
		    SET health_enabled = $2, health_interval_seconds = $3,
		        health_fail_threshold = $4, health_rise_threshold = $5
		  WHERE id = $1
		  RETURNING `+serverCols, id, enabled, intervalSec, failThreshold, riseThreshold)
	s, err := scanServer(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Server{}, ErrNotFound
	}
	return s, err
}

// SetServerDrain sets a single server's drain flag (true = pulled from rotation).
// Clears drained_at/drain_reason on undrain. Returns the updated server.
func (st *Store) SetServerDrain(ctx context.Context, id int64, drained bool, reason string) (Server, error) {
	var row pgx.Row
	if drained {
		row = st.pool.QueryRow(ctx,
			`UPDATE servers SET drained = true, drained_at = now(), drain_reason = $2
			 WHERE id = $1 RETURNING `+serverCols, id, reason)
	} else {
		row = st.pool.QueryRow(ctx,
			`UPDATE servers SET drained = false, drained_at = NULL, drain_reason = ''
			 WHERE id = $1 RETURNING `+serverCols, id)
	}
	s, err := scanServer(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Server{}, ErrNotFound
	}
	return s, err
}

// DrainRegion drains/undrains every server in a region (case-insensitive). It
// returns the affected servers (so the caller can audit each). Empty result =
// no servers in that region.
func (st *Store) DrainRegion(ctx context.Context, region string, drained bool, reason string) ([]Server, error) {
	var rows pgx.Rows
	var err error
	if drained {
		rows, err = st.pool.Query(ctx,
			`UPDATE servers SET drained = true, drained_at = now(), drain_reason = $2
			 WHERE upper(region) = upper($1) RETURNING `+serverCols, region, reason)
	} else {
		rows, err = st.pool.Query(ctx,
			`UPDATE servers SET drained = false, drained_at = NULL, drain_reason = ''
			 WHERE upper(region) = upper($1) RETURNING `+serverCols, region)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Server{}
	for rows.Next() {
		s, err := scanServer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// SetServerRole sets a server's role ('edge' | 'shield'). A pure shield PoP is a
// mid-tier cache in front of origins and is excluded from the geo DNS set. When
// servePublic is non-nil it ALSO sets serve_public (Hybrid Shield+PoP, Build Spec
// Layer 1): role=shield + serve_public=true is a hybrid box that stays in DNS while
// acting as the parent. A nil servePublic leaves the existing flag untouched (so a
// plain edge/shield role flip doesn't disturb it). Returns the updated server.
func (st *Store) SetServerRole(ctx context.Context, id int64, role string, servePublic *bool) (Server, error) {
	var row pgx.Row
	if servePublic != nil {
		row = st.pool.QueryRow(ctx,
			`UPDATE servers SET role = $2, serve_public = $3 WHERE id = $1 RETURNING `+serverCols, id, role, *servePublic)
	} else {
		row = st.pool.QueryRow(ctx,
			`UPDATE servers SET role = $2 WHERE id = $1 RETURNING `+serverCols, id, role)
	}
	s, err := scanServer(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Server{}, ErrNotFound
	}
	return s, err
}

// SetServerHostKey pins the TOFU-captured SSH host key.
func (st *Store) SetServerHostKey(ctx context.Context, id int64, hostKey string) error {
	_, err := st.pool.Exec(ctx, `UPDATE servers SET host_key = $2 WHERE id = $1`, id, hostKey)
	return err
}

// MarkServerOnline flips status to online and updates last_seen (and
// provisioned_at on first check-in). Called from the authenticated heartbeat.
// Returns transitioned=true only when the status actually changed to "online"
// (it was not already online), so the caller triggers a DNS reconcile on the
// transition rather than on every heartbeat.
func (st *Store) MarkServerOnline(ctx context.Context, id int64) (transitioned bool, err error) {
	var prev string
	err = st.pool.QueryRow(ctx, `
		WITH old AS (SELECT status FROM servers WHERE id = $1)
		UPDATE servers
		SET status = 'online', last_seen = now(), provisioned_at = COALESCE(provisioned_at, now())
		WHERE id = $1
		RETURNING (SELECT status FROM old)`, id).Scan(&prev)
	if err != nil {
		return false, err
	}
	return prev != "online", nil
}
