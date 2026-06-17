package api

import (
	"strings"
	"testing"

	"brisk-control/internal/store"
)

func TestIsApex(t *testing.T) {
	cases := map[string]bool{
		"example.com":     true,  // registrable apex
		"cdn.example.com": false, // subdomain -> CNAME-able
		"foo.co.uk":       true,  // eTLD+1 under a multi-label public suffix
		"www.foo.co.uk":   false, // subdomain of the registrable apex
		"a.b.customer.io": false,
		"customer.io":     true,
	}
	for domain, want := range cases {
		if got := isApex(domain); got != want {
			t.Errorf("isApex(%q) = %v, want %v", domain, got, want)
		}
	}
}

func TestCnameInstructionApex(t *testing.T) {
	apex := cnameInstruction("example.com", "cdn.a2zjav.com", true)
	if !strings.Contains(apex, "ALIAS") || !strings.Contains(apex, "flattening") {
		t.Errorf("apex instruction should mention ALIAS/flattening: %q", apex)
	}
	if !strings.Contains(apex, "does not hand out per-edge A records") {
		t.Errorf("apex instruction must steer away from per-edge A records: %q", apex)
	}
	sub := cnameInstruction("cdn.example.com", "cdn.a2zjav.com", false)
	if !strings.Contains(sub, "CNAME") || !strings.Contains(sub, "cdn.a2zjav.com") {
		t.Errorf("subdomain instruction should give the CNAME target: %q", sub)
	}
}

// customDomainAgentZone inherits the parent's delivery settings, names itself
// after the custom domain, and is tls_mode=managed so the per-domain cert attaches.
func TestCustomDomainAgentZone(t *testing.T) {
	parent := store.Zone{
		ID: 7, CDNHostname: "cdn.a2zjav.com", OriginURL: "https://origin.internal",
		HostHeader: "www.origin.com", Video: true, Profile: "vod", PlaylistTTL: "1h",
		SegmentTTL: "12h", CorsOrigin: "*", BrotliLevel: 5, Status: "active",
	}
	cd := store.CustomDomain{Domain: "cdn.customer.com", ZoneID: 7}
	az := customDomainAgentZone(cd, parent)

	if az.CDNHostname != "cdn.customer.com" {
		t.Errorf("server_name should be the custom domain, got %q", az.CDNHostname)
	}
	if az.TLSMode != "managed" {
		t.Errorf("custom domain must be tls_mode=managed, got %q", az.TLSMode)
	}
	if az.OriginURL != parent.OriginURL || az.HostHeader != parent.HostHeader {
		t.Errorf("custom domain must inherit parent origin/host_header")
	}
	if !az.Video || az.Profile != "vod" || az.SegmentTTL != "12h" {
		t.Errorf("custom domain must inherit parent delivery settings")
	}
}

// The ETag must change when a custom-domain vhost's cert serial changes (a renewal)
// even if the real zones are unchanged — so edges re-pull the renewed cert.
func TestConfigETagSensitiveToCustomDomainCert(t *testing.T) {
	zones := []store.Zone{{ID: 7, ConfigVersion: 3}}
	base := []agentZone{
		{CDNHostname: "cdn.a2zjav.com", TLSCertSerial: "wild1"},
		{CDNHostname: "cdn.customer.com", TLSCertSerial: "certA"},
	}
	renewed := []agentZone{
		{CDNHostname: "cdn.a2zjav.com", TLSCertSerial: "wild1"},
		{CDNHostname: "cdn.customer.com", TLSCertSerial: "certB"}, // renewed serial
	}
	if configETag(zones, base) == configETag(zones, renewed) {
		t.Errorf("ETag must change when a custom-domain cert serial changes")
	}

	// Adding a custom-domain vhost must also change the ETag (new server block).
	added := append([]agentZone{}, base...)
	added = append(added, agentZone{CDNHostname: "cdn.other.com", TLSCertSerial: "certC"})
	if configETag(zones, base) == configETag(zones, added) {
		t.Errorf("ETag must change when a custom-domain vhost is added")
	}
}
