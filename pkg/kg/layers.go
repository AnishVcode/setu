package kg

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/setu-platform/setu/pkg/value"
)

// LayersFile is the on-disk form of a jurisdiction's boundary layers.
//
// Geospatial predicates in rules — "within the coastal regulation zone",
// "further than 500 m from the coastline" — are only reproducible if the map
// they were evaluated against is pinned alongside the rules. The layer file is
// therefore versioned, hashed into the ruleset snapshot, and recorded on every
// determination that used it (spec 5.2: jurisdiction resolution must be
// explainable — which layer, which version, which polygon).
type LayersFile struct {
	Version string                 `json:"version"`
	Source  string                 `json:"source,omitempty"`
	Layers  map[string][]LayerFeat `json:"layers"`
}

// LayerFeat is one named polygon in a layer.
type LayerFeat struct {
	Name string       `json:"name"`
	Ring [][2]float64 `json:"ring"` // [latitude, longitude] pairs, closed implicitly
}

// LayersFileName is the conventional file name inside a jurisdiction directory.
const LayersFileName = "layers.json"

// LoadLayers reads the boundary layers for a jurisdiction directory. A missing
// file is not an error: a ruleset with no geospatial rules needs no layers, and
// geospatial predicates evaluated without a layer correctly return UNKNOWN
// rather than false — not having the map is not evidence of being outside the
// zone.
func LoadLayers(dir string) (*value.Layers, error) {
	path := filepath.Join(dir, LayersFileName)
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var f LayersFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if f.Version == "" {
		return nil, fmt.Errorf("%s: version is required, so a determination can name the map it used", path)
	}

	l := value.NewLayers(f.Version)
	names := make([]string, 0, len(f.Layers))
	for name := range f.Layers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		for _, feat := range f.Layers[name] {
			if len(feat.Ring) < 3 {
				return nil, fmt.Errorf("%s: layer %q feature %q has %d points; a polygon needs at least 3",
					path, name, feat.Name, len(feat.Ring))
			}
			ring := make([]value.LatLon, 0, len(feat.Ring))
			for _, p := range feat.Ring {
				ring = append(ring, value.LatLon{Lat: p[0], Lon: p[1]})
			}
			l.Add(name, value.Zone(name, f.Version, feat.Name, ring))
		}
	}
	return l, nil
}
