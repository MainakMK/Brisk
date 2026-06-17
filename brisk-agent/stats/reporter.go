package stats

import (
	"context"
	"encoding/json"
	"log"
	"time"
)

// maxBufferSamples bounds the in-memory buffer (~minutes of samples). When full,
// the oldest are dropped so a down control plane never makes the agent grow
// unbounded or block request serving.
const maxBufferSamples = 5000

// ShipFunc sends a JSON body of samples to the control plane. Decoupling via a
// func (rather than importing the client) keeps stats free of import cycles.
type ShipFunc func(ctx context.Context, body []byte) error

// Reporter collects on a ticker and ships with a bounded retry buffer.
type Reporter struct {
	collector *Collector
	ship      ShipFunc
	interval  time.Duration
	buf       []Sample
}

// NewReporter wires a collector to a ship function on the given interval.
func NewReporter(c *Collector, ship ShipFunc, interval time.Duration) *Reporter {
	if interval <= 0 {
		interval = defaultInterval
	}
	return &Reporter{collector: c, ship: ship, interval: interval}
}

// Run collects + ships every interval until ctx is cancelled.
func (r *Reporter) Run(ctx context.Context) {
	t := time.NewTicker(r.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.tick(ctx)
		}
	}
}

func (r *Reporter) tick(ctx context.Context) {
	samples, err := r.collector.Collect()
	if err != nil {
		log.Printf("stats collect failed: %v", err)
	}
	r.buf = append(r.buf, samples...)
	if len(r.buf) > maxBufferSamples {
		r.buf = r.buf[len(r.buf)-maxBufferSamples:] // drop oldest
	}
	if len(r.buf) == 0 {
		return
	}

	body, err := json.Marshal(r.buf)
	if err != nil {
		log.Printf("stats marshal failed: %v", err)
		r.buf = r.buf[:0]
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	err = r.ship(cctx, body)
	cancel()
	if err != nil {
		log.Printf("stats ship failed (buffering %d samples): %v", len(r.buf), err)
		return // keep buffer for retry/flush on reconnect
	}
	r.buf = r.buf[:0] // shipped (buffer flushed)
}
