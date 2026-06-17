// Package customdomains runs the Phase 4 Step 2 custom-domain lifecycle:
// DNS verification (this file) gating per-domain ACME issuance + renewal (manager.go).
package customdomains

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"
)

// publicResolverAddr is a public recursive resolver. We verify against what the
// PUBLIC internet (and the CA) sees — NOT the container's 127.0.0.11 Docker DNS,
// which can't resolve external customer domains and caches poorly. Cloudflare 1.1.1.1.
const publicResolverAddr = "1.1.1.1:53"

// Verifier resolves DNS to confirm a custom domain is routed at Brisk before any
// ACME attempt (the gate that protects the LE failed-validation rate limit and
// blocks issuing certs for domains a tenant doesn't actually control/route).
type Verifier struct {
	resolver *net.Resolver
}

// NewVerifier returns a Verifier that queries a public recursive resolver.
func NewVerifier() *Verifier {
	r := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: 5 * time.Second}
			// Force the public resolver regardless of the host's resolv.conf.
			return d.DialContext(ctx, network, publicResolverAddr)
		},
	}
	return &Verifier{resolver: r}
}

// Result is the outcome of a verification attempt.
type Result struct {
	OK     bool
	Detail string // human-readable, surfaced as last_error on failure / status detail on success
}

// Verify reports whether domain's DNS lands on Brisk. It passes when EITHER:
//   - the domain's CNAME chain resolves to one of expectCNAMEs (the zone's CDN
//     hostname or the geo routing record), OR
//   - the domain's A/AAAA records resolve to a Brisk edge IP (expectIPs) — this
//     covers apex ALIAS/flattening and a CNAME pointed straight at the geo set.
//
// expectCNAMEs/expectIPs are matched case-insensitively with trailing dots
// normalized. A miss returns OK=false with a Detail explaining what was seen, so
// the dashboard can show actionable guidance instead of a silent retry.
func (v *Verifier) Verify(ctx context.Context, domain string, expectCNAMEs, expectIPs []string) Result {
	domain = normalizeHost(domain)
	wantNames := normalizeSet(expectCNAMEs)
	wantIPs := toSet(expectIPs)

	// 1) CNAME chain: Go's LookupCNAME follows the chain and returns the final
	//    canonical name (or the domain itself if there's no CNAME).
	cname, cerr := v.resolver.LookupCNAME(ctx, domain)
	if cerr == nil {
		cn := normalizeHost(cname)
		if cn != "" && cn != domain && wantNames[cn] {
			return Result{OK: true, Detail: fmt.Sprintf("CNAME -> %s (Brisk)", cn)}
		}
	}

	// 2) Resolved addresses: do they land on a Brisk edge? Covers ALIAS/flattening
	//    (apex) and a CNAME to the geo routing record (which itself A's to edges).
	ips, ierr := v.resolver.LookupHost(ctx, domain)
	matched := []string{}
	for _, ip := range ips {
		if wantIPs[ip] {
			matched = append(matched, ip)
		}
	}
	if len(matched) > 0 {
		return Result{OK: true, Detail: fmt.Sprintf("resolves to Brisk edge(s) %s", strings.Join(matched, ", "))}
	}

	// Not pointed at Brisk (yet). Build an honest detail.
	switch {
	case cerr != nil && ierr != nil:
		return Result{OK: false, Detail: fmt.Sprintf("domain does not resolve yet (%v)", ierr)}
	case cname != "" && normalizeHost(cname) != domain:
		return Result{OK: false, Detail: fmt.Sprintf("CNAME points to %s, not a Brisk hostname; create the CNAME to your Brisk CDN hostname", normalizeHost(cname))}
	case len(ips) > 0:
		return Result{OK: false, Detail: fmt.Sprintf("resolves to %s, not a Brisk edge; point the domain (CNAME) at your Brisk CDN hostname", strings.Join(ips, ", "))}
	default:
		return Result{OK: false, Detail: "no DNS records found yet; create the CNAME and allow time to propagate"}
	}
}

// normalizeHost lowercases a hostname and strips a trailing dot.
func normalizeHost(h string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(h)), ".")
}

func normalizeSet(in []string) map[string]bool {
	out := make(map[string]bool, len(in))
	for _, s := range in {
		if n := normalizeHost(s); n != "" {
			out[n] = true
		}
	}
	return out
}

func toSet(in []string) map[string]bool {
	out := make(map[string]bool, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out[s] = true
		}
	}
	return out
}
