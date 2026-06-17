package dns

import (
	_ "embed"
	"encoding/json"
	"sort"
	"strings"
	"time"
)

// RoutingMode is the Smart-Record routing strategy applied to the cdn.<zone> set.
type RoutingMode string

const (
	// ModeGeographic routes by the user's location to the edge whose coordinates
	// are closest. Default — simplest mental model (nearest by distance).
	ModeGeographic RoutingMode = "geographic"
	// ModeLatency routes by estimated network latency from the user to the nearest
	// Bunny datacenter region selected for the edge (distance != latency).
	ModeLatency RoutingMode = "latency"
)

// NormalizeMode parses a network-wide mode string, defaulting to geographic.
func NormalizeMode(s string) RoutingMode {
	if strings.EqualFold(strings.TrimSpace(s), string(ModeLatency)) {
		return ModeLatency
	}
	return ModeGeographic
}

// NormalizeOverride parses a per-server override. "" means "use the network-wide
// mode"; anything else resolves to a concrete mode (geographic|latency).
func NormalizeOverride(s string) RoutingMode {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case string(ModeLatency):
		return ModeLatency
	case string(ModeGeographic):
		return ModeGeographic
	default:
		return "" // not overridden
	}
}

// RegionLoc maps a Brisk region code to a physical location for geographic
// routing and a Bunny datacenter region for latency routing.
//
// Geographic uses Lat/Long (verified against the live Bunny API: SmartRoutingType
// 2 + GeolocationLatitude/Longitude). Latency uses LatencyZone — Bunny stores the
// string verbatim and does NOT validate it at write time, so these codes must
// match Bunny's published latency zones for routing to actually work; pick the
// Bunny region closest to the server (Bunny's guidance). Verify the codes against
// the Bunny dashboard's latency-zone selector. This map is the single place to
// extend/adjust as Brisk adds PoPs.
type RegionLoc struct {
	Lat         float64 `json:"lat"`
	Long        float64 `json:"long"`
	LatencyZone string  `json:"latency_zone"` // Bunny region code for latency mode
	Label       string  `json:"label"`
}

// legacyRegions are the CURATED codes: the exact codes the live edges use (US-NY,
// EU-FRA, IN-BLR …) plus broad continent fallbacks and a few nicer labels. They are
// overlaid ON TOP of the generated world catalogue in buildRegionMap, so a live edge's
// region code (and its coordinates) never change even as the global list grows.
var legacyRegions = map[string]RegionLoc{
	// ───────── United States (city-level; the original US-XX state codes kept) ─────────
	"US":     {39.8283, -98.5795, "US", "United States"},
	"US-NY":  {40.7128, -74.0060, "US", "New York, US"},
	"US-IL":  {41.8781, -87.6298, "US", "Chicago, US"},
	"US-CA":  {37.7749, -122.4194, "US", "San Francisco, US"},
	"US-TX":  {29.7604, -95.3698, "US", "Dallas, US"},
	"US-WA":  {47.6062, -122.3321, "US", "Seattle, US"},
	"US-IAD": {39.0438, -77.4874, "US", "Ashburn, US"},
	"US-LAX": {34.0522, -118.2437, "US", "Los Angeles, US"},
	"US-MIA": {25.7617, -80.1918, "US", "Miami, US"},
	"US-ATL": {33.7490, -84.3880, "US", "Atlanta, US"},
	"US-DEN": {39.7392, -104.9903, "US", "Denver, US"},
	"US-PHX": {33.4484, -112.0740, "US", "Phoenix, US"},
	"US-BOS": {42.3601, -71.0589, "US", "Boston, US"},
	"US-SLC": {40.7608, -111.8910, "US", "Salt Lake City, US"},
	"US-HOU": {29.7604, -95.3698, "US", "Houston, US"},
	"US-NJ":  {40.7357, -74.1724, "US", "Newark, US"},
	// ───────── Canada / Mexico / LATAM ─────────
	"CA-TOR": {43.6532, -79.3832, "CA", "Toronto, CA"},
	"CA-YVR": {49.2827, -123.1207, "CA", "Vancouver, CA"},
	"CA-YUL": {45.5017, -73.5673, "CA", "Montreal, CA"},
	"MX":     {19.4326, -99.1332, "MX", "Mexico City, MX"},
	"BR":     {-23.5505, -46.6333, "BR", "São Paulo, Brazil"},
	"BR-SAO": {-23.5505, -46.6333, "BR", "São Paulo, Brazil"},
	"BR-RIO": {-22.9068, -43.1729, "BR", "Rio de Janeiro, BR"},
	"AR":     {-34.6037, -58.3816, "AR", "Buenos Aires, AR"},
	"CL":     {-33.4489, -70.6693, "CL", "Santiago, CL"},
	"CO":     {4.7110, -74.0721, "CO", "Bogotá, CO"},
	"PE":     {-12.0464, -77.0428, "PE", "Lima, PE"},
	// ───────── Europe (EU-XX airport codes; EU = continent center) ─────────
	"EU":     {50.1109, 8.6821, "DE", "Europe"},
	"EU-FRA": {50.1109, 8.6821, "DE", "Frankfurt, EU"},
	"EU-LON": {51.5074, -0.1278, "GB", "London, EU"},
	"EU-PAR": {48.8566, 2.3522, "FR", "Paris, EU"},
	"EU-AMS": {52.3676, 4.9041, "NL", "Amsterdam, EU"},
	"EU-MAD": {40.4168, -3.7038, "ES", "Madrid, EU"},
	"EU-BCN": {41.3874, 2.1686, "ES", "Barcelona, EU"},
	"EU-MIL": {45.4642, 9.1900, "IT", "Milan, EU"},
	"EU-WAW": {52.2297, 21.0122, "PL", "Warsaw, EU"},
	"EU-STO": {59.3293, 18.0686, "SE", "Stockholm, EU"},
	"EU-HEL": {60.1699, 24.9384, "FI", "Helsinki, EU"},
	"EU-DUB": {53.3498, -6.2603, "IE", "Dublin, EU"},
	"EU-VIE": {48.2082, 16.3738, "AT", "Vienna, EU"},
	"EU-ZRH": {47.3769, 8.5417, "CH", "Zurich, EU"},
	"EU-CPH": {55.6761, 12.5683, "DK", "Copenhagen, EU"},
	"EU-OSL": {59.9139, 10.7522, "NO", "Oslo, EU"},
	"EU-PRG": {50.0755, 14.4378, "CZ", "Prague, EU"},
	"EU-BUD": {47.4979, 19.0402, "HU", "Budapest, EU"},
	"EU-OTP": {44.4268, 26.1025, "RO", "Bucharest, EU"},
	"EU-ATH": {37.9838, 23.7275, "GR", "Athens, EU"},
	"EU-LIS": {38.7223, -9.1393, "PT", "Lisbon, EU"},
	"EU-BRU": {50.8503, 4.3517, "BE", "Brussels, EU"},
	"EU-SOF": {42.6977, 23.3219, "BG", "Sofia, EU"},
	"EU-MUC": {48.1351, 11.5820, "DE", "Munich, EU"},
	// ───────── UK / Russia / Ukraine / Turkey ─────────
	"GB": {51.5074, -0.1278, "GB", "London, UK"},
	"RU": {55.7558, 37.6173, "RU", "Moscow, RU"},
	"UA": {50.4501, 30.5234, "UA", "Kyiv, UA"},
	"TR": {41.0082, 28.9784, "TR", "Istanbul, TR"},
	// ───────── Middle East ─────────
	"AE": {25.2048, 55.2708, "AE", "Dubai, UAE"},
	"IL": {32.0853, 34.7818, "IL", "Tel Aviv, IL"},
	"SA": {24.7136, 46.6753, "SA", "Riyadh, SA"},
	"QA": {25.2854, 51.5310, "QA", "Doha, QA"},
	"BH": {26.0667, 50.5577, "BH", "Manama, BH"},
	// ───────── India / South Asia ─────────
	"IN":     {20.5937, 78.9629, "IN", "India"},
	"IN-DEL": {28.6139, 77.2090, "IN", "Delhi, India"},
	"IN-BLR": {12.9716, 77.5946, "IN", "Bengaluru, India"},
	"IN-BOM": {19.0760, 72.8777, "IN", "Mumbai, India"},
	"IN-MAA": {13.0827, 80.2707, "IN", "Chennai, India"},
	"IN-HYD": {17.3850, 78.4867, "IN", "Hyderabad, India"},
	"IN-CCU": {22.5726, 88.3639, "IN", "Kolkata, India"},
	"PK":     {24.8607, 67.0011, "PK", "Karachi, PK"},
	"BD":     {23.8103, 90.4125, "BD", "Dhaka, BD"},
	"LK":     {6.9271, 79.8612, "LK", "Colombo, LK"},
	// ───────── East / Southeast Asia ─────────
	"SG":     {1.3521, 103.8198, "SG", "Singapore"},
	"JP":     {35.6762, 139.6503, "JP", "Japan"},
	"JP-TYO": {35.6762, 139.6503, "JP", "Tokyo, Japan"},
	"JP-OSA": {34.6937, 135.5023, "JP", "Osaka, Japan"},
	"HK":     {22.3193, 114.1694, "HK", "Hong Kong"},
	"KR":     {37.5665, 126.9780, "KR", "Seoul, KR"},
	"TW":     {25.0330, 121.5654, "TW", "Taipei, TW"},
	"CN":     {31.2304, 121.4737, "CN", "Shanghai, CN"},
	"CN-PEK": {39.9042, 116.4074, "CN", "Beijing, CN"},
	"TH":     {13.7563, 100.5018, "TH", "Bangkok, TH"},
	"MY":     {3.1390, 101.6869, "MY", "Kuala Lumpur, MY"},
	"ID":     {-6.2088, 106.8456, "ID", "Jakarta, ID"},
	"PH":     {14.5995, 120.9842, "PH", "Manila, PH"},
	"VN":     {10.8231, 106.6297, "VN", "Ho Chi Minh City, VN"},
	// ───────── Oceania ─────────
	"AU":     {-33.8688, 151.2093, "AU", "Sydney, Australia"},
	"AU-SYD": {-33.8688, 151.2093, "AU", "Sydney, Australia"},
	"AU-MEL": {-37.8136, 144.9631, "AU", "Melbourne, Australia"},
	"AU-PER": {-31.9505, 115.8605, "AU", "Perth, Australia"},
	"NZ":     {-36.8485, 174.7633, "NZ", "Auckland, NZ"},
	// ───────── Africa ─────────
	"ZA":     {-26.2041, 28.0473, "ZA", "Johannesburg, South Africa"},
	"ZA-CPT": {-33.9249, 18.4241, "ZA", "Cape Town, South Africa"},
	"NG":     {6.5244, 3.3792, "NG", "Lagos, NG"},
	"EG":     {30.0444, 31.2357, "EG", "Cairo, EG"},
	"KE":     {-1.2864, 36.8172, "KE", "Nairobi, KE"},

	// ───────── Hosting-provider datacenter towns (exact labels) ─────────
	// Famous DC locations that are SMALL towns next to big cities, so the population
	// filter misses them — added by hand from providers' published DC pages (Hetzner,
	// OVH, AWS, GCP, Azure, Meta, etc.) so an operator sees the exact site by name.
	// Europe
	"DE-FALKENSTEIN":   {50.4779, 12.3713, "DE", "Falkenstein (Hetzner), DE"},
	"DE-NUREMBERG":     {49.4521, 11.0767, "DE", "Nuremberg (Hetzner), DE"},
	"FR-ROUBAIX":       {50.6901, 3.1746, "FR", "Roubaix (OVH), FR"},
	"FR-GRAVELINES":    {50.9871, 2.1255, "FR", "Gravelines (OVH), FR"},
	"FR-STRASBOURG":    {48.5734, 7.7521, "FR", "Strasbourg (OVH), FR"},
	"FR-MARSEILLE":     {43.2965, 5.3698, "FR", "Marseille, FR"},
	"NL-EEMSHAVEN":     {53.4386, 6.8339, "NL", "Eemshaven (Google), NL"},
	"NL-MIDDENMEER":    {52.8089, 5.0125, "NL", "Middenmeer (Microsoft), NL"},
	"FI-HAMINA":        {60.5694, 27.1878, "FI", "Hamina (Google), FI"},
	"SE-LULEA":         {65.5848, 22.1567, "SE", "Luleå (Meta), SE"},
	"BE-SAINTGHISLAIN": {50.4500, 3.8167, "BE", "Saint-Ghislain (Google), BE"},
	"DK-FREDERICIA":    {55.5657, 9.7527, "DK", "Fredericia (Google/Apple), DK"},
	"IE-CLONEE":        {53.4106, -6.4406, "IE", "Clonee (Meta), IE"},
	"GB-NEWPORT":       {51.5842, -2.9977, "GB", "Newport, GB"},
	"GB-SLOUGH":        {51.5105, -0.5950, "GB", "Slough, GB"},
	"ES-ZARAGOZA":      {41.6488, -0.8891, "ES", "Zaragoza (AWS), ES"},
	// North America (US/CA hosting towns)
	"CA-BEAUHARNOIS":   {45.3151, -73.8779, "CA", "Beauharnois (OVH), CA"},
	"US-COUNCILBLUFFS": {41.2619, -95.8608, "US", "Council Bluffs (Google), US"},
	"US-THEDALLES":     {45.5946, -121.1787, "US", "The Dalles (Google), US"},
	"US-MONCKSCORNER":  {33.1960, -79.9968, "US", "Moncks Corner (Google), US"},
	"US-LENOIR":        {35.9140, -81.5390, "US", "Lenoir (Google), US"},
	"US-HENDERSON":     {36.0397, -114.9819, "US", "Henderson (Google), US"},
	"US-PRYOR":         {36.3076, -95.3169, "US", "Pryor (Google), US"},
	"US-MIDLOTHIAN":    {32.4824, -96.9944, "US", "Midlothian (Google), US"},
	"US-NEWCARLISLE":   {41.7017, -86.5089, "US", "New Carlisle (Google), US"},
	"US-MESA":          {33.4152, -111.8315, "US", "Mesa (Google), US"},
	"US-QUINCY":        {47.2343, -119.8527, "US", "Quincy (Azure), US"},
	"US-BOYDTON":       {36.6676, -78.3875, "US", "Boydton (Azure), US"},
	"US-CHEYENNE":      {41.1400, -104.8202, "US", "Cheyenne (Microsoft), US"},
	"US-GOODYEAR":      {33.4353, -112.3576, "US", "Goodyear (Microsoft), US"},
	"US-DESMOINES":     {41.5868, -93.6250, "US", "Des Moines (Microsoft), US"},
	"US-COLUMBUS":      {39.9612, -82.9988, "US", "Columbus (AWS), US"},
	"US-BOARDMAN":      {45.8390, -119.7006, "US", "Boardman (AWS), US"},
	"US-PRINEVILLE":    {44.2998, -120.8345, "US", "Prineville (Meta), US"},
	"US-ALTOONA":       {41.6447, -93.4646, "US", "Altoona (Meta), US"},
	"US-PAPILLION":     {41.1544, -96.0422, "US", "Papillion (Meta), US"},
	"US-EAGLEMTN":      {40.3141, -112.0069, "US", "Eagle Mountain (Meta), US"},
	"US-FORESTCITY":    {35.3340, -81.8651, "US", "Forest City (Meta), US"},
	"US-NEWALBANY":     {40.0817, -82.8088, "US", "New Albany, US"},
	"US-STERLING":      {39.0062, -77.4286, "US", "Sterling, US"},
	"US-HILLSBORO":     {45.5229, -122.9898, "US", "Hillsboro, US"},
	"US-SANTACLARA":    {37.3541, -121.9552, "US", "Santa Clara, US"},
	"US-RENO":          {39.5349, -119.4517, "US", "Tahoe-Reno (Switch), US"},
	// Asia-Pacific / LATAM hosting towns
	"MX-QUERETARO": {20.5888, -100.3899, "MX", "Querétaro, MX"},
	"CL-QUILICURA": {-33.3500, -70.7333, "CL", "Quilicura (Google), CL"},
	"MY-JOHOR":     {1.4927, 103.7414, "MY", "Johor Bahru, MY"},
	"TW-CHANGHUA":  {24.0518, 120.5161, "TW", "Changhua (Google), TW"},
	"KR-CHUNCHEON": {37.8813, 127.7298, "KR", "Chuncheon (Naver), KR"},
	"JP-INZAI":     {35.8326, 140.1437, "JP", "Inzai (Tokyo), JP"},
}

// regionsCatalogue is the generated world-cities routing catalogue (~670 cities,
// every country) compiled from a public population dataset (see _gen_regions.go). It
// gives "full world coverage" so an operator can pick the actual city of any server.
//
//go:embed regions.json
var regionsCatalogue []byte

// RegionMap is the full routing catalogue used everywhere (LookupRegion, RegionList,
// smartFieldsFor): the embedded world catalogue, with legacyRegions overlaid so the
// curated + live-edge codes always win. Built once at package init.
var RegionMap = buildRegionMap()

func buildRegionMap() map[string]RegionLoc {
	m := make(map[string]RegionLoc, 800)
	var cat []struct {
		Code, Cc, Label string
		Lat, Long       float64
	}
	_ = json.Unmarshal(regionsCatalogue, &cat) // fields match case-insensitively
	for _, c := range cat {
		if c.Code == "" {
			continue
		}
		m[c.Code] = RegionLoc{Lat: c.Lat, Long: c.Long, LatencyZone: c.Cc, Label: c.Label}
	}
	for k, v := range legacyRegions {
		m[k] = v // curated + live-edge codes take precedence over the generated set
	}
	return m
}

// LookupRegion resolves a server region to a location, trying the most specific
// key first and falling back to broader prefixes: "US-IL-foo" -> "US-IL" -> "US".
// Returns ok=false when nothing matches (caller treats the edge as unmapped).
func LookupRegion(region string) (RegionLoc, bool) {
	key := strings.ToUpper(strings.TrimSpace(region))
	if key == "" {
		return RegionLoc{}, false
	}
	if loc, ok := RegionMap[key]; ok {
		return loc, true
	}
	parts := strings.Split(key, "-")
	for i := len(parts) - 1; i >= 1; i-- {
		if loc, ok := RegionMap[strings.Join(parts[:i], "-")]; ok {
			return loc, true
		}
	}
	return RegionLoc{}, false
}

// resolveMode picks the effective mode for an endpoint: a per-server override
// wins over the network-wide default.
func resolveMode(networkMode RoutingMode, override string) RoutingMode {
	if o := NormalizeOverride(override); o != "" {
		return o
	}
	if networkMode == "" {
		return ModeGeographic
	}
	return networkMode
}

// smartFieldsFor computes the Smart-Record fields to write for an endpoint under
// the given network-wide mode. An unmapped region yields SmartNone (the record
// still serves as a plain member of the set, it just isn't geo/latency-weighted).
func smartFieldsFor(ep Endpoint, networkMode RoutingMode) (srt int, lat, long float64, latZone string) {
	loc, ok := LookupRegion(ep.Region)
	if !ok {
		return SmartNone, 0, 0, ""
	}
	switch resolveMode(networkMode, ep.RoutingOverride) {
	case ModeLatency:
		return SmartLatency, 0, 0, loc.LatencyZone
	default:
		return SmartGeolocation, loc.Lat, loc.Long, ""
	}
}

// normWeight clamps a weight into Bunny's 0-100 range, defaulting empty to 100.
func normWeight(w int) int {
	if w <= 0 {
		return 100
	}
	if w > 100 {
		return 100
	}
	return w
}

// maxCapacity returns the largest CapacityMbps among capacity-weighted endpoints in the
// set (0 if none opt in). It's the normalization reference for effectiveWeight.
func maxCapacity(eps []Endpoint) int {
	m := 0
	for _, ep := range eps {
		if ep.WeightByCapacity && ep.CapacityMbps > m {
			m = ep.CapacityMbps
		}
	}
	return m
}

// effectiveWeight is the Smart-Record weight to write for an endpoint. When the PoP opts
// into capacity weighting (and a reference max exists), the weight is its capacity
// normalized to the biggest capacity-weighted box, scaled to 1-100 — so a 10 Gbps box
// gets ~10x the share of a 1 Gbps box. Otherwise it's the manual weight (today's
// behavior, so a fleet with nobody opted in renders byte-identical). maxCap is
// maxCapacity(endpoints), computed once per reconcile.
//
// On top of that BASE weight, the live load-feedback factor (#3) is applied last: a
// hot edge's factor (<1) scales its weight down so it bleeds share to cooler peers,
// while a cool edge (factor 1.0, or no load data) keeps its full base. The result is
// floored at 1 so load alone never blackholes an edge (drain/health own full removal).
func effectiveWeight(ep Endpoint, maxCap int) int {
	return applyLoadFactor(baseWeight(ep, maxCap), ep.LoadFactor)
}

// baseWeight is the capacity- or manual-derived weight, BEFORE the live load factor.
func baseWeight(ep Endpoint, maxCap int) int {
	if ep.WeightByCapacity && ep.CapacityMbps > 0 && maxCap > 0 {
		w := (ep.CapacityMbps*100 + maxCap/2) / maxCap // integer round
		if w < 1 {
			w = 1
		}
		if w > 100 {
			w = 100
		}
		return w
	}
	return normWeight(ep.Weight)
}

// applyLoadFactor scales a base weight by the live load-steering factor. A factor of 0
// (no load data) or >=1 (cool) leaves the weight unchanged — so load steering off is
// byte-identical. A factor in (0,1) reduces the weight (hot edge), rounded to nearest
// and floored at 1 (never a load-induced blackhole).
func applyLoadFactor(base int, factor float64) int {
	if factor <= 0 || factor >= 1 {
		return base
	}
	w := int(float64(base)*factor + 0.5) // round to nearest
	if w < 1 {
		w = 1
	}
	return w
}

// Effective is the read-only, resolved routing view for one edge (for the
// /dns/routing endpoint so Step 5's UI can show "this PoP routes by X").
type Effective struct {
	EdgeID      string      `json:"edge_id"`
	Region      string      `json:"region"`
	Label       string      `json:"label,omitempty"`
	Mode        RoutingMode `json:"mode"`
	Mapped      bool        `json:"mapped"`
	Online      bool        `json:"online"`
	Weight      int         `json:"weight"`
	Lat         float64     `json:"lat,omitempty"`
	Long        float64     `json:"long,omitempty"`
	LatencyZone string      `json:"latency_zone,omitempty"`
	Overridden  bool        `json:"overridden"`
	// LoadFactor is the live load-steering multiplier (#3) currently applied to this
	// edge's weight: 1.0 = cool/unsteered, <1.0 = hot (share reduced). The API fills
	// it from the load controller; ResolveEffective leaves it at the neutral 1.0.
	LoadFactor float64 `json:"load_factor"`
}

// ResolveEffective computes the effective routing for one endpoint (for surfacing
// in the API; mirrors exactly what the reconciler writes).
func ResolveEffective(ep Endpoint, networkMode RoutingMode, now time.Time, staleAfter time.Duration) Effective {
	loc, mapped := LookupRegion(ep.Region)
	mode := resolveMode(networkMode, ep.RoutingOverride)
	e := Effective{
		EdgeID:     ep.EdgeID,
		Region:     ep.Region,
		Mode:       mode,
		Mapped:     mapped,
		Online:     ep.Online(now, staleAfter),
		Weight:     normWeight(ep.Weight),
		Overridden: NormalizeOverride(ep.RoutingOverride) != "",
		LoadFactor: 1.0, // the API overlays the live factor; neutral here
	}
	if mapped {
		e.Label = loc.Label
		if mode == ModeLatency {
			e.LatencyZone = loc.LatencyZone
		} else {
			e.Lat, e.Long = loc.Lat, loc.Long
		}
	}
	return e
}

// RegionList returns the region map as a sorted slice (stable output for the API).
func RegionList() []map[string]any {
	keys := make([]string, 0, len(RegionMap))
	for k := range RegionMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		loc := RegionMap[k]
		out = append(out, map[string]any{
			"region": k, "lat": loc.Lat, "long": loc.Long,
			"latency_zone": loc.LatencyZone, "label": loc.Label,
		})
	}
	return out
}
