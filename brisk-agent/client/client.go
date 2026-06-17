// Package client is the edge's authenticated connection to brisk-control.
//
// HTTPClient sends authenticated heartbeats, config pulls (PullConfig), and signed
// release fetches to brisk-control. When no control plane is configured the agent
// runs standalone (the Phase 1 behavior) and this client is simply not used.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ControlPlane is the secure, authenticated link to brisk-control.
type ControlPlane interface {
	// Heartbeat posts liveness so the control plane marks the edge online.
	Heartbeat(ctx context.Context) error
}

// HTTPClient talks to the control plane over HTTP(S) with a bearer token.
type HTTPClient struct {
	BaseURL      string // e.g. https://control.example.com  (no trailing slash needed)
	Token        string // brisk_... agent token
	EdgeID       string
	AgentVersion string
	NginxVersion string
	OS           string
	Kernel       string
	GoVersion    string
	HTTP         *http.Client
}

// New returns an HTTPClient with sane defaults.
func New(baseURL, token, edgeID, agentVersion, nginxVersion, osPretty, kernel, goVersion string) *HTTPClient {
	return &HTTPClient{
		BaseURL:      strings.TrimRight(baseURL, "/"),
		Token:        token,
		EdgeID:       edgeID,
		AgentVersion: agentVersion,
		NginxVersion: nginxVersion,
		OS:           osPretty,
		Kernel:       kernel,
		GoVersion:    goVersion,
		HTTP:         &http.Client{Timeout: 10 * time.Second},
	}
}

type heartbeatBody struct {
	EdgeID       string `json:"edge_id"`
	AgentVersion string `json:"agent_version"`
	NginxVersion string `json:"nginx_version"`
	OS           string `json:"os"`
	Kernel       string `json:"kernel"`
	GoVersion    string `json:"go_version"`
}

// Heartbeat posts to /api/v1/agent/heartbeat with the bearer token.
func (c *HTTPClient) Heartbeat(ctx context.Context) error {
	body, _ := json.Marshal(heartbeatBody{
		EdgeID:       c.EdgeID,
		AgentVersion: c.AgentVersion,
		NginxVersion: c.NginxVersion,
		OS:           c.OS,
		Kernel:       c.Kernel,
		GoVersion:    c.GoVersion,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.BaseURL+"/api/v1/agent/heartbeat", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("heartbeat: status %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}

// ReleaseInfo is the control plane's answer to "what version should THIS edge run?".
type ReleaseInfo struct {
	TargetVersion string `json:"target_version"`
	URL           string `json:"url"`
	SHA256        string `json:"sha256"`
	Signature     string `json:"signature"`
	SignedBy      string `json:"signed_by"`
}

// FetchRelease asks the control plane which version this edge should be on. It returns the
// current version (no-op) unless this edge's rollout wave is open, in which case it returns the
// new version + the signed download details (which the agent re-verifies before swapping).
func (c *HTTPClient) FetchRelease(ctx context.Context) (ReleaseInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/api/v1/agent/release", nil)
	if err != nil {
		return ReleaseInfo{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return ReleaseInfo{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ReleaseInfo{}, fmt.Errorf("fetch release: status %d", resp.StatusCode)
	}
	var ri ReleaseInfo
	if err := json.NewDecoder(resp.Body).Decode(&ri); err != nil {
		return ReleaseInfo{}, err
	}
	return ri, nil
}

// DownloadBinary streams the signed agent binary for a version. The caller MUST VerifyBinary the
// bytes (sha + signature) before doing anything with them.
func (c *HTTPClient) DownloadBinary(ctx context.Context, version string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/api/v1/releases/"+version+"/binary", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	// The shared client's 10s timeout is sized for heartbeat/config calls; the signed binary is
	// ~20MB and travels over the reverse SSH tunnel, so it needs a much longer budget. Use a
	// dedicated client here (the caller's ctx still bounds the overall attempt).
	dl := &http.Client{Timeout: 5 * time.Minute}
	resp, err := dl.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download binary %s: status %d", version, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// ShipStats POSTs a JSON array of stat samples to /api/v1/agent/stats.
func (c *HTTPClient) ShipStats(ctx context.Context, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.BaseURL+"/api/v1/agent/stats", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("ship stats: status %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}

// ShipLogs POSTs a JSON array of structured access-log entries to
// /api/v1/agent/logs (Phase 4 Step 6). Same auth + tolerance as stats.
func (c *HTTPClient) ShipLogs(ctx context.Context, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.BaseURL+"/api/v1/agent/logs", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("ship logs: status %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}

// ShipSecurityEvents POSTs a JSON array of WAF firewall-log events to
// /api/v1/agent/security-events (Phase 4 Step 4). Same auth + tolerance as stats.
func (c *HTTPClient) ShipSecurityEvents(ctx context.Context, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.BaseURL+"/api/v1/agent/security-events", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("ship security events: status %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}

// AckPurge reports a completed purge job to the control plane so it can advance
// the job's completion count. Best-effort: a failure here doesn't undo the purge.
func (c *HTTPClient) AckPurge(ctx context.Context, jobID int64) error {
	body, _ := json.Marshal(map[string]int64{"job_id": jobID})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.BaseURL+"/api/v1/agent/purge/ack", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("ack purge: status %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}

// PullConfig does a conditional GET of /api/v1/agent/config. It sends
// If-None-Match with the prior (unquoted) etag and returns the HTTP status, the
// body (only on 200), and the new (unquoted) ETag. A 304 means "unchanged".
func (c *HTTPClient) PullConfig(ctx context.Context, etag string) (status int, body []byte, newETag string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/api/v1/agent/config", nil)
	if err != nil {
		return 0, nil, "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if etag != "" {
		req.Header.Set("If-None-Match", `"`+etag+`"`)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, nil, "", err
	}
	defer resp.Body.Close()

	newETag = strings.Trim(resp.Header.Get("ETag"), `"`)
	switch resp.StatusCode {
	case http.StatusOK:
		body, err = io.ReadAll(io.LimitReader(resp.Body, 4<<20)) // cap 4 MiB
		return resp.StatusCode, body, newETag, err
	case http.StatusNotModified:
		return resp.StatusCode, nil, newETag, nil
	default:
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return resp.StatusCode, nil, newETag, fmt.Errorf("pull config: status %d: %s",
			resp.StatusCode, strings.TrimSpace(string(msg)))
	}
}
