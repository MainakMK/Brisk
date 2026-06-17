# Bunny Scriptable DNS — Future Reference (Phase 4+)

**Status:** NOT adopted. Evaluated 2026-06-10. Brisk stays on **normal Bunny DNS**
(static records + Smart Records driven by our own control plane) for now. This doc
captures *what* Scriptable DNS is and *where* it could help later, so we don't
re-research it from scratch.

Docs:
- https://docs.bunny.net/dns/scriptable/introduction
- https://docs.bunny.net/dns/scriptable/helper-objects
- https://docs.bunny.net/dns/scriptable/query-response-object-types

---

## What it is (in one line)
Instead of static DNS records, you write a JavaScript `handleQuery(query)` function
that runs **at the moment of every DNS lookup** and returns the answer dynamically.
The decision happens at Bunny's anycast edge, per request.

### Entry point
```js
export default function handleQuery(query) {
  // query.request.geoLocation.country, .clientIP, .queryType, .serverZone, ...
  return new ARecord("111.111.111.111", 30); // (ip, ttl)
}
```
> `console.log` is editor-only — must be removed before publishing or the script
> fails at runtime.

### Query object (what you get per request)
`query.request` = `DnsQuery`:
- `hostname` — queried name
- `clientIP` — remote client IP
- `queryType` — A / AAAA / TXT
- `ednsIP` — EDNS0 client-subnet IP
- `serverZone` — which Bunny DNS PoP answered (DE, UK, SG, ...)
- `geoLocation` — `{ latitude, longitude, country (ISO-2), asn }`

### Response object types you can return (single or array)
- `ARecord(ip, ttl=30)`
- `AaaaRecord(ip, ttl=30)`
- `CnameRecord(hostname, ttl=30)`
- `PullZoneRecord(pullzone)` — auto-maps to the pull zone's A/AAAA

### Helper objects (the powerful part)
- **`Monitoring.getStatus(ip)` → `{ isOnline, latency }`** — Bunny health-checks the
  IP in the background from its own DNS PoPs. First call seeds the monitor (sync wait
  for initial latency); subsequent calls return cached status instantly.
- **`GeoDatabase.resolve(ip)` → `GeoLocation`** — geo-IP lookup.
- **`GeoDistance.calculate(...)`** — distance between two lat/long points, two
  `GeoLocation`s, or a `Server` + `GeoLocation`.
- **`RoutingEngine.getWeightedRandom(servers, onlineOnly=false, applyWeight=true)`** —
  weighted round-robin pick, can skip offline servers.
- **`RoutingEngine.getClosestServer(servers, location, onlineOnly=false, applyWeight=true)`** —
  nearest-server pick by geo, can skip offline servers.
- **`Server(ip, lat, lon, weight)`** — server descriptor for the routing engine.

So in ~15 lines you get geo-routing + health-failover + weighted load-balancing,
decided per-query at Bunny's edge.

---

## Why we are NOT using it now

1. **It duplicates Brisk's own brain.** Our control plane already does this job: the
   reconciler writes records via the Bunny API, geo/latency Smart-Record routing comes
   from `servers.region`, and our self-driven health checker gives ~32s failover.
   Adopting Scriptable DNS moves that routing logic *out of our Go code and into
   Bunny's JS sandbox* — directly against **Golden Rule #1** ("Brisk runs forever on
   our own code; no third-party service in the production runtime").

2. **It deepens vendor lock-in.** Today Bunny is just "a DNS API we POST records to" —
   swappable. If our *routing logic* becomes a Bunny-hosted JS script, moving off Bunny
   later (when we scale/sell) means rewriting the brain, not swapping an API client.

3. **Routing is core CDN IP.** We want the decision engine in Brisk, not rented.

---

## Where it genuinely could help (revisit Phase 4+)

### A. Thin failover safety net *underneath* our control plane
A tiny script that just drops an edge from rotation when `Monitoring.getStatus(ip)`
reports offline — so the live site keeps routing correctly **even if our laptop / VPS
control plane is down.** This aligns with **Golden Rule #3** (data plane independent of
control plane): Bunny's edge-side monitoring becomes a backstop that survives a
control-plane outage, instead of our static records going stale.

Shape (illustrative only — IPs here are our already-public edge IPs):
```js
export default function handleQuery(query) {
  // edges injected/templated by our control plane on publish
  const edges = [
    new Server("104.248.231.144", 40.69, -74.18, 100), // US-NY
    new Server("188.245.225.172", 50.11,  8.68, 100),  // EU-FRA
    new Server("139.59.78.21",    12.97, 77.59, 100),  // BLR
  ];
  return RoutingEngine.getClosestServer(
    edges, query.request.geoLocation, /*onlineOnly*/ true);
}
```
Key point: the script would be **generated/published by our control plane** (single
source of truth = our DB), not hand-edited in Bunny. It's a resilience layer, not the
primary router.

### B. Many-PoP, sub-second closest-edge selection
Once we run many PoPs and want true per-query nearest-edge routing that static Smart
Records can't express (e.g. fine-grained lat/long + live latency + weights), the
`RoutingEngine` + `Monitoring` combo does it at the edge, per request, with TTLs as low
as 30s. Static records can't react that fast or that granularly.

---

## Decision summary
- **Now (Phase 3):** keep normal Bunny DNS. Routing brain stays in Brisk's Go control
  plane. Bunny stays a swappable API client.
- **Phase 4+:** consider Scriptable DNS *only* as (A) a control-plane-published failover
  backstop using `Monitoring`, and/or (B) fine-grained many-PoP closest-edge routing.
  Never as the primary, hand-edited router.
