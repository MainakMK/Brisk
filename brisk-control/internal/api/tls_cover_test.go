package api

import "testing"

func TestCertCovers(t *testing.T) {
	wildcardAndApex := []string{"*.a2zjav.com", "a2zjav.com"}
	wildcardOnly := []string{"*.a2zjav.com"}

	cases := []struct {
		name    string
		domains []string
		host    string
		want    bool
	}{
		{"wildcard covers one-label sub", wildcardAndApex, "cdn.a2zjav.com", true},
		{"apex exact match", wildcardAndApex, "a2zjav.com", true},
		{"wildcard does NOT cover apex", wildcardOnly, "a2zjav.com", false},
		{"wildcard does NOT cover two-label sub", wildcardAndApex, "a.b.a2zjav.com", false},
		{"case-insensitive sub", wildcardAndApex, "CDN.A2ZJAV.COM", true},
		{"different base", wildcardAndApex, "cdn.other.com", false},
		{"empty host", wildcardAndApex, "", false},
		{"bare base is not a sub of its own wildcard", wildcardOnly, "a2zjav.com", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := certCovers(c.domains, c.host); got != c.want {
				t.Fatalf("certCovers(%v, %q) = %v, want %v", c.domains, c.host, got, c.want)
			}
		})
	}
}
