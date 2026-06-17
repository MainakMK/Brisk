// Package purge removes cached objects from the edge's open-source Nginx cache.
//
// Open-source Nginx has no built-in purge directive (that is NGINX Plus). The
// ngx_cache_purge module exists, but its WILDCARD purge needs Nginx's background
// cache-purger process (proxy_cache_path purger=on) to actually evict matched
// entries — and that parameter only exists when the module is *statically*
// patched into Nginx, not as the dynamic module Brisk loads. Empirically the
// dynamic module's wildcard eviction is partial/unreliable, which is unacceptable
// for sliced video (one URL -> many $slice_range entries).
//
// So Brisk purges the way CLAUDE.md sanctions ("the agent purges by deleting
// cache files"): it scans the cache directory and deletes every file whose stored
// cache KEY matches the purge, by prefix. This is fully reliable, clears ALL
// slices of a video in one pass (slice keys are "<host><uri><slice_range>", all
// sharing the "<host><uri>" prefix), and needs no extra Nginx module. After a
// file is unlinked, the next request is a MISS (Nginx re-fetches from origin).
package purge

import (
	"bytes"
	"context"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// --- NATS wire contract (must match brisk-control/internal/purge) ---

// Message is the JSON payload delivered over JetStream. For type "all",
// Hosts/Target are empty (purge the entire cache).
type Message struct {
	Type   string   `json:"type"`             // url | prefix | zone | all
	Hosts  []string `json:"hosts,omitempty"`  // zone hostnames whose cache to purge
	Target string   `json:"target,omitempty"` // path (url/prefix) or "/" (zone)
	JobID  int64    `json:"job_id"`
	ZoneID int64    `json:"zone_id,omitempty"`
}

// EdgeSubject returns this edge's purge subject. Must match the control plane.
func EdgeSubject(edgeID string) string { return "brisk.purge.edge." + SanitizeToken(edgeID) }

// SanitizeToken makes s a valid single NATS subject token. Must match the
// control plane's identical function so subjects line up.
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

// --- purger ---

// Purger applies a purge to the local cache.
type Purger interface {
	Purge(ctx context.Context, m Message) error
}

// keyHeaderScan is how many bytes of a cache file we read to find the KEY line.
// Nginx writes a small binary header then "\nKEY: <key>\n" before the response.
const keyHeaderScan = 2048

// FilePurger deletes Nginx cache files whose stored KEY matches the purge.
type FilePurger struct {
	cacheDir string
}

// NewFilePurger builds a purger over the on-disk cache at cacheDir.
func NewFilePurger(cacheDir string) *FilePurger {
	if cacheDir == "" {
		cacheDir = "/var/cache/brisk"
	}
	return &FilePurger{cacheDir: cacheDir}
}

// Purge deletes cache files matching the message:
//   - all    -> every cached file
//   - zone   -> keys starting "<host>/"        (whole zone, all hosts)
//   - prefix -> keys starting "<host><prefix>" (path prefix)
//   - url    -> keys starting "<host><uri>"    (the file AND all its slices)
func (p *FilePurger) Purge(_ context.Context, m Message) error {
	matchAll := m.Type == "all"

	var prefixes []string
	if !matchAll {
		target := m.Target
		if m.Type == "zone" || target == "" {
			target = "/"
		}
		if !strings.HasPrefix(target, "/") {
			target = "/" + target
		}
		target = strings.TrimSuffix(target, "*") // wildcard is implicit (prefix match)
		for _, h := range m.Hosts {
			if h != "" {
				prefixes = append(prefixes, h+target)
			}
		}
		if len(prefixes) == 0 {
			return nil // nothing to match
		}
	}

	deleted := 0
	walkErr := filepath.WalkDir(p.cacheDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries; never fail the whole purge
		}
		if d.IsDir() || d.Name() == "" {
			return nil
		}
		// Skip Nginx temp files.
		if strings.Contains(path, "/temp/") {
			return nil
		}
		match := matchAll
		if !match {
			key, ok := readCacheKey(path)
			if !ok {
				return nil
			}
			for _, pre := range prefixes {
				if strings.HasPrefix(key, pre) {
					match = true
					break
				}
			}
		}
		if match {
			if err := os.Remove(path); err == nil {
				deleted++
			}
		}
		return nil
	})
	log.Printf("purge: deleted %d cache file(s) for type=%s target=%q hosts=%v", deleted, m.Type, m.Target, m.Hosts)
	return walkErr
}

// readCacheKey extracts the cache KEY from an Nginx cache file (the "KEY: <key>"
// line near the start). Returns false if the file isn't a recognizable cache file.
func readCacheKey(path string) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	buf := make([]byte, keyHeaderScan)
	n, _ := io.ReadFull(f, buf)
	if n <= 0 {
		return "", false
	}
	data := buf[:n]
	marker := []byte("\nKEY: ")
	idx := bytes.Index(data, marker)
	if idx < 0 {
		return "", false
	}
	start := idx + len(marker)
	end := bytes.IndexByte(data[start:], '\n')
	if end < 0 {
		return "", false
	}
	return string(data[start : start+end]), true
}
