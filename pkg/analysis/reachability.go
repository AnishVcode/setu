// Package analysis performs the static checks spec 5.5.4 requires of the
// compiler — contradictory rules, unreachable rules, orphaned approvals — and
// the change-impact analysis spec 5.5.5 requires before a ruleset is published.
//
// Two of these checks are decidable and two are not, and the package is honest
// about which is which:
//
//	Cycles and orphans     Decided exactly, by graph traversal (in pkg/compile
//	                       and below).
//	Unreachable rules      Proven unsatisfiable for conjunctions of comparisons
//	                       on a single variable, which is where the mistake
//	                       almost always is. Anything else is reported as
//	                       "no satisfying profile found", never as "unreachable".
//	Contradictions         Detected by finding an actual profile that makes two
//	                       opposing rules of equal precedence both fire. A hit
//	                       is proof; a miss is not proof of absence, and the
//	                       report says so.
//
// A checker that overstated its own certainty would be worse than none: a
// regulatory author would delete a rule the analysis merely failed to
// understand.
package analysis

import (
	"fmt"
	"math/big"
	"sort"
	"strings"

	"github.com/setu-platform/setu/pkg/compile"
	"github.com/setu-platform/setu/pkg/srl"
	"github.com/setu-platform/setu/pkg/unit"
	"github.com/setu-platform/setu/pkg/value"
)

// Finding is one analysis result.
type Finding struct {
	Kind     string // unreachable | contradiction | orphan | unused
	Severity srl.Severity
	Pos      srl.Pos
	Subject  string
	Msg      string
	Hint     string
	// Witness is a profile demonstrating the finding, rendered as an SRL
	// `given` block so it can be pasted straight in as a regression test.
	Witness string
	// Proven distinguishes a demonstrated fact from a search that came up empty.
	Proven bool
}

func (f Finding) String() string {
	s := fmt.Sprintf("%s: %s: [%s] %s", f.Pos, f.Severity, f.Kind, f.Msg)
	if f.Hint != "" {
		s += "\n    hint: " + f.Hint
	}
	if f.Witness != "" {
		for _, line := range strings.Split(f.Witness, "\n") {
			s += "\n    " + line
		}
	}
	return s
}

// Config parameterises the searches.
type Config struct {
	// Attempts is how many candidate profiles the witness search tries per rule
	// or per rule pair.
	Attempts int
	Seed     int64
	AsOf     value.Date
	Layers   *value.Layers
}

// DefaultConfig is what `setuctl analyze` and the publish gate use.
func DefaultConfig(asOf value.Date) Config {
	return Config{Attempts: 2000, Seed: 424242, AsOf: asOf}
}

// Report is the full analysis result.
type Report struct {
	Findings []Finding
}

// Errors counts blocking findings.
func (r Report) Errors() int {
	n := 0
	for _, f := range r.Findings {
		if f.Severity == srl.SevError {
			n++
		}
	}
	return n
}

// Run performs every static analysis over a compiled program.
func Run(prog *compile.Program, cfg Config) Report {
	var rep Report
	s := newSearcher(prog, cfg)

	unreach := unreachableRules(prog, s)
	rep.Findings = append(rep.Findings, unreach...)

	// A rule proven never to fire cannot contradict anything. Reporting it as a
	// contradiction as well would send an author to look at a second problem
	// that will disappear when they fix the first.
	dead := map[string]bool{}
	for _, f := range unreach {
		if f.Proven {
			dead[f.Subject] = true
		}
	}
	rep.Findings = append(rep.Findings, contradictions(prog, s, dead)...)
	rep.Findings = append(rep.Findings, unreachableApprovals(prog)...)

	sort.SliceStable(rep.Findings, func(i, j int) bool {
		a, b := rep.Findings[i], rep.Findings[j]
		if a.Severity != b.Severity {
			return a.Severity < b.Severity
		}
		if a.Pos.File != b.Pos.File {
			return a.Pos.File < b.Pos.File
		}
		return a.Pos.Line < b.Pos.Line
	})
	return rep
}

// unreachableRules looks for rules that can never fire.
func unreachableRules(prog *compile.Program, s *searcher) []Finding {
	var out []Finding
	for _, rid := range prog.Graph.RuleIDs() {
		r := prog.Graph.Rules[rid]
		if r.When == nil {
			continue
		}

		// A rule whose validity window has closed is dead content, not a defect.
		if !r.Valid.Until.IsZero() && !r.Valid.Until.After(r.Valid.From) && !r.Valid.From.IsZero() {
			out = append(out, Finding{
				Kind: "unreachable", Severity: srl.SevError, Pos: r.Pos, Subject: rid,
				Proven: true,
				Msg: fmt.Sprintf("rule %s has an empty validity window (%s to %s)",
					rid, r.Valid.From, r.Valid.Until),
				Hint: "a rule that was never in force cannot apply to anything",
			})
			continue
		}

		// Provable unsatisfiability: contradictory bounds on one variable.
		if reason, proven := proveUnsatisfiable(r.When); proven {
			out = append(out, Finding{
				Kind: "unreachable", Severity: srl.SevError, Pos: r.Pos, Subject: rid,
				Proven: true,
				Msg:    fmt.Sprintf("rule %s can never fire: %s", rid, reason),
				Hint:   "no profile can satisfy the condition, so the rule has no effect",
			})
			continue
		}

		// Otherwise, search for a profile that makes it fire.
		if w, found := s.witnessFires(rid); !found {
			out = append(out, Finding{
				Kind: "unreachable", Severity: srl.SevWarning, Pos: r.Pos, Subject: rid,
				Proven: false,
				Msg: fmt.Sprintf(
					"no profile making rule %s fire was found in %d attempts", rid, s.cfg.Attempts),
				Hint: "this is a search, not a proof. Either the rule is unreachable, " +
					"or its condition is narrow enough that generated profiles miss it; " +
					"the authored positive test case should show which",
			})
		} else {
			_ = w
		}
	}
	return out
}

// contradictions looks for two rules of equal precedence that act in opposite
// directions on the same approval and can fire together.
func contradictions(prog *compile.Program, s *searcher, dead map[string]bool) []Finding {
	var out []Finding
	for _, aid := range prog.Graph.ApprovalIDs() {
		rules := prog.ApprovalRules[aid]
		type act struct {
			rule   string
			prec   int
			exempt bool
		}
		var acts []act
		for _, rid := range rules {
			r := prog.Graph.Rules[rid]
			for _, ef := range r.Then {
				if ef.Target != aid {
					continue
				}
				switch ef.Kind {
				case srl.EffectRequireApproval:
					acts = append(acts, act{rid, r.Precedence, false})
				case srl.EffectExemptApproval:
					acts = append(acts, act{rid, r.Precedence, true})
				}
			}
		}
		for i := 0; i < len(acts); i++ {
			for j := i + 1; j < len(acts); j++ {
				a, b := acts[i], acts[j]
				if a.prec != b.prec || a.exempt == b.exempt {
					continue
				}
				if dead[a.rule] || dead[b.rule] {
					continue
				}
				req, exm := a.rule, b.rule
				if a.exempt {
					req, exm = b.rule, a.rule
				}
				w, found := s.witnessBothFire(a.rule, b.rule)
				if !found {
					out = append(out, Finding{
						Kind: "contradiction", Severity: srl.SevWarning,
						Pos: prog.Graph.Rules[a.rule].Pos, Subject: aid, Proven: false,
						Msg: fmt.Sprintf(
							"%s and %s both act on %s at precedence %d in opposite directions; "+
								"no profile triggering both was found in %d attempts",
							req, exm, aid, a.prec, s.cfg.Attempts),
						Hint: "give the exemption a higher precedence so the resolution is " +
							"explicit rather than dependent on the conditions never overlapping",
					})
					continue
				}
				out = append(out, Finding{
					Kind: "contradiction", Severity: srl.SevError,
					Pos: prog.Graph.Rules[a.rule].Pos, Subject: aid, Proven: true,
					Msg: fmt.Sprintf(
						"%s requires %s and %s exempts it, both at precedence %d, "+
							"and this profile triggers both",
						req, aid, exm, a.prec),
					Hint: "raise the exemption's precedence above the trigger's, or narrow " +
						"one of the conditions. Left as it is, an applicant matching both " +
						"gets no answer at all",
					Witness: w,
				})
			}
		}
	}
	return out
}

// unreachableApprovals finds approvals nothing can ever require.
func unreachableApprovals(prog *compile.Program) []Finding {
	var out []Finding
	for _, aid := range prog.Graph.ApprovalIDs() {
		if len(prog.ApprovalRules[aid]) > 0 {
			continue
		}
		if len(prog.EdgesByPrereq[aid]) > 0 {
			continue
		}
		a := prog.Graph.Approvals[aid]
		out = append(out, Finding{
			Kind: "orphan", Severity: srl.SevError, Pos: a.Pos, Subject: aid, Proven: true,
			Msg:  fmt.Sprintf("approval %s is unreachable: no rule requires it and nothing depends on it", aid),
			Hint: "write a rule that requires it, or remove it",
		})
	}
	return out
}

// ---------- provable unsatisfiability ----------

// interval is a closed-open numeric range in a dimension's base unit.
type interval struct {
	lo, hi     *big.Rat
	loOpen     bool
	hiOpen     bool
	hasLo      bool
	hasHi      bool
	eq         *big.Rat
	hasEq      bool
	neq        []*big.Rat
	enumEq     string
	hasEnumEq  bool
	enumNeq    map[string]bool
	boolEq     *bool
	contradict string
}

// proveUnsatisfiable decides unsatisfiability for a conjunction of comparisons
// against profile variables. It returns a reason only when it is certain.
//
// Disjunction is not analysed: `a or b` is satisfiable if either side is, and
// proving both sides unsatisfiable is the same problem recursively, which is
// handled — but any other shape yields "not proven", never "unsatisfiable".
func proveUnsatisfiable(e srl.Expr) (string, bool) {
	switch x := e.(type) {
	case *srl.BinaryExpr:
		if x.Op == srl.OR {
			l, lok := proveUnsatisfiable(x.L)
			r, rok := proveUnsatisfiable(x.R)
			if lok && rok {
				return "both alternatives are unsatisfiable (" + l + "; " + r + ")", true
			}
			return "", false
		}
	case *srl.BoolLit:
		if !x.Val {
			return "the condition is literally false", true
		}
		return "", false
	}

	conj := flattenAnd(e)
	domains := map[string]*interval{}
	for _, c := range conj {
		bin, ok := c.(*srl.BinaryExpr)
		if !ok {
			continue
		}
		ref, other, op, ok := orient(bin)
		if !ok {
			continue
		}
		lit, ok := literalOf(other)
		if !ok {
			continue
		}
		d := domains[ref.Var]
		if d == nil {
			d = &interval{enumNeq: map[string]bool{}}
			domains[ref.Var] = d
		}
		applyConstraint(d, op, lit, ref.Var)
	}

	var names []string
	for n := range domains {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if r := domains[n].contradiction(n); r != "" {
			return r, true
		}
	}
	return "", false
}

func flattenAnd(e srl.Expr) []srl.Expr {
	bin, ok := e.(*srl.BinaryExpr)
	if !ok || bin.Op != srl.AND {
		return []srl.Expr{e}
	}
	return append(flattenAnd(bin.L), flattenAnd(bin.R)...)
}

// orient puts the profile reference on the left, flipping the operator if the
// comparison was written the other way round.
func orient(b *srl.BinaryExpr) (*srl.ProfileRef, srl.Expr, srl.Kind, bool) {
	if ref, ok := b.L.(*srl.ProfileRef); ok {
		return ref, b.R, b.Op, true
	}
	if ref, ok := b.R.(*srl.ProfileRef); ok {
		return ref, b.L, flipOp(b.Op), true
	}
	return nil, nil, 0, false
}

func flipOp(k srl.Kind) srl.Kind {
	switch k {
	case srl.LT:
		return srl.GT
	case srl.LE:
		return srl.GE
	case srl.GT:
		return srl.LT
	case srl.GE:
		return srl.LE
	}
	return k
}

// literalOf reduces a literal expression to a comparable value in base units.
func literalOf(e srl.Expr) (value.Value, bool) {
	switch x := e.(type) {
	case *srl.NumberLit:
		return value.Rat(x.Val), true
	case *srl.MoneyLit:
		return value.Money(x.Val), true
	case *srl.BoolLit:
		return value.Bool(x.Val), true
	case *srl.QuantityLit:
		u, ok := unit.Lookup(x.Unit)
		if !ok {
			return value.Null, false
		}
		return value.Quantity(x.Val, u), true
	case *srl.ObjectRef:
		if len(x.Path) == 2 {
			return value.Enum(x.Path[0], x.Path[1]), true
		}
	}
	return value.Null, false
}

func applyConstraint(d *interval, op srl.Kind, v value.Value, varName string) {
	switch v.Kind {
	case value.KindBool:
		b := v.Bool
		switch op {
		case srl.EQ:
			if d.boolEq != nil && *d.boolEq != b {
				d.contradict = fmt.Sprintf("profile.%s is required to be both true and false", varName)
			}
			d.boolEq = &b
		case srl.NE:
			nb := !b
			if d.boolEq != nil && *d.boolEq != nb {
				d.contradict = fmt.Sprintf("profile.%s is required to be both true and false", varName)
			}
			d.boolEq = &nb
		}
		return

	case value.KindEnum:
		switch op {
		case srl.EQ:
			if d.hasEnumEq && d.enumEq != v.Enum {
				d.contradict = fmt.Sprintf("profile.%s is required to equal both %s and %s",
					varName, d.enumEq, v.Enum)
			}
			if d.enumNeq[v.Enum] {
				d.contradict = fmt.Sprintf("profile.%s is required to equal and not equal %s",
					varName, v.Enum)
			}
			d.enumEq, d.hasEnumEq = v.Enum, true
		case srl.NE:
			if d.hasEnumEq && d.enumEq == v.Enum {
				d.contradict = fmt.Sprintf("profile.%s is required to equal and not equal %s",
					varName, v.Enum)
			}
			d.enumNeq[v.Enum] = true
		}
		return
	}

	if !v.Numeric() {
		return
	}
	n := unit.ToBase(v.Num, v.Unit)
	switch op {
	case srl.EQ:
		if d.hasEq && d.eq.Cmp(n) != 0 {
			d.contradict = fmt.Sprintf("profile.%s is required to equal two different values", varName)
		}
		d.eq, d.hasEq = n, true
	case srl.NE:
		d.neq = append(d.neq, n)
	case srl.GT:
		d.tightenLo(n, true)
	case srl.GE:
		d.tightenLo(n, false)
	case srl.LT:
		d.tightenHi(n, true)
	case srl.LE:
		d.tightenHi(n, false)
	}
}

func (d *interval) tightenLo(n *big.Rat, open bool) {
	if !d.hasLo || n.Cmp(d.lo) > 0 || (n.Cmp(d.lo) == 0 && open) {
		d.lo, d.loOpen, d.hasLo = n, open, true
	}
}

func (d *interval) tightenHi(n *big.Rat, open bool) {
	if !d.hasHi || n.Cmp(d.hi) < 0 || (n.Cmp(d.hi) == 0 && open) {
		d.hi, d.hiOpen, d.hasHi = n, open, true
	}
}

func (d *interval) contradiction(varName string) string {
	if d.contradict != "" {
		return d.contradict
	}
	if d.hasLo && d.hasHi {
		c := d.lo.Cmp(d.hi)
		if c > 0 {
			return fmt.Sprintf("profile.%s must be both above %s and below %s",
				varName, d.lo.RatString(), d.hi.RatString())
		}
		if c == 0 && (d.loOpen || d.hiOpen) {
			return fmt.Sprintf("profile.%s must be both above and below %s, exclusively",
				varName, d.lo.RatString())
		}
	}
	if d.hasEq {
		if d.hasLo {
			if d.eq.Cmp(d.lo) < 0 || (d.loOpen && d.eq.Cmp(d.lo) == 0) {
				return fmt.Sprintf("profile.%s must equal %s and also exceed %s",
					varName, d.eq.RatString(), d.lo.RatString())
			}
		}
		if d.hasHi {
			if d.eq.Cmp(d.hi) > 0 || (d.hiOpen && d.eq.Cmp(d.hi) == 0) {
				return fmt.Sprintf("profile.%s must equal %s and also be below %s",
					varName, d.eq.RatString(), d.hi.RatString())
			}
		}
		for _, n := range d.neq {
			if d.eq.Cmp(n) == 0 {
				return fmt.Sprintf("profile.%s must both equal and not equal %s",
					varName, n.RatString())
			}
		}
	}
	return ""
}
