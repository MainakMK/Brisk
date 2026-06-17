package logship

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestParseLine proves the nginx brisk_json line -> Entry mapping: RFC3339 ts,
// the GeoIP "-" placeholder maps to empty country, and the quoted rt/urt timing
// fields parse leniently ("" -> 0, a list -> first value).
func TestParseLine(t *testing.T) {
	line := []byte(`{"ts":"2026-06-11T08:00:00+00:00","ip":"203.0.113.7","country":"-",` +
		`"method":"GET","host":"cdn.example.com","path":"/a.ts","status":206,"bytes":1234,` +
		`"cache":"HIT","rt":"0.005","urt":"","ref":"-","ua":"curl/8","rid":"r1"}`)
	e, ok := parseLine(line)
	if !ok {
		t.Fatal("parseLine returned !ok for a valid line")
	}
	if e.Zone != "cdn.example.com" || e.ClientIP != "203.0.113.7" || e.Path != "/a.ts" {
		t.Errorf("host/ip/path wrong: %+v", e)
	}
	if e.Country != "" {
		t.Errorf("GeoIP placeholder %q must map to empty country, got %q", "-", e.Country)
	}
	if e.Status != 206 || e.Bytes != 1234 || e.CacheStatus != "HIT" {
		t.Errorf("status/bytes/cache wrong: %+v", e)
	}
	if e.RequestTime != 0.005 || e.UpstreamTime != 0 {
		t.Errorf("timing wrong: rt=%v urt=%v", e.RequestTime, e.UpstreamTime)
	}
	if !e.TS.Equal(time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)) {
		t.Errorf("ts not parsed as RFC3339 UTC: %v", e.TS)
	}

	// urt as a multi-upstream list -> first value; non-JSON -> skipped.
	if e2, _ := parseLine([]byte(`{"ts":"x","host":"h","rt":"0.10","urt":"0.20, 0.30"}`)); e2.UpstreamTime != 0.20 {
		t.Errorf("urt list should take first value, got %v", e2.UpstreamTime)
	}
	if _, ok := parseLine([]byte(`not json`)); ok {
		t.Error("non-JSON line must be rejected")
	}
}

// TestReadNewTail proves the tail semantics: the first read skips history (we never
// ship lines that predate the agent), only complete (newline-terminated) lines are
// shipped, and a partial trailing line is held until its newline arrives.
func TestReadNewTail(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "brisk.requests.log")
	hist := `{"ts":"2026-06-11T07:00:00+00:00","host":"old.example.com","ip":"10.0.0.1","path":"/old","status":200,"bytes":1,"cache":"MISS","rt":"0.001","urt":""}` + "\n"
	if err := os.WriteFile(p, []byte(hist), 0o644); err != nil {
		t.Fatal(err)
	}

	var shipped []Entry
	ship := func(_ context.Context, body []byte) error {
		var batch []Entry
		if err := json.Unmarshal(body, &batch); err != nil {
			return err
		}
		shipped = append(shipped, batch...)
		return nil
	}
	s := New(p, ship, time.Second)

	// First read: skip to EOF, ship nothing (history not shipped).
	s.readNew()
	s.flush(context.Background())
	if len(shipped) != 0 {
		t.Fatalf("first read must skip history, shipped %d", len(shipped))
	}

	// Append two complete lines + a partial (no trailing newline).
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"ts":"2026-06-11T08:00:01+00:00","host":"a.example.com","ip":"203.0.113.7","path":"/a.ts","status":206,"bytes":100,"cache":"HIT","rt":"0.005","urt":""}` + "\n")
	f.WriteString(`{"ts":"2026-06-11T08:00:02+00:00","host":"a.example.com","ip":"203.0.113.8","path":"/b.ts","status":200,"bytes":200,"cache":"MISS","rt":"0.030","urt":"0.025"}` + "\n")
	f.WriteString(`{"ts":"2026-06-11T08:00:03+00:00","host":"a.example.com","path":"/partial"`) // no newline yet
	f.Close()

	s.readNew()
	s.flush(context.Background())
	if len(shipped) != 2 {
		t.Fatalf("expected 2 complete lines shipped, got %d: %+v", len(shipped), shipped)
	}
	if shipped[0].Path != "/a.ts" || shipped[1].Path != "/b.ts" {
		t.Errorf("wrong lines shipped: %+v", shipped)
	}
	if shipped[1].UpstreamTime != 0.025 {
		t.Errorf("upstream_time not carried: %v", shipped[1].UpstreamTime)
	}

	// Complete the partial line — now it ships.
	f, _ = os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString(`,"status":404,"bytes":0,"cache":"-","rt":"0.001","urt":""}` + "\n")
	f.Close()
	s.readNew()
	s.flush(context.Background())
	if len(shipped) != 3 || shipped[2].Path != "/partial" || shipped[2].Status != 404 {
		t.Fatalf("completed partial line should ship once whole: %+v", shipped)
	}
}

// TestReadNewTruncate proves rotation handling: if the file shrinks below the saved
// offset (truncated/rotated), the offset resets so we don't seek past EOF.
func TestReadNewTruncate(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "brisk.requests.log")
	os.WriteFile(p, []byte(`{"ts":"2026-06-11T08:00:00+00:00","host":"h","path":"/1","status":200,"bytes":1,"cache":"HIT","rt":"0.001","urt":""}`+"\n"), 0o644)

	var shipped []Entry
	ship := func(_ context.Context, body []byte) error {
		var b []Entry
		json.Unmarshal(body, &b)
		shipped = append(shipped, b...)
		return nil
	}
	s := New(p, ship, time.Second)
	s.readNew() // skip history -> offset at EOF (a non-zero offset)

	// Model logrotate copytruncate: the file is truncated to 0 first. A tick here
	// sees size(0) < offset and must reset the offset (the size<offset guard).
	if err := os.Truncate(p, 0); err != nil {
		t.Fatal(err)
	}
	s.readNew()

	// Then the file regrows with fresh lines; the next tick reads them from 0.
	os.WriteFile(p, []byte(`{"ts":"2026-06-11T08:01:00+00:00","host":"h","path":"/fresh","status":200,"bytes":2,"cache":"MISS","rt":"0.002","urt":""}`+"\n"), 0o644)
	s.readNew()
	s.flush(context.Background())
	if len(shipped) != 1 || shipped[0].Path != "/fresh" {
		t.Fatalf("after truncation the fresh line should ship exactly once: %+v", shipped)
	}
}
