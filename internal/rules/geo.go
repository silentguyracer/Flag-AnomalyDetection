package rules

import (
	"math"
	"strings"
)

// LatLon coordinates of country approximate population centroids.
type LatLon struct {
	Lat float64
	Lon float64
}

// CountryCentroids provides approximate centroid coordinates for major ISO country codes.
var CountryCentroids = map[string]LatLon{
	"GB": {Lat: 55.3781, Lon: -3.4360},
	"US": {Lat: 37.0902, Lon: -95.7129},
	"CA": {Lat: 56.1304, Lon: -106.3468},
	"FR": {Lat: 46.2276, Lon: 2.2137},
	"DE": {Lat: 51.1657, Lon: 10.4515},
	"ES": {Lat: 40.4637, Lon: -3.7492},
	"IT": {Lat: 41.8719, Lon: 12.5674},
	"NL": {Lat: 52.1326, Lon: 5.2913},
	"JP": {Lat: 36.2048, Lon: 138.2529},
	"AU": {Lat: -25.2744, Lon: 133.7751},
	"SG": {Lat: 1.3521, Lon: 103.8198},
	"IN": {Lat: 20.5937, Lon: 78.9629},
	"BR": {Lat: -14.2350, Lon: -51.9253},
	"ZA": {Lat: -30.5595, Lon: 22.9375},
	"CN": {Lat: 35.8617, Lon: 104.1954},
	"AE": {Lat: 23.4241, Lon: 53.8478},
	"NG": {Lat: 9.0820, Lon: 8.6753},
	"RU": {Lat: 61.5240, Lon: 105.3188},
	"MX": {Lat: 23.6345, Lon: -102.5528},
	"IE": {Lat: 53.1424, Lon: -7.6921},
}

// DistanceKm computes great-circle distance between two countries using the Haversine formula.
// If either country is unknown, it defaults to a conservative international distance of 5,000 km.
func DistanceKm(c1, c2 string) float64 {
	c1 = strings.ToUpper(strings.TrimSpace(c1))
	c2 = strings.ToUpper(strings.TrimSpace(c2))
	if c1 == c2 {
		return 0.0
	}

	p1, ok1 := CountryCentroids[c1]
	p2, ok2 := CountryCentroids[c2]
	if !ok1 || !ok2 {
		// Fallback for cross-border transactions with unindexed countries
		return 3500.0
	}

	const earthRadiusKm = 6371.0
	dLat := (p2.Lat - p1.Lat) * math.Pi / 180.0
	dLon := (p2.Lon - p1.Lon) * math.Pi / 180.0

	lat1 := p1.Lat * math.Pi / 180.0
	lat2 := p2.Lat * math.Pi / 180.0

	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Sin(dLon/2)*math.Sin(dLon/2)*math.Cos(lat1)*math.Cos(lat2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))

	return earthRadiusKm * c
}
