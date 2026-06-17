// Package waf is Brisk's per-zone Web Application Firewall (Phase 4 Step 4).
//
// Engine choice: OWASP Coraza (pure Go, CRS v4) embedded in the agent. Nginx
// inspects each request via auth_request -> the agent's local WAF service, which
// runs that zone's managed CRS + custom rules at the configured mode (detect or
// block). Rate limiting is Nginx-native (limit_req); this package owns rule
// inspection. Coraza is CGO-free, so the edge stays a single static binary and
// CRS v4 ships embedded — no external rule files. (ModSecurity is EOL; Coraza is
// its maintained Go successor, Apache-2.0.)
package waf

import (
	"fmt"
	"strconv"
	"strings"

	coreruleset "github.com/corazawaf/coraza-coreruleset/v4"
	"github.com/corazawaf/coraza/v3"
)

// compileCoraza builds a Coraza WAF running OWASP CRS v4 at the given paranoia
// level. The engine is always SecRuleEngine On — Brisk decides block-vs-detect
// itself from the interruption + the zone mode (so detect mode still surfaces the
// "would-block" for tuning). Body inspection is capped (request bodies are not
// deep-scanned: the auth_request hook forwards no body, and large media/video must
// not tank throughput — the CDN body-inspect cap).
func compileCoraza(paranoia int) (coraza.WAF, error) {
	if paranoia < 1 || paranoia > 4 {
		paranoia = 1
	}
	// crs-setup knobs: set the blocking + detection paranoia level, and keep the
	// inbound anomaly threshold at the CRS default (5). These SecActions run in
	// phase 1 before the CRS rules evaluate.
	directives := strings.Join([]string{
		`Include @coraza.conf-recommended`,
		`Include @crs-setup.conf.example`,
		`SecAction "id:900110,phase:1,nolog,pass,t:none,` +
			`setvar:tx.blocking_paranoia_level=` + strconv.Itoa(paranoia) + `,` +
			`setvar:tx.detection_paranoia_level=` + strconv.Itoa(paranoia) + `"`,
		`Include @owasp_crs/*.conf`,
		`SecRuleEngine On`,
		// Cap request-body inspection (defense in depth alongside auth_request body
		// off). Anything larger is not deep-scanned — protects throughput.
		`SecRequestBodyLimit 131072`,
		`SecRequestBodyNoFilesLimit 131072`,
		`SecRequestBodyLimitAction ProcessPartial`,
	}, "\n")

	waf, err := coraza.NewWAF(coraza.NewWAFConfig().
		WithRootFS(coreruleset.FS).
		WithDirectives(directives))
	if err != nil {
		return nil, fmt.Errorf("compile coraza CRS (paranoia %d): %w", paranoia, err)
	}
	return waf, nil
}
