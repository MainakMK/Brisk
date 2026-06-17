// Package purge is the control plane's instant-purge channel over NATS JetStream.
//
// A purge issued from the API is recorded as a job, then published to the
// per-edge subject(s) for every edge serving the zone. JetStream is durable and
// at-least-once: an edge that was briefly disconnected receives missed purges on
// reconnect (a missed purge = stale content served — unacceptable for a CDN).
package purge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const (
	// StreamName is the JetStream stream that holds all purge messages.
	StreamName = "BRISK_PURGE"
	// SubjectPrefix is the root of every purge subject.
	SubjectPrefix = "brisk.purge"
	// streamSubjects captures every purge subject (per-edge fan-out).
	streamSubjects = SubjectPrefix + ".>"
)

// Message is the JSON payload delivered to an edge. For type "all", Hosts/Target
// are empty (the edge purges the entire cache).
type Message struct {
	Type   string   `json:"type"`             // url | prefix | zone | all
	Hosts  []string `json:"hosts,omitempty"`  // zone hostnames whose cache to purge
	Target string   `json:"target,omitempty"` // path (url/prefix) or "/" (zone)
	JobID  int64    `json:"job_id"`           // purge_jobs.id (for completion ack)
	ZoneID int64    `json:"zone_id,omitempty"`
}

// EdgeSubject returns the per-edge purge subject for an edge_id. The edge_id is
// sanitized so it is always a valid single NATS subject token. The agent derives
// the identical subject from its own edge_id.
func EdgeSubject(edgeID string) string {
	return SubjectPrefix + ".edge." + SanitizeToken(edgeID)
}

// SanitizeToken makes s safe as a single NATS subject token (no '.', ' ', '*',
// '>'). Both the publisher and the agent consumer must use this identically.
func SanitizeToken(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '-' || r == '_':
			return r
		default:
			return '_'
		}
	}, s)
}

// Publisher publishes purge messages to JetStream.
type Publisher struct {
	nc *nats.Conn
	js jetstream.JetStream
}

// New connects to NATS and ensures the BRISK_PURGE stream exists. A blank url
// returns (nil, nil) so the control plane can run without a purge channel
// (purge endpoints then report unavailable).
func New(ctx context.Context, url string) (*Publisher, error) {
	if strings.TrimSpace(url) == "" {
		return nil, nil
	}
	nc, err := nats.Connect(url,
		nats.MaxReconnects(-1),
		nats.ReconnectWait(time.Second),
		nats.Name("brisk-control"),
	)
	if err != nil {
		return nil, fmt.Errorf("nats connect: %w", err)
	}
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("jetstream: %w", err)
	}
	if err := ensureStream(ctx, js); err != nil {
		nc.Close()
		return nil, err
	}
	return &Publisher{nc: nc, js: js}, nil
}

// ensureStream creates (or updates) the durable, file-backed purge stream.
func ensureStream(ctx context.Context, js jetstream.JetStream) error {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err := js.CreateOrUpdateStream(cctx, jetstream.StreamConfig{
		Name:        StreamName,
		Subjects:    []string{streamSubjects},
		Storage:     jetstream.FileStorage, // survive restarts -> durable replay
		Retention:   jetstream.LimitsPolicy,
		MaxAge:      24 * time.Hour, // purges are only useful briefly; cap growth
		Description: "Brisk instant cache-purge channel",
	})
	if err != nil {
		return fmt.Errorf("ensure stream %s: %w", StreamName, err)
	}
	return nil
}

// Publish sends one purge message to an edge's subject and waits for the JetStream
// ack (so we know it was persisted/replicated, hence replayable).
func (p *Publisher) Publish(ctx context.Context, edgeID string, msg Message) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := p.js.Publish(cctx, EdgeSubject(edgeID), data); err != nil {
		return fmt.Errorf("publish to %s: %w", EdgeSubject(edgeID), err)
	}
	return nil
}

// Close drains and closes the NATS connection.
func (p *Publisher) Close() {
	if p != nil && p.nc != nil {
		_ = p.nc.Drain()
	}
}
