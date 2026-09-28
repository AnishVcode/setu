package value

import (
	"fmt"
	"math"
	"math/big"
	"sort"
	"strings"
)

// Geo is a geometry: a point, or a named polygonal layer feature.
//
// Rules ask geospatial questions — "is the premise within an eco-sensitive
// zone", "is it more than 500 m from the coastline" (spec 5.5.3). Answering
// them requires the actual boundary layer, its version, and which polygon
// matched, because spec 5.2 requires jurisdiction resolution to be explainable.
type Geo struct {
	Kind    GeoKind
	Lat     float64 // WGS84 degrees; presentation and distance only
	Lon     float64
	Name    string // feature or layer name for a Zone
	Layer   string // the boundary layer this came from
	Version string // the layer version, so a determination can be reproduced
	Ring    []LatLon
}

// GeoKind distinguishes a point from an area.
type GeoKind uint8

const (
	GeoPoint GeoKind = iota
	GeoZone
)

// LatLon is a WGS84 coordinate pair.
type LatLon struct {
	Lat float64
	Lon float64
}

// Point constructs a premise location.
func Point(lat, lon float64) *Geo { return &Geo{Kind: GeoPoint, Lat: lat, Lon: lon} }

// Zone constructs a named layer feature from its boundary ring.
func Zone(layer, version, name string, ring []LatLon) *Geo {
	return &Geo{Kind: GeoZone, Layer: layer, Version: version, Name: name, Ring: ring}
}

func (g *Geo) String() string {
	if g == nil {
		return "unknown location"
	}
	if g.Kind == GeoPoint {
		return fmt.Sprintf("(%.6f, %.6f)", g.Lat, g.Lon)
	}
	return g.Name
}

func (g *Geo) canonical() string {
	if g == nil {
		return "nil"
	}
	if g.Kind == GeoPoint {
		// Six decimal places is ~0.1 m; fixing the precision keeps the
		// canonical form stable across float formatting differences.
		return fmt.Sprintf("pt:%.6f:%.6f", g.Lat, g.Lon)
	}
	return "zone:" + g.Layer + ":" + g.Version + ":" + g.Name
}

// Layers is a versioned set of boundary layers a determination is evaluated
// against. It is pinned into the ruleset snapshot alongside the rules, so a
// determination can be reproduced exactly years later (P6).
type Layers struct {
	Version string
	byLayer map[string][]*Geo
}

// NewLayers constructs an empty layer set.
func NewLayers(version string) *Layers {
	return &Layers{Version: version, byLayer: map[string][]*Geo{}}
}

// Add registers a feature in a layer.
func (l *Layers) Add(layer string, g *Geo) {
	g.Layer = layer
	g.Version = l.Version
	l.byLayer[layer] = append(l.byLayer[layer], g)
	sort.SliceStable(l.byLayer[layer], func(i, j int) bool {
		return l.byLayer[layer][i].Name < l.byLayer[layer][j].Name
	})
}

// Features returns the features of a layer, sorted by name, so tooling can
// reason about the actual polygons rather than only test points against them.
func (l *Layers) Features(layer string) []*Geo {
	src := l.byLayer[layer]
	out := make([]*Geo, len(src))
	copy(out, src)
	return out
}

// Centroid returns the average of a feature's ring vertices. For the convex
// rings a boundary layer is made of, this lies inside the feature, which is
// what makes it useful as a probe point for reachability analysis.
func (g *Geo) Centroid() (LatLon, bool) {
	if g == nil || len(g.Ring) == 0 {
		return LatLon{}, false
	}
	var lat, lon float64
	for _, p := range g.Ring {
		lat += p.Lat
		lon += p.Lon
	}
	n := float64(len(g.Ring))
	return LatLon{Lat: lat / n, Lon: lon / n}, true
}

// Bounds returns the axis-aligned bounding box of a feature.
func (g *Geo) Bounds() (minLat, minLon, maxLat, maxLon float64, ok bool) {
	if g == nil || len(g.Ring) == 0 {
		return 0, 0, 0, 0, false
	}
	minLat, minLon = g.Ring[0].Lat, g.Ring[0].Lon
	maxLat, maxLon = minLat, minLon
	for _, p := range g.Ring[1:] {
		if p.Lat < minLat {
			minLat = p.Lat
		}
		if p.Lat > maxLat {
			maxLat = p.Lat
		}
		if p.Lon < minLon {
			minLon = p.Lon
		}
		if p.Lon > maxLon {
			maxLon = p.Lon
		}
	}
	return minLat, minLon, maxLat, maxLon, true
}

// LayerNames lists registered layers, sorted.
func (l *Layers) LayerNames() []string {
	out := make([]string, 0, len(l.byLayer))
	for k := range l.byLayer {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Within reports whether a point falls inside any feature of the named layer,
// returning the matching feature so the trace can name it. An unknown layer
// yields Unknown rather than false: not having the map is not evidence of
// being outside the zone.
func (l *Layers) Within(p *Geo, layer string) (Tri, *Geo) {
	if p == nil || p.Kind != GeoPoint {
		return Unknown, nil
	}
	feats, ok := l.byLayer[layer]
	if !ok {
		return Unknown, nil
	}
	for _, f := range feats {
		if pointInRing(p.Lat, p.Lon, f.Ring) {
			return True, f
		}
	}
	return False, nil
}

// DistanceTo returns the great-circle distance in metres from p to the nearest
// feature of the named layer, as an exact rational rounded to whole metres.
//
// The geodesy runs in float64 — there is no exact rational haversine — but the
// result is rounded to a whole metre before it re-enters rule evaluation, so
// the value the rules see is exact and reproducible. Statutory buffers are
// stated in whole metres, so no legal precision is lost.
func (l *Layers) DistanceTo(p *Geo, layer string) (Tri, *big.Rat, *Geo) {
	if p == nil || p.Kind != GeoPoint {
		return Unknown, nil, nil
	}
	feats, ok := l.byLayer[layer]
	if !ok || len(feats) == 0 {
		return Unknown, nil, nil
	}
	best := math.Inf(1)
	var bestF *Geo
	for _, f := range feats {
		d := distanceToRing(p.Lat, p.Lon, f.Ring)
		if d < best {
			best, bestF = d, f
		}
	}
	if math.IsInf(best, 1) {
		return Unknown, nil, nil
	}
	return True, big.NewRat(int64(math.Round(best)), 1), bestF
}

// pointInRing is the standard ray-casting test on a closed ring.
func pointInRing(lat, lon float64, ring []LatLon) bool {
	if len(ring) < 3 {
		return false
	}
	in := false
	j := len(ring) - 1
	for i := 0; i < len(ring); i++ {
		yi, xi := ring[i].Lat, ring[i].Lon
		yj, xj := ring[j].Lat, ring[j].Lon
		if (yi > lat) != (yj > lat) {
			x := (xj-xi)*(lat-yi)/(yj-yi) + xi
			if lon < x {
				in = !in
			}
		}
		j = i
	}
	return in
}

// distanceToRing is zero inside the ring, otherwise the distance to the
// nearest edge.
func distanceToRing(lat, lon float64, ring []LatLon) float64 {
	if len(ring) == 0 {
		return math.Inf(1)
	}
	if pointInRing(lat, lon, ring) {
		return 0
	}
	best := math.Inf(1)
	j := len(ring) - 1
	for i := 0; i < len(ring); i++ {
		d := distanceToSegment(lat, lon, ring[j], ring[i])
		if d < best {
			best = d
		}
		j = i
	}
	return best
}

const earthRadiusM = 6371008.8

func haversine(lat1, lon1, lat2, lon2 float64) float64 {
	p := math.Pi / 180
	a := 0.5 - math.Cos((lat2-lat1)*p)/2 +
		math.Cos(lat1*p)*math.Cos(lat2*p)*(1-math.Cos((lon2-lon1)*p))/2
	return 2 * earthRadiusM * math.Asin(math.Sqrt(a))
}

// distanceToSegment projects onto the segment in a local equirectangular frame,
// which is accurate well inside the scale of any statutory buffer.
func distanceToSegment(lat, lon float64, a, b LatLon) float64 {
	p := math.Pi / 180
	cosLat := math.Cos(lat * p)
	toXY := func(la, lo float64) (float64, float64) {
		return lo * p * earthRadiusM * cosLat, la * p * earthRadiusM
	}
	px, py := toXY(lat, lon)
	ax, ay := toXY(a.Lat, a.Lon)
	bx, by := toXY(b.Lat, b.Lon)
	dx, dy := bx-ax, by-ay
	if dx == 0 && dy == 0 {
		return haversine(lat, lon, a.Lat, a.Lon)
	}
	t := ((px-ax)*dx + (py-ay)*dy) / (dx*dx + dy*dy)
	t = math.Max(0, math.Min(1, t))
	cx, cy := ax+t*dx, ay+t*dy
	return math.Hypot(px-cx, py-cy)
}

// Describe renders the layer set for a determination's provenance block.
func (l *Layers) Describe() string {
	var b strings.Builder
	b.WriteString("boundary layers v" + l.Version + ": ")
	b.WriteString(strings.Join(l.LayerNames(), ", "))
	return b.String()
}
