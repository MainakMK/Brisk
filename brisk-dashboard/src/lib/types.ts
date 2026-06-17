/** API response contracts — mirror brisk-control internal/store JSON tags.
   Source of truth: brisk-control/internal/store/{stats,servers,provision}.go. */

export interface Overview {
  online_servers: number;
  req_per_sec: number;
  bandwidth_bps: number;
  hit_ratio: number; // 0..1
  window_seconds: number;
}

export type ServerStatus =
  | "online"
  | "offline"
  | "provisioning"
  | "pending"
  | "disabled"
  | string;

export type ServerRole = "edge" | "shield" | string;

export interface Server {
  id: number;
  name: string;
  region: string;
  ip: string;
  hostname?: string | null;
  edge_id: string;
  capacity_mbps: number;
  // Capacity-weighted routing (opt-in): when true, this PoP's DNS weight is derived from
  // capacity_mbps instead of routing_weight.
  weight_by_capacity?: boolean;
  status: ServerStatus;
  role: ServerRole; // Origin Shield (Phase 4 Step 3): edge (in DNS) | shield (mid-tier)
  // Hybrid Shield+PoP: when role=shield, serve_public=true keeps the box in the public
  // geo DNS set AND acting as the parent (hybrid); false = pure shield (out of DNS).
  serve_public?: boolean;
  ssh_user?: string | null;
  ssh_port?: number;
  last_seen: string | null;
  provisioned_at: string | null;
  created_at: string;

  // Smart-Record routing (Phase 3 Step 3)
  routing_weight: number;
  routing_override: string; // "" | geographic | latency

  // Health checking (Phase 3 Step 4) — 0 threshold/interval = inherit network default
  health_status: "unknown" | "healthy" | "unhealthy" | string;
  health_checked_at?: string | null;
  health_enabled: boolean;
  health_interval_seconds: number;
  health_fail_threshold: number;
  health_rise_threshold: number;

  // Admin drain / maintenance (Phase 3 Step 5)
  drained: boolean;
  drained_at?: string | null;
  drain_reason: string;

  // Tech / runtime stack the agent reports about this PoP (heartbeat → migration 00029).
  // Empty/absent until an agent that reports them has checked in.
  nginx_version?: string;
  agent_version?: string;
  os_pretty?: string;
  kernel?: string;
  go_version?: string;
}

// --- Self-service agent rollout (Phase: agent-self-service-rollout) ---

/** A signed agent release the fleet can self-update to. */
export interface AgentRelease {
  version: string;
  sha256: string;
  signed_by: string;
  size_bytes: number;
  notes: string;
  created_at: string;
}

/** One row of the deploy/pause/rollback/upload audit trail. */
export interface AuditEntry {
  id: number;
  actor: string;
  action: string;
  subject: string;
  details: string;
  at: string;
}

export type RolloutStatus = "scheduled" | "running" | "paused" | "done" | "failed" | "cancelled" | string;
export type RolloutTargetState =
  | "queued"
  | "in_progress"
  | "soaking"
  | "done"
  | "failed"
  | "skipped"
  | string;

/** One "deploy version X to these PoPs" action. */
export interface Rollout {
  id: number;
  release_version: string;
  target_pops: string[];
  soak_seconds: number;
  status: RolloutStatus;
  scheduled_at?: string | null;
  error_reason?: string;
  created_by?: string;
  created_at: string;
  started_at?: string | null;
  finished_at?: string | null;
}

/** One PoP within a rollout. */
export interface RolloutTarget {
  rollout_id: number;
  edge_id: string;
  from_version: string;
  to_version: string;
  state: RolloutTargetState;
  error_reason?: string;
  soak_until?: string | null;
  updated_at: string;
}

/** GET /rollouts/active and GET /rollouts/{id} response (null when none active). */
export interface RolloutDetail {
  rollout: Rollout;
  targets: RolloutTarget[];
}

export interface StartRolloutInput {
  version: string;
  pops: string[];
  soak_seconds: number;
  scheduled_at?: string | null;
}

/** cpu/ram/disk + last_sample are null until the first metric sample arrives. */
export interface ServerLive {
  server_id: number;
  cpu_pct: number | null;
  ram_pct: number | null;
  disk_pct: number | null;
  req_per_sec: number;
  bandwidth_bps: number;
  hit_ratio: number;
  last_sample: string | null;
  /** Total RAM + cache-disk capacity (bytes); null until the agent reports them. */
  ram_total_bytes?: number | null;
  disk_total_bytes?: number | null;
}

export interface ProvisionLog {
  id: number;
  ts: string;
  level: "info" | "error" | string;
  message: string;
}

export interface SeriesPoint {
  time: string;
  requests: number;
  hits: number;
  misses: number;
  bytes_sent: number;
  bandwidth_bps: number | null;
  cpu_pct: number | null;
  ram_pct: number | null;
  disk_pct: number | null;
  hit_ratio: number;
}

export interface Stats {
  server_id: number;
  resolution: string;
  from: string;
  to: string;
  points: SeriesPoint[];
}

/** POST /servers request — SSH creds are used once for provisioning, not stored. */
export interface CreateServerInput {
  name: string;
  region: string;
  ip: string;
  edge_id?: string;
  capacity_mbps?: number;
  weight_by_capacity?: boolean;
  ssh_user: string;
  ssh_port: number;
  ssh_password?: string;
  ssh_private_key?: string;
}

/** POST /servers / reprovision / rotate responses surface the agent token ONCE. */
export interface CreateServerResponse {
  server: Server;
  agent_token: string;
  status: string;
  note?: string;
}

export interface TokenResponse {
  agent_token: string;
  status?: string;
}

// --- Zones + cache rules (brisk-control internal/store/{zones,rules}.go) ---

export type TLSMode = "selfsigned" | "mkcert" | "letsencrypt";
export type ZoneProfile = "vod" | "live";

export interface Zone {
  id: number;
  account_id: number;
  name: string;
  cdn_hostname: string;
  custom_domain?: string | null;
  origin_url: string;
  host_header: string; // upstream Host header (empty = derive from origin)
  tls_mode: TLSMode | string;
  video: boolean;
  profile: ZoneProfile | string;
  playlist_ttl: string;
  segment_ttl: string;
  cors_origin: string;
  brotli_level: number;
  status: string; // active | disabled
  config_version: number;
  created_at: string;
  updated_at: string;
  rules?: CacheRule[];
  // Origin Shield (Phase 4 Step 3): when enabled, edges proxy this zone's misses
  // through shield_server_id (a role=shield PoP); null = network-default shield.
  origin_shield_enabled?: boolean;
  shield_server_id?: number | null;

  // WAF + rate limiting (Phase 4 Step 4). OFF by default; enabling defaults to
  // detect mode. The full rules/limits live behind GET /zones/{id}/waf.
  waf_enabled?: boolean;
  waf_mode?: WAFMode | string;
  waf_managed_ruleset?: WAFManagedRuleset | string;
  waf_paranoia?: number;
  waf_wp_preset?: boolean;
  waf_fail_open?: boolean;

  // Cache Settings (Bunny-style per-zone cache controls). Always present from the API;
  // every field defaults to current edge behavior (see CacheSettings).
  cache_settings?: CacheSettings;

  // Hotlink Protection (Referer allowlist, migration 00024). OFF by default; when on,
  // the edge blocks (403) requests whose Referer isn't an allowed host.
  hotlink_enabled?: boolean;
  hotlink_allowed_referrers?: string; // comma-separated hosts (may include *.x)
  hotlink_allow_empty_referer?: boolean;

  // Origin connection options (Bunny-style "Origin" panel, migration 00025). OFF by
  // default => today's edge behavior. SSL-verify => validate the origin's TLS cert;
  // follow-redirects => chase an origin 3xx one hop; forward-host => send the client's
  // Host upstream instead of host_header.
  origin_ssl_verify?: boolean;
  origin_follow_redirects?: boolean;
  forward_host_header?: boolean;

  // Custom 502/504 error page (Bunny-style, migration 00026). Branded HTML the edge
  // serves on a nginx-generated 502/504. Empty => off (nginx default page).
  error_5xx_html?: string;

  // Blocked-IP denylist (Bunny-style, migration 00027). Comma-separated IPs / CIDRs the
  // edge blocks (403) on content locations. Empty => off.
  blocked_ips?: string;

  // Access toggles (Bunny-style, migration 00028). block_root_path => 403 the bare root +
  // directory roots; block_post => 405 POST requests. Both default false (off).
  block_root_path?: boolean;
  block_post?: boolean;
}

/** Body for PUT /zones/{id}/hotlink (the dashboard sends the full set). */
export interface SetZoneHotlinkInput {
  enabled: boolean;
  allowed_referrers: string;
  allow_empty_referer: boolean;
}

/** Body for PUT /zones/{id}/error-page. Empty html clears it (back to nginx default). */
export interface SetZoneErrorPageInput {
  html: string;
}

/** Body for PUT /zones/{id}/blocked-ips. Comma/newline IPs or CIDRs; empty clears it. */
export interface SetZoneBlockedIPsInput {
  blocked_ips: string;
}

/** Body for PUT /zones/{id}/access-flags (the dashboard sends the full set). */
export interface SetZoneAccessFlagsInput {
  block_root_path: boolean;
  block_post: boolean;
}

export interface SetZoneShieldInput {
  enabled: boolean;
  shield_server_id?: number | null;
}

// --- Cache Settings (Bunny-style Smart Cache controls). Mirrors brisk-control
// store.CacheSettings + the PUT /zones/{id}/cache-settings body. Defaults reproduce
// current behavior (edge_mode/browser_mode "default", stale_* true, rest off/empty). ---
export type CacheEdgeMode = "default" | "respect_origin" | "override" | "no_cache";
export type CacheBrowserMode = "default" | "match_server" | "override" | "no_cache";

export interface CacheSettings {
  smart: boolean;
  edge_mode: CacheEdgeMode | string;
  edge_ttl: string;
  browser_mode: CacheBrowserMode | string;
  browser_ttl: string;
  query_sort: boolean;
  cache_errors: boolean;
  vary_webp: boolean;
  vary_device: boolean;
  vary_country: boolean;
  vary_cookie: string;
  vary_querystring: boolean;
  vary_headers: string; // comma-separated request header names
  query_whitelist: string; // comma-separated args
  strip_cookies: boolean;
  large_object: boolean;
  stale_offline: boolean;
  stale_updating: boolean;
}

/** The settings that reproduce current edge behavior — the create-time default + the
    fallback when a zone predates the feature. */
export const DEFAULT_CACHE_SETTINGS: CacheSettings = {
  smart: false,
  edge_mode: "default",
  edge_ttl: "",
  browser_mode: "default",
  browser_ttl: "",
  query_sort: false,
  cache_errors: false,
  vary_webp: false,
  vary_device: false,
  vary_country: false,
  vary_cookie: "",
  vary_querystring: false,
  vary_headers: "",
  query_whitelist: "",
  strip_cookies: false,
  large_object: false,
  stale_offline: true,
  stale_updating: true,
};

export interface SetServerRoleInput {
  role: ServerRole;
  // Hybrid Shield+PoP: only meaningful when role=shield. true => stays a public PoP
  // (hybrid); false => pure shield (out of DNS). Omit to leave unchanged.
  serve_public?: boolean;
}

// --- WAF + rate limiting (Phase 4 Step 4) ---
// Mirrors brisk-control internal/store/{zones,waf}.go + internal/api/waf.go.

export type WAFMode = "detect" | "block";
export type WAFManagedRuleset = "off" | "owasp_crs";

export interface WAFSettings {
  enabled: boolean;
  mode: WAFMode | string;
  managed_ruleset: WAFManagedRuleset | string;
  paranoia: number; // CRS paranoia 1..4
  wp_preset: boolean;
  fail_open: boolean;
}

export type WAFRuleField = "ip" | "country" | "path" | "method" | "header" | "user_agent";
export type WAFRuleOp = "eq" | "prefix" | "regex" | "cidr";
export type WAFRuleAction = "block" | "challenge" | "log" | "allow";

export interface WAFCustomRule {
  id: number;
  zone_id: number;
  priority: number;
  field: WAFRuleField | string;
  op: WAFRuleOp | string;
  value: string;
  header_name?: string | null;
  action: WAFRuleAction | string;
  enabled: boolean;
  created_at: string;
}

export interface WAFRateLimit {
  id: number;
  zone_id: number;
  path_match: string;
  match_type: "exact" | "prefix" | string;
  requests: number;
  period_seconds: number;
  burst: number;
  key: "ip" | "ip_path" | string;
  action: "block" | "challenge" | string;
  count_mode: "all" | "errors_only" | string;
  enabled: boolean;
  created_at: string;
}

/** GET /zones/{id}/waf — settings + ordered rules + rate limits. */
export interface WAFConfig extends WAFSettings {
  rules: WAFCustomRule[];
  rate_limits: WAFRateLimit[];
}

export interface SetZoneWAFInput {
  enabled: boolean;
  mode: WAFMode;
  managed_ruleset: WAFManagedRuleset;
  paranoia: number;
  wp_preset: boolean;
  fail_open: boolean;
}

export interface CreateWAFRuleInput {
  priority?: number;
  field: WAFRuleField;
  op: WAFRuleOp;
  value: string;
  header_name?: string;
  action: WAFRuleAction;
  enabled?: boolean;
}

export interface CreateWAFRateLimitInput {
  path_match: string;
  match_type?: "exact" | "prefix";
  requests: number;
  period_seconds: number;
  burst?: number;
  key?: "ip" | "ip_path";
  action?: "block" | "challenge";
  count_mode?: "all" | "errors_only";
  enabled?: boolean;
}

/** One firewall-log row (GET /zones/{id}/security-events, admin /security-events). */
export interface SecurityEvent {
  ts: string;
  zone_id?: number | null;
  server_id?: number | null;
  edge_id: string;
  client_ip: string;
  country: string;
  rule_id: string;
  rule_type: string; // managed | custom | ratelimit
  action: string; // block | detect | log | challenge
  mode: string; // detect | block
  path: string;
  method: string;
  ua: string;
  message: string;
}

export interface SecurityEventsResponse {
  events: SecurityEvent[];
  from: string;
  to: string;
}

export interface SecurityEventCount {
  label: string;
  count: number;
}

/** GET /security-events/summary (admin cross-tenant overview). */
export interface SecurityEventSummary {
  from: string;
  to: string;
  total_block: number;
  total_log: number;
  top_zones: SecurityEventCount[];
  top_ips: SecurityEventCount[];
}

export type RuleMatchType = "path_prefix" | "extension" | "regex";
export type RuleAction = "override_cache_ttl" | "bypass_cache" | "force_download" | "redirect";

export interface CacheRule {
  id: number;
  zone_id: number;
  priority: number;
  match_type: RuleMatchType | string;
  match_value: string;
  action: RuleAction | string;
  action_value?: string | null;
  created_at: string;
}

/** POST /zones — cdn_hostname is set on create only (not updatable via PUT). */
export interface CreateZoneInput {
  name: string;
  cdn_hostname: string;
  custom_domain?: string | null;
  origin_url: string;
  host_header?: string;
  tls_mode?: TLSMode;
  video?: boolean;
  profile?: ZoneProfile;
  playlist_ttl?: string;
  segment_ttl?: string;
  cors_origin?: string;
  brotli_level?: number;
}

/** PUT /zones/{id} — name + everything except cdn_hostname; bumps config_version. */
export interface UpdateZoneInput {
  name?: string;
  custom_domain?: string | null;
  origin_url?: string;
  host_header?: string;
  tls_mode?: TLSMode;
  video?: boolean;
  profile?: ZoneProfile;
  playlist_ttl?: string;
  segment_ttl?: string;
  cors_origin?: string;
  brotli_level?: number;
  status?: string;
  // Origin options (migration 00025).
  origin_ssl_verify?: boolean;
  origin_follow_redirects?: boolean;
  forward_host_header?: boolean;
}

export interface CreateRuleInput {
  priority: number;
  match_type: RuleMatchType;
  match_value: string;
  action: RuleAction;
  action_value?: string | null;
}

// --- Header transforms (Phase 4 Step 5 — Lua edge logic) ---
// Mirrors brisk-control internal/store/transforms.go + internal/api/transforms.go.

export type TransformPhase = "request" | "response";
export type TransformOp = "set" | "remove";
export type TransformMatchType = "all" | "path_prefix" | "path_regex" | "method";

export interface HeaderTransform {
  id: number;
  zone_id: number;
  priority: number;
  phase: TransformPhase | string;
  op: TransformOp | string;
  header: string;
  value?: string | null;
  match_type: TransformMatchType | string;
  match_value?: string | null;
  enabled: boolean;
  created_at: string;
}

export interface CreateHeaderTransformInput {
  priority?: number;
  phase: TransformPhase;
  op: TransformOp;
  header: string;
  value?: string;
  match_type?: TransformMatchType;
  match_value?: string;
  enabled?: boolean;
}

// --- Custom domains + per-domain auto-TLS (Phase 4 Step 2) ---
// Mirrors brisk-control internal/api/domains.go customDomainView.

export type CustomDomainStatus =
  | "pending_dns" // waiting for the customer CNAME
  | "verifying" // DNS being (re)checked before any ACME
  | "issuing" // DNS verified; ACME HTTP-01 in flight
  | "active" // cert issued + fanned out; served via SNI
  | "renewing" // transient during an in-margin renewal
  | "failed" // verify/issue/renew failed or CNAME removed
  | string;

export interface CustomDomain {
  id: number;
  zone_id: number;
  account_id: number;
  domain: string;
  status: CustomDomainStatus;
  verify_method: string;
  cert_name: string;
  last_error: string;
  attempt_count: number;
  last_verified_at?: string | null;
  next_attempt_at?: string | null;
  created_at: string;
  updated_at: string;
  // enriched (view-only) fields
  cname_target: string;
  is_apex: boolean;
  instructions: string;
  zone_hostname?: string; // admin cross-tenant list
  cert_issuer?: string;
  cert_staging?: boolean;
  cert_not_after?: string | null;
  days_remaining?: number;
}

export interface AddDomainInput {
  domain: string;
}

// --- Purge (brisk-control internal/store/purge.go) ---

export type PurgeType = "url" | "prefix" | "zone" | "all";
export type PurgeStatus = "pending" | "partial" | "done" | "failed" | string;

export interface PurgeJob {
  id: number;
  account_id: number;
  zone_id?: number | null;
  type: PurgeType | string;
  target: string;
  status: PurgeStatus;
  edges_total: number;
  edges_done: number;
  created_at: string;
  completed_at?: string | null;
}

/** POST /zones/{id}/purge — target must be an absolute path for url/prefix. */
export interface PurgeInput {
  type: "url" | "prefix" | "zone";
  target: string;
}

/** POST /purge/all — empty server_ids = every edge. */
export interface PurgeAllInput {
  server_ids?: number[];
}

// --- Bunny DNS (Phase 3) ---

export interface DnsStatus {
  configured: boolean;
  zone?: string;
  zone_id?: number;
  record_count?: number;
  reason?: string;
  error?: string;
}

/** One control-plane managed cert (metadata only — never key/chain). */
export interface TlsCert {
  name: string;
  domains: string;
  issuer: string;
  serial: string;
  staging: boolean;
  not_before?: string;
  not_after?: string;
  days_remaining?: number;
  updated_at: string;
}

export interface TlsStatus {
  managed: boolean;
  staging: boolean;
  domains: string[];
  cert_name: string;
  certs: TlsCert[];
}

/** Bunny record (PascalCase from the API). Type 0 = A. */
export interface DnsRecord {
  Id: number;
  Type: number;
  Name: string;
  Value: string;
  Ttl?: number;
  Weight?: number;
  Disabled?: boolean;
  Comment?: string;
  SmartRoutingType?: number;
  MonitorType?: number;
  MonitorStatus?: number;
}

export type DnsLockState = "locked" | "pending" | "unlocked";

export interface DnsLock {
  locked: boolean;
  state: DnsLockState;
  unlock_delay_seconds: number;
  updated_at: string;
  unlock_requested_at?: string;
  unlock_available_at?: string;
  seconds_remaining?: number;
}

export interface AddDnsRecordInput {
  name: string;
  value: string; // IPv4
  ttl?: number;
  weight?: number;
  comment?: string;
}

// --- Health, rotation & drain (Phase 3 Steps 4-5) ---

export type RotationReason = "in_rotation" | "drained" | "unhealthy" | "offline" | string;

/** One row of GET /health/status. */
export interface EdgeHealth {
  server_id: number;
  edge_id: string;
  host: string;
  status: string; // server lifecycle status
  state: "healthy" | "unhealthy" | "unknown" | string;
  healthy: boolean;
  in_rotation: boolean;
  rotation_reason: RotationReason;
  check_enabled: boolean;
  consecutive_fails: number;
  consecutive_successes: number;
  last_probe?: string;
  last_latency_ms: number;
  last_error?: string;
  probing: boolean;
  interval_seconds: number;
  fail_threshold: number;
  rise_threshold: number;
}

export interface HealthStatusResponse {
  checker_enabled: boolean;
  edges: EdgeHealth[];
}

export interface HealthConfigDefaults {
  enabled: boolean;
  interval_sec: number;
  timeout_sec: number;
  fail_threshold: number;
  rise_threshold: number;
  path: string;
  scheme: string;
  port: number;
  ttl_seconds: number;
}

export interface HealthConfigServer {
  server_id: number;
  edge_id: string;
  check_enabled: boolean;
  interval_sec: number;
  fail_threshold: number;
  rise_threshold: number;
  overridden: boolean;
}

export interface HealthConfigResponse {
  defaults: HealthConfigDefaults;
  servers: HealthConfigServer[];
}

/** GET /dns/routing — effective per-PoP routing view. */
export interface DnsRegion {
  region: string;
  lat: number;
  long: number;
  latency_zone: string;
  label: string;
}

export interface DnsRoutingServer {
  edge_id: string;
  region: string;
  label?: string;
  mode: "geographic" | "latency" | string;
  mapped: boolean;
  online: boolean;
  weight: number;
  lat?: number;
  long?: number;
  latency_zone?: string;
  overridden: boolean;
}

export interface DnsRouting {
  mode: "geographic" | "latency" | string;
  record: string;
  ttl: number;
  dns_enabled: boolean;
  regions: DnsRegion[];
  servers: DnsRoutingServer[];
  latency_note?: string;
  // Bunny native DNS monitor (Layer-2 failover backstop): whether Brisk stamps a
  // record monitor + which method ("ping" | "http"; "" when off).
  bunny_monitor?: { enabled: boolean; method: string };
}

/** GET /servers/{id}/rotation. */
export interface ServerRotation {
  edge_id: string;
  in_rotation: boolean;
  reason: RotationReason;
  drained: boolean;
  drain_reason: string;
  health: string;
  status: string;
}

/** Recent routing events from GET /dns/audit. */
export interface DnsAuditEntry {
  id: number;
  action: string; // add | enable | disable | update | remove | drain | undrain
  edge_id: string;
  record_name: string;
  value: string;
  record_id?: number | null;
  reason: string;
  created_at: string;
}

export interface DrainInput {
  reason?: string;
  force?: boolean;
}

export interface SetRoutingInput {
  weight?: number;
  override?: string; // "" | geographic | latency
  // Capacity-weighted routing (opt-in); both optional so a weight/override save can omit them.
  capacity_mbps?: number;
  weight_by_capacity?: boolean;
}

export interface SetHealthInput {
  enabled?: boolean;
  interval_seconds?: number;
  fail_threshold?: number;
  rise_threshold?: number;
}

/** One structured edge access-log row (Phase 4 Step 6). Shipped by the agent from
    each edge's JSON access log into the request_logs hypertable. */
export interface RequestLog {
  ts: string;
  server_id?: number | null;
  edge_id: string;
  zone_id?: number | null;
  client_ip: string;
  country: string;
  method: string;
  host: string;
  path: string;
  status: number;
  bytes: number;
  cache_status: string; // HIT | MISS | BYPASS | EXPIRED | STALE | -
  request_time: number; // total edge time (s)
  upstream_time: number; // origin/shield time (s)
  referer: string;
  user_agent: string;
  request_id: string;
}

/** GET /logs (admin) or GET /zones/{id}/logs (tenant) — newest first. */
export interface LogsResponse {
  logs: RequestLog[];
  from: string;
  to: string;
}

/** Real per-request aggregates over request_logs (Phase 4 Step 6, Parts 3+4):
    origin offload, status breakdown, latency percentiles, top paths/countries. */
export interface LabeledCount {
  label: string;
  count: number;
}
export interface PathStat {
  path: string;
  count: number;
  bytes: number;
}
export interface CacheBreakdown {
  hit: number;
  miss: number; // origin-fetched
  bypass: number;
  other: number;
  hit_bytes: number;
  total_bytes: number;
  offload_req: number; // hit/(hit+miss) by request count
  offload_bytes: number; // hit_bytes/total_bytes
}
export interface LatencyStats {
  p50: number; // seconds
  p95: number;
  p99: number;
  avg: number;
}
export interface LogAnalytics {
  total: number;
  status_classes: LabeledCount[];
  cache: CacheBreakdown;
  latency: LatencyStats;
  top_paths: PathStat[];
  top_countries: LabeledCount[];
}
/** GET /logs/analytics (admin) or /zones/{id}/logs/analytics (tenant). */
export interface LogAnalyticsResponse {
  analytics: LogAnalytics;
  from: string;
  to: string;
}

/** Client-side filter for the Logs page (maps to query params). */
export interface LogFilter {
  zone_id?: number; // 0/undefined = all zones
  status?: string; // "" | "2"/"4"/"5" class | exact e.g. "404"
  cache?: string; // "" | HIT | MISS | BYPASS | EXPIRED | STALE
  path?: string; // substring
  ip?: string; // exact client IP
  country?: string; // ISO code
  from?: string; // RFC3339 window start
  limit?: number; // 1..1000 (server clamps; default 200)
}
