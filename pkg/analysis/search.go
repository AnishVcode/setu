package analysis

import (
	"math/big"
	"math/rand"
	"sort"

	"github.com/setu-platform/setu/pkg/compile"
	"github.com/setu-platform/setu/pkg/eval"
	"github.com/setu-platform/setu/pkg/sema"
	"github.com/setu-platform/setu/pkg/srl"
	"github.com/setu-platform/setu/pkg/testkit"
	"github.com/setu-platform/setu/pkg/unit"
	"github.com/setu-platform/setu/pkg/value"
)

// searcher looks for profiles that demonstrate a property.
//
// It is a search, not a solver. The candidates it tries are drawn from the
// literals the rules themselves compare against, and from the values just
// either side of them, because a rule's condition changes exactly at its
// thresholds and almost nowhere else. That makes the search far more likely to
// find a witness than uniform sampling would, but it never licenses the
// conclusion that no witness exists.
type searcher struct {
	prog *compile.Program
	cfg  Config
	eng  *eval.Engine
	rnd  *rand.Rand

	// candidates holds the values worth trying for each variable.
	candidates map[string][]value.Value
	names      []string
}

func newSearcher(prog *compile.Program, cfg Config) *searcher {
	if cfg.Attempts <= 0 {
		cfg.Attempts = 500
	}
	s := &searcher{
		prog: prog, cfg: cfg,
		eng:        eval.New(prog),
		rnd:        rand.New(rand.NewSource(cfg.Seed)),
		candidates: map[string][]value.Value{},
		names:      prog.Graph.VarNames(),
	}
	s.buildCandidates()
	return s
}

// buildCandidates harvests every literal a rule compares a variable against,
// then adds the values immediately above and below it.
func (s *searcher) buildCandidates() {
	add := func(name string, v value.Value) {
		s.candidates[name] = append(s.candidates[name], v)
		if v.Numeric() {
			for _, delta := range []int64{-1, 1} {
				alt := v
				alt.Num = new(big.Rat).Add(v.Num, big.NewRat(delta, 1))
				s.candidates[name] = append(s.candidates[name], alt)
			}
		}
	}

	visit := func(e srl.Expr) {
		bin, ok := e.(*srl.BinaryExpr)
		if !ok {
			return
		}
		switch bin.Op {
		case srl.EQ, srl.NE, srl.LT, srl.LE, srl.GT, srl.GE:
		default:
			return
		}
		if ref, ok := bin.L.(*srl.ProfileRef); ok {
			if v, ok := literalOf(bin.R); ok {
				add(ref.Var, v)
			}
		}
		if ref, ok := bin.R.(*srl.ProfileRef); ok {
			if v, ok := literalOf(bin.L); ok {
				add(ref.Var, v)
			}
		}
	}
	inVisit := func(e srl.Expr) {
		in, ok := e.(*srl.InExpr)
		if !ok {
			return
		}
		ref, ok := in.X.(*srl.ProfileRef)
		if !ok {
			return
		}
		set, ok := in.Set.(*srl.SetLit)
		if !ok {
			return
		}
		for _, el := range set.Elems {
			if v, ok := literalOf(el); ok {
				add(ref.Var, v)
			}
		}
	}

	for _, rid := range s.prog.Graph.RuleIDs() {
		srl.Walk(s.prog.Graph.Rules[rid].When, visit)
		srl.Walk(s.prog.Graph.Rules[rid].When, inVisit)
	}

	// Every enum member and both booleans are always worth trying.
	for _, name := range s.names {
		t, ok := s.prog.VarTypes[name]
		if !ok {
			continue
		}
		switch t.Kind {
		case sema.Bool:
			s.candidates[name] = append(s.candidates[name], value.Bool(true), value.Bool(false))
		case sema.EnumT:
			if e, ok := s.prog.Graph.Enums[t.Name]; ok {
				for _, m := range e.Members {
					s.candidates[name] = append(s.candidates[name], value.Enum(t.Name, m.Name))
				}
			}
		case sema.Geo:
			// Points inside each known boundary layer, and one far outside, so
			// geospatial conditions are reachable in both directions.
			for _, p := range s.geoCandidates() {
				s.candidates[name] = append(s.candidates[name], p)
			}
		case sema.Number:
			base := unit.Base(t.Dim)
			if v, ok := s.prog.Graph.Vars[name]; ok && v.Unit != "" {
				if u, ok := unit.Lookup(v.Unit); ok && u.Dim == t.Dim {
					base = u
				}
			}
			for _, n := range []int64{0, 1, 10, 100, 1000, 100000, 10000000} {
				s.candidates[name] = append(s.candidates[name],
					value.Quantity(big.NewRat(n, 1), base))
			}
		case sema.Date:
			for _, d := range []value.Date{
				{Year: 1990, Month: 1, Day: 1}, {Year: 2020, Month: 6, Day: 29},
				{Year: 2026, Month: 9, Day: 4}, {Year: 2035, Month: 12, Day: 31},
			} {
				s.candidates[name] = append(s.candidates[name], value.OfDate(d))
			}
		}
	}

	for name := range s.candidates {
		s.candidates[name] = dedupeValues(s.candidates[name])
	}
}

// geoCandidates returns a probe point inside each boundary-layer feature, and
// one well outside every layer.
//
// The inside points are feature centroids rather than grid samples. A grid fine
// enough to hit a small zone reliably would be enormous, and a grid coarse
// enough to be cheap misses zones entirely — which would report a perfectly
// reachable geospatial rule as possibly unreachable, exactly the false alarm
// that teaches an author to stop reading the analysis.
func (s *searcher) geoCandidates() []value.Value {
	out := []value.Value{value.OfGeo(value.Point(0, 0))}
	if s.cfg.Layers == nil {
		return out
	}
	for _, layer := range s.cfg.Layers.LayerNames() {
		for _, f := range s.cfg.Layers.Features(layer) {
			c, ok := f.Centroid()
			if !ok {
				continue
			}
			if t, _ := s.cfg.Layers.Within(value.Point(c.Lat, c.Lon), layer); t == value.True {
				out = append(out, value.OfGeo(value.Point(c.Lat, c.Lon)))
			}
			// A point just outside the bounding box exercises the negative side.
			if minLat, minLon, _, _, ok := f.Bounds(); ok {
				out = append(out, value.OfGeo(value.Point(minLat-0.5, minLon-0.5)))
			}
		}
	}
	return out
}

func dedupeValues(vs []value.Value) []value.Value {
	sort.SliceStable(vs, func(i, j int) bool { return vs[i].Canonical() < vs[j].Canonical() })
	out := vs[:0]
	var last string
	for i, v := range vs {
		c := v.Canonical()
		if i > 0 && c == last {
			continue
		}
		last = c
		out = append(out, v)
	}
	return out
}

// candidate builds one trial profile, preferring the harvested values.
func (s *searcher) candidate() *eval.Profile {
	p := eval.NewProfile("witness")
	for _, name := range s.names {
		cands := s.candidates[name]
		if len(cands) == 0 {
			continue
		}
		p.Set(name, cands[s.rnd.Intn(len(cands))])
	}
	return p
}

// witnessFires searches for a profile that makes one rule fire.
func (s *searcher) witnessFires(ruleID string) (string, bool) {
	return s.search(func(d *eval.Determination) bool {
		return firedTrue(d, ruleID)
	})
}

// witnessBothFire searches for a profile that makes two rules fire together.
func (s *searcher) witnessBothFire(a, b string) (string, bool) {
	return s.search(func(d *eval.Determination) bool {
		return firedTrue(d, a) && firedTrue(d, b)
	})
}

func (s *searcher) search(pred func(*eval.Determination) bool) (string, bool) {
	for i := 0; i < s.cfg.Attempts; i++ {
		p := s.candidate()
		d := s.eng.Evaluate(
			eval.Input{Profile: p, AsOf: s.cfg.AsOf, Language: "en", Layers: s.cfg.Layers},
			eval.Options{Trace: true},
		)
		if pred(d) {
			return testkit.RenderProfile(s.prog, p), true
		}
	}
	return "", false
}

func firedTrue(d *eval.Determination, ruleID string) bool {
	for _, t := range d.Trace {
		if t.RuleID == ruleID {
			return t.Fired == "true"
		}
	}
	return false
}
