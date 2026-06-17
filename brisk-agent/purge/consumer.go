package purge

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const (
	streamName     = "BRISK_PURGE"
	streamSubjects = "brisk.purge.>"
)

// AckFunc reports a completed purge back to the control plane (best-effort).
type AckFunc func(ctx context.Context, jobID int64) error

// Consumer subscribes to this edge's purge subject on JetStream and applies each
// purge via the Purger, acking only AFTER a successful apply. Because it uses a
// DURABLE consumer with explicit acks, a purge published while the agent was
// offline is redelivered on reconnect — so no purge is ever missed.
type Consumer struct {
	natsURL string
	edgeID  string
	purger  Purger
	ack     AckFunc
}

// NewConsumer builds a purge consumer. ack may be nil (no completion reporting).
func NewConsumer(natsURL, edgeID string, purger Purger, ack AckFunc) *Consumer {
	return &Consumer{natsURL: natsURL, edgeID: edgeID, purger: purger, ack: ack}
}

// Run connects to NATS and consumes purge messages until ctx is cancelled. It
// blocks; run it in its own goroutine. NATS auto-reconnects; the durable
// consumer resumes (and replays unacked messages) after any disconnect.
func (c *Consumer) Run(ctx context.Context) error {
	nc, err := nats.Connect(c.natsURL,
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2*time.Second),
		nats.Name("brisk-agent:"+c.edgeID),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			log.Printf("purge: nats disconnected: %v", err)
		}),
		nats.ReconnectHandler(func(_ *nats.Conn) {
			log.Printf("purge: nats reconnected (replaying any missed purges)")
		}),
	)
	if err != nil {
		return fmt.Errorf("nats connect: %w", err)
	}
	defer nc.Drain()

	js, err := jetstream.New(nc)
	if err != nil {
		return fmt.Errorf("jetstream: %w", err)
	}

	// Ensure the stream exists (idempotent; the control plane also ensures it, but
	// the agent may boot first). Then bind a durable, explicit-ack consumer
	// filtered to THIS edge's subject only.
	sctx, scancel := context.WithTimeout(ctx, 15*time.Second)
	stream, err := js.CreateOrUpdateStream(sctx, jetstream.StreamConfig{
		Name:      streamName,
		Subjects:  []string{streamSubjects},
		Storage:   jetstream.FileStorage,
		Retention: jetstream.LimitsPolicy,
		MaxAge:    24 * time.Hour,
	})
	scancel()
	if err != nil {
		return fmt.Errorf("ensure stream: %w", err)
	}

	subject := EdgeSubject(c.edgeID)
	cctx, ccancel := context.WithTimeout(ctx, 15*time.Second)
	cons, err := stream.CreateOrUpdateConsumer(cctx, jetstream.ConsumerConfig{
		Durable:       "agent-" + SanitizeToken(c.edgeID),
		AckPolicy:     jetstream.AckExplicitPolicy,
		FilterSubject: subject,
		MaxDeliver:    -1, // retry forever until acked
		AckWait:       30 * time.Second,
	})
	ccancel()
	if err != nil {
		return fmt.Errorf("ensure consumer: %w", err)
	}

	log.Printf("purge: listening on %s (durable agent-%s)", subject, SanitizeToken(c.edgeID))

	cc, err := cons.Consume(func(msg jetstream.Msg) {
		c.handle(ctx, msg)
	})
	if err != nil {
		return fmt.Errorf("consume: %w", err)
	}
	defer cc.Stop()

	<-ctx.Done()
	return ctx.Err()
}

// handle applies one purge and acks only on success (so JetStream redelivers on
// failure). Bad/undecodable messages are terminated (won't redeliver forever).
func (c *Consumer) handle(ctx context.Context, msg jetstream.Msg) {
	var m Message
	if err := json.Unmarshal(msg.Data(), &m); err != nil {
		log.Printf("purge: bad message (terminating): %v", err)
		_ = msg.Term() // poison message — don't redeliver
		return
	}

	actx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := c.purger.Purge(actx, m); err != nil {
		log.Printf("purge: apply failed (will retry in 2s): %v", err)
		_ = msg.NakWithDelay(2 * time.Second) // redeliver after a delay (no tight storm)
		return
	}

	if err := msg.Ack(); err != nil {
		log.Printf("purge: ack failed: %v", err)
		return
	}
	log.Printf("purge: applied type=%s target=%q hosts=%v job=%d", m.Type, m.Target, m.Hosts, m.JobID)

	if c.ack != nil && m.JobID > 0 {
		if err := c.ack(actx, m.JobID); err != nil {
			log.Printf("purge: completion ack to control plane failed: %v", err)
		}
	}
}
