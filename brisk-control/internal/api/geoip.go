package api

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"time"
)

// geoIPResult is the dashboard-facing shape: coordinates + a "City, Country" label +
// ISO-2 country code, used by Add Server to auto-pick the nearest region.
type geoIPResult struct {
	Lat   float64 `json:"lat"`
	Long  float64 `json:"long"`
	Label string  `json:"label"`
	CC    string  `json:"country_code"`
}

// geoIP geolocates a server IP SERVER-SIDE (on the control plane) and returns its
// coordinates + label. Doing it here — not from the browser — means the dashboard hits
// no third party (no CORS failures, the reason the client-side lookup didn't auto-pick)
// and the lookup is reliable. It is best-effort + only used at Add-Server time (an admin
// action), never in the edge serving path, so it doesn't touch the "own code runs
// production" rule. Provider: ip-api.com (free, keyless, server-side HTTP).
func (a *API) geoIP(w http.ResponseWriter, r *http.Request) {
	ip := strings.TrimSpace(r.URL.Query().Get("ip"))
	if net.ParseIP(ip) == nil {
		writeError(w, http.StatusBadRequest, "a valid ip query param is required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	url := "http://ip-api.com/json/" + ip + "?fields=status,message,lat,lon,city,country,countryCode"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "geoip request build failed")
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		writeError(w, http.StatusBadGateway, "geoip lookup unreachable")
		return
	}
	defer resp.Body.Close()

	var body struct {
		Status      string  `json:"status"`
		Lat         float64 `json:"lat"`
		Lon         float64 `json:"lon"`
		City        string  `json:"city"`
		Country     string  `json:"country"`
		CountryCode string  `json:"countryCode"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil || body.Status != "success" {
		writeError(w, http.StatusNotFound, "ip could not be located")
		return
	}

	label := body.City
	switch {
	case body.City != "" && body.Country != "":
		label = body.City + ", " + body.Country
	case body.Country != "":
		label = body.Country
	}
	writeJSON(w, http.StatusOK, geoIPResult{
		Lat: body.Lat, Long: body.Lon, Label: label, CC: body.CountryCode,
	})
}
