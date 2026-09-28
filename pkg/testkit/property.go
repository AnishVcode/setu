package testkit

import (
	"fmt"
	"math/big"
	"math/rand"
	"sort"
	"strings"

	"github.com/setu-platform/setu/pkg/compile"
	"github.com/setu-platform/setu/pkg/eval"
	"github.com/setu-platform/setu/pkg/sema"
	"github.com/setu-platform/setu/pkg/srl"
	"github.com/setu-platform/setu/pkg/unit"
	"github.com/setu-platform/setu/pkg/value"
)

// PropertyConfig parameterises the generated-profile suite.
type PropertyConfig struct {
	// Cases is the number of profiles to generate.
	Cases int
	// Seed fixes the generator. CI uses a constant seed so that a failure is
	// reproducible from the report alone; a nightly job varies it to widen
	// coverage over time.
	Seed int64
	// AsOf anchors every generated evaluation.
	AsOf value.Date
	// UnansweredRate is the probability that a variable is left unanswered,
	// which is what exercises the three-valued paths.
	UnansweredRate float64
	// Layers are the boundary layers geospatial predicates resolve against.
	Layers *value.Layers
}

// DefaultPropertyConfig is the configuration CI uses.
func DefaultPropertyConfig(asOf value.Date) PropertyConfig {
	return PropertyConfig{Cases: 500, Seed: 20260101, AsOf: asOf, UnansweredRate: 0.25}
}

// PropertyViolation is one counterexample.
type PropertyViolation struct {
	Property string
	Detail   string
	// Profile is the generated input, rendered so it can be pasted straight
	// into a rule file as a regression test case.
	Profile string
}

// PropertyReport is the outcome of a property run.
type PropertyReport struct {
	Cases      int
	Violations []PropertyViolation
	// SkippedMonotonicity records the rules that make the monotonicity property
	// inapplicable, so the gap is visible rather than silent.
	SkippedMonotonicity []string
}

// RunProperties checks the invariants that must hold for every ruleset,
// whatever the law it encodes.
//
// The properties are:
//
//	Totality       Evaluation always produces a determination.
//	Determinism    The same profile evaluates identically every time.
//	Citation       Every required approval carries a statutory citation.
//	Relevance      No question is asked about an answer already given.
//	Fee arithmetic The total equals the sum of the computed lines.
//	DAG sanity     The critical path never exceeds the sequential total.
//	Monotonicity   Answering a further question never reverses a settled
//	               outcome. This is the soundness property of the three-valued
//	               evaluation: UNKNOWN may become TRUE or FALSE, but TRUE must
//	               never become FALSE. It is what lets the platform show a
//	               partial determination to an applicant without misleading them.
func RunProperties(prog *compile.Program, cfg PropertyConfig) PropertyReport {
	e := eval.New(prog)
	gen := newGenerator(prog, cfg.Seed)
	rep := PropertyReport{Cases: cfg.Cases}

	// defined() inspects whether an answer exists, so a rule using it is not
	// monotone in information by construction. Rather than weaken the property,
	// the affected rules are named and the check is skipped for the ruleset.
	nonMonotone := rulesUsingDefined(prog)
	rep.SkippedMonotonicity = nonMonotone

	add := func(prop, detail string, p *eval.Profile) {
		if len(rep.Violations) >= 25 {
			return // enough to diagnose; the rest would be noise
		}
		rep.Violations = append(rep.Violations, PropertyViolation{
			Property: prop, Detail: detail, Profile: RenderProfile(prog, p),
		})
	}

	for i := 0; i < cfg.Cases; i++ {
		p := gen.profile(cfg.UnansweredRate)
		in := eval.Input{Profile: p, AsOf: cfg.AsOf, Language: "en", Layers: cfg.Layers}

		d := e.Evaluate(in, eval.Options{})
		if d == nil {
			add("totality", "evaluation produced no determination", p)
			continue
		}

		d2 := e.Evaluate(in, eval.Options{})
		if d.Hash != d2.Hash {
			add("determinism", "two evaluations of one profile produced different hashes", p)
		}

		for _, a := range d.Approvals {
			if strings.TrimSpace(a.LegalBasis) == "" {
				add("citation", "approval "+a.ID+" is required with no statutory citation", p)
			}
			if strings.TrimSpace(a.Reason) == "" && a.ByDependency == "" {
				add("citation", "approval "+a.ID+" is required with no explanation", p)
			}
		}

		for _, u := range d.Unresolved {
			if p.Answered(u.Var) {
				add("relevance",
					"asked about "+u.Var+", which the profile already answers", p)
			}
			if strings.TrimSpace(u.Question) == "" {
				add("relevance", "unresolved "+u.Var+" carries no question text", p)
			}
		}

		if err := checkFeeArithmetic(d); err != nil {
			add("fee arithmetic", err.Error(), p)
		}

		if d.DAG.TotalDays > d.DAG.SequentialDays {
			add("dag sanity", fmt.Sprintf(
				"critical path %d days exceeds the sequential total %d days",
				d.DAG.TotalDays, d.DAG.SequentialDays), p)
		}

		if len(nonMonotone) == 0 {
			if v := checkMonotonicity(e, gen, p, cfg); v != "" {
				add("monotonicity", v, p)
			}
		}
	}
	return rep
}

// checkFeeArithmetic verifies the total against the lines.
func checkFeeArithmetic(d *eval.Determination) error {
	sum := new(big.Rat)
	complete := true
	for _, f := range d.Fees {
		if !f.Computed {
			complete = false
			continue
		}
		r, ok := new(big.Rat).SetString(f.AmountRaw)
		if !ok {
			return fmt.Errorf("fee %s has an unparseable amount %q", f.FeeID, f.AmountRaw)
		}
		sum.Add(sum, r)
	}
	if complete != d.FeeComplete {
		return fmt.Errorf("feeComplete is %v but the lines say %v", d.FeeComplete, complete)
	}
	if got := value.Money(sum).String(); got != d.FeeTotal {
		return fmt.Errorf("fee total is %s but the lines sum to %s", d.FeeTotal, got)
	}
	return nil
}

// checkMonotonicity answers one more question and verifies that nothing which
// was settled changed its mind.
func checkMonotonicity(e *eval.Engine, gen *generator, p *eval.Profile,
	cfg PropertyConfig) string {

	unanswered := gen.unanswered(p)
	if len(unanswered) == 0 {
		return ""
	}
	name := unanswered[gen.rnd.Intn(len(unanswered))]
	extended := p.Clone()
	extended.Set(name, gen.valueFor(name))

	before := e.Evaluate(eval.Input{Profile: p, AsOf: cfg.AsOf, Language: "en", Layers: cfg.Layers},
		eval.Options{})
	after := e.Evaluate(eval.Input{Profile: extended, AsOf: cfg.AsOf, Language: "en", Layers: cfg.Layers},
		eval.Options{})

	for _, sb := range before.Statuses() {
		if sb.Required == value.Unknown {
			continue // an undetermined outcome may settle either way
		}
		sa, ok := after.StatusOf(sb.ID)
		if !ok {
			return fmt.Sprintf("approval %s disappeared after answering %s", sb.ID, name)
		}
		if sa.Required != sb.Required {
			return fmt.Sprintf(
				"approval %s was %s, then became %s after answering %s = %s; "+
					"a settled outcome must not change when more is learned",
				sb.ID, sb.Required, sa.Required, name, extended.Get(name))
		}
	}
	return ""
}

// rulesUsingDefined finds the rules whose conditions call defined().
func rulesUsingDefined(prog *compile.Program) []string {
	var out []string
	for _, rid := range prog.Graph.RuleIDs() {
		found := false
		srl.Walk(prog.Graph.Rules[rid].When, func(e srl.Expr) {
			if c, ok := e.(*srl.CallExpr); ok && c.Fn == "defined" {
				found = true
			}
		})
		if found {
			out = append(out, rid)
		}
	}
	return out
}

// ---------- generation ----------

// generator produces profiles biased towards the boundaries the rules actually
// test. Uniform random values almost never land on a statutory threshold; the
// interesting profiles are the ones at, just below, and just above each one.
type generator struct {
	prog *compile.Program
	rnd  *rand.Rand
	// interesting maps a variable to the literal values rules compare it with.
	interesting map[string][]value.Value
	names       []string
}

func newGenerator(prog *compile.Program, seed int64) *generator {
	g := &generator{
		prog:        prog,
		rnd:         rand.New(rand.NewSource(seed)),
		interesting: map[string][]value.Value{},
		names:       prog.Graph.VarNames(),
	}
	g.collectInteresting()
	return g
}

// collectInteresting harvests the literal on the other side of every comparison
// against a profile variable.
func (g *generator) collectInteresting() {
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
		record := func(ref *srl.ProfileRef, other srl.Expr) {
			v, err := literalValue(other)
			if err != nil {
				return
			}
			g.interesting[ref.Var] = append(g.interesting[ref.Var], v)
		}
		if ref, ok := bin.L.(*srl.ProfileRef); ok {
			record(ref, bin.R)
		}
		if ref, ok := bin.R.(*srl.ProfileRef); ok {
			record(ref, bin.L)
		}
	}
	for _, rid := range g.prog.Graph.RuleIDs() {
		srl.Walk(g.prog.Graph.Rules[rid].When, visit)
	}
	for _, fid := range g.prog.Graph.FeeIDs() {
		srl.Walk(g.prog.Graph.Fees[fid].Amount, visit)
	}
	for _, iid := range g.prog.Graph.IncentiveIDs() {
		srl.Walk(g.prog.Graph.Incentives[iid].Value, visit)
	}
	// Also harvest slab bounds, which are where fee schedules change step.
	for _, fid := range g.prog.Graph.FeeIDs() {
		srl.Walk(g.prog.Graph.Fees[fid].Amount, func(e srl.Expr) {
			s, ok := e.(*srl.SlabExpr)
			if !ok {
				return
			}
			ref, ok := s.Subject.(*srl.ProfileRef)
			if !ok {
				return
			}
			for _, arm := range s.Arms {
				if v, err := literalValue(arm.Bound); err == nil {
					g.interesting[ref.Var] = append(g.interesting[ref.Var], v)
				}
			}
		})
	}
}

func (g *generator) profile(unansweredRate float64) *eval.Profile {
	p := eval.NewProfile("generated")
	for _, name := range g.names {
		if g.rnd.Float64() < unansweredRate {
			continue
		}
		p.Set(name, g.valueFor(name))
	}
	return p
}

func (g *generator) unanswered(p *eval.Profile) []string {
	var out []string
	for _, n := range g.names {
		if !p.Answered(n) {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// valueFor produces a value for one variable, preferring boundaries.
func (g *generator) valueFor(name string) value.Value {
	t, ok := g.prog.VarTypes[name]
	if !ok {
		return value.Null
	}
	cands := g.interesting[name]
	if len(cands) > 0 && g.rnd.Float64() < 0.75 {
		base := cands[g.rnd.Intn(len(cands))]
		return g.perturb(base)
	}
	return g.freshValue(t, name)
}

// perturb nudges a boundary value to just below, exactly on, or just above it,
// which is where off-by-one errors in encoded thresholds live.
func (g *generator) perturb(v value.Value) value.Value {
	if !v.Numeric() {
		return v
	}
	switch g.rnd.Intn(3) {
	case 0:
		out := v
		out.Num = new(big.Rat).Sub(v.Num, big.NewRat(1, 1))
		return out
	case 1:
		return v
	default:
		out := v
		out.Num = new(big.Rat).Add(v.Num, big.NewRat(1, 1))
		return out
	}
}

func (g *generator) freshValue(t sema.Type, name string) value.Value {
	switch t.Kind {
	case sema.Bool:
		return value.Bool(g.rnd.Intn(2) == 1)
	case sema.Text:
		return value.Text(fmt.Sprintf("t%d", g.rnd.Intn(1000)))
	case sema.Date:
		return value.Date{
			Year: 2000 + g.rnd.Intn(40), Month: 1 + g.rnd.Intn(12), Day: 1 + g.rnd.Intn(28),
		}.Value()
	case sema.Period:
		return value.OfPeriod(value.Period{Days: g.rnd.Intn(365)})
	case sema.Geo:
		// A point somewhere in the Tamil Nadu bounding box.
		lat := 8.0 + g.rnd.Float64()*5.5
		lon := 76.0 + g.rnd.Float64()*4.5
		return value.OfGeo(value.Point(lat, lon))
	case sema.EnumT:
		e, ok := g.prog.Graph.Enums[t.Name]
		if !ok || len(e.Members) == 0 {
			return value.Null
		}
		return value.Enum(t.Name, e.Members[g.rnd.Intn(len(e.Members))].Name)
	case sema.Number:
		n := int64(g.rnd.Intn(1000))
		if t.Dim == unit.DimMoney {
			return value.Money(big.NewRat(n*100000, 1))
		}
		u := unit.Base(t.Dim)
		if v, ok := g.prog.Graph.Vars[name]; ok && v.Unit != "" {
			if lu, ok := unit.Lookup(v.Unit); ok {
				u = lu
			}
		}
		return value.Quantity(big.NewRat(n, 1), u)
	case sema.SetT:
		if t.Elem == nil {
			return value.Null
		}
		return value.SetOf([]value.Value{g.freshValue(*t.Elem, name)})
	}
	return value.Null
}

// RenderProfile writes a profile as an SRL `given` block.
//
// The output must parse. A counterexample is only useful if an author can paste
// it into the rule file as a regression test without editing it, so this
// renders SRL literals — `true`, not the "yes" a value renders for a person —
// and states quantities in the unit the profile declares.
func RenderProfile(prog *compile.Program, p *eval.Profile) string {
	var b strings.Builder
	b.WriteString("given {\n")
	for _, name := range prog.Graph.VarNames() {
		if !p.Answered(name) {
			continue
		}
		b.WriteString("  profile." + name + " = " + literalText(prog, name, p.Get(name)) + "\n")
	}
	b.WriteString("}")
	return b.String()
}

// literalText renders a value in a form the parser accepts.
func literalText(prog *compile.Program, name string, v value.Value) string {
	switch v.Kind {
	case value.KindBool:
		if v.Bool {
			return "true"
		}
		return "false"
	case value.KindText:
		return `"` + v.Text + `"`
	case value.KindEnum:
		return v.EnumTy + "." + v.Enum
	case value.KindDate:
		return v.Date.String()
	case value.KindGeo:
		if v.Geo == nil {
			return "unknown"
		}
		return fmt.Sprintf("point(%.6f, %.6f)", v.Geo.Lat, v.Geo.Lon)
	case value.KindMoney:
		return "INR " + ratLiteral(v.Num)
	case value.KindNumber:
		if v.Unit.IsNone() {
			return ratLiteral(v.Num)
		}
		// Prefer the unit the profile declares, so the literal reads the way the
		// rules that test it are written.
		out := v
		if decl, ok := prog.Graph.Vars[name]; ok && decl.Unit != "" {
			if u, ok := unit.Lookup(decl.Unit); ok {
				if conv, ok := v.In(u); ok {
					out = conv
				}
			}
		}
		return ratLiteral(out.Num) + " " + out.Unit.Label()
	case value.KindSet:
		parts := make([]string, len(v.Set))
		for i, m := range v.Set {
			parts[i] = literalText(prog, name, m)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	}
	return "unknown"
}

// ratLiteral renders an exact rational in a form the lexer accepts. The lexer
// has no syntax for a fraction, so a non-terminating value is rendered as a
// decimal with enough places to stay on the same side of any threshold a rule
// is likely to test.
func ratLiteral(r *big.Rat) string {
	if r.IsInt() {
		return r.Num().String()
	}
	s := r.FloatString(9)
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}
