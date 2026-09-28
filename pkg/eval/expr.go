package eval

import (
	"math/big"
	"sort"
	"strings"

	"github.com/setu-platform/setu/pkg/srl"
	"github.com/setu-platform/setu/pkg/unit"
	"github.com/setu-platform/setu/pkg/value"
)

// res is the result of evaluating one expression node.
//
// Unknowns is the reason an UNKNOWN result is UNKNOWN: the profile variables
// whose absence actually determined the outcome. It is computed by relevance,
// not by collecting every variable the expression mentions. In
// `A and B`, where A is definitely false, the unanswered variables in B are
// irrelevant — the answer is false either way — and asking about them would be
// a pointless question put to an applicant.
type res struct {
	v        value.Value
	unknowns map[string]bool
	// note is a short human-readable rendering used in the derivation trace.
	note string
	// children are sub-results, retained only when tracing.
	children []TraceNode
}

func known(v value.Value) res { return res{v: v} }

func unknownRes(vars ...string) res {
	m := map[string]bool{}
	for _, v := range vars {
		if v != "" {
			m[v] = true
		}
	}
	return res{v: value.Null, unknowns: m}
}

func mergeUnknowns(rs ...res) map[string]bool {
	out := map[string]bool{}
	for _, r := range rs {
		for k := range r.unknowns {
			out[k] = true
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func sortedUnknowns(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TraceNode is one step of a derivation trace: the sub-expression as written,
// what it evaluated to, and why.
type TraceNode struct {
	Expr     string      `json:"expr"`
	Result   string      `json:"result"`
	Detail   string      `json:"detail,omitempty"`
	Children []TraceNode `json:"children,omitempty"`
}

// evalCtx carries everything an expression needs.
type evalCtx struct {
	in *Input
	// approvalStatus lets required() and exempted() read settled approvals.
	approvalStatus map[string]*ApprovalStatus
	// trace enables construction of the derivation trace. Evaluation is
	// identical either way; only the recording differs.
	trace bool
}

// evalExpr evaluates an expression under three-valued semantics.
func (c *evalCtx) evalExpr(e srl.Expr) res {
	switch x := e.(type) {
	case *srl.NumberLit:
		return c.leaf(e, known(value.Rat(x.Val)))
	case *srl.MoneyLit:
		return c.leaf(e, known(value.Money(x.Val)))
	case *srl.PeriodLit:
		return c.leaf(e, known(value.OfPeriod(x.Val)))
	case *srl.StringLit:
		return c.leaf(e, known(value.Text(x.Val)))
	case *srl.BoolLit:
		return c.leaf(e, known(value.Bool(x.Val)))
	case *srl.DateLit:
		return c.leaf(e, known(value.OfDate(x.Val)))
	case *srl.UnknownLit:
		return c.leaf(e, res{v: value.Null})

	case *srl.QuantityLit:
		u, ok := unit.Lookup(x.Unit)
		if !ok {
			return c.leaf(e, res{v: value.Null})
		}
		return c.leaf(e, known(value.Quantity(x.Val, u)))

	case *srl.SetLit:
		var members []value.Value
		var rs []res
		for _, el := range x.Elems {
			r := c.evalExpr(el)
			rs = append(rs, r)
			if r.v.IsUnknown() {
				out := res{v: value.Null, unknowns: mergeUnknowns(rs...)}
				return c.node(e, out, rs)
			}
			members = append(members, r.v)
		}
		return c.node(e, known(value.SetOf(members)), rs)

	case *srl.ProfileRef:
		v := c.in.Profile.Get(x.Var)
		if v.IsUnknown() {
			return c.leafDetail(e, unknownRes(x.Var), "not yet answered")
		}
		return c.leafDetail(e, known(v), v.String())

	case *srl.ObjectRef:
		return c.evalObjectRef(x)

	case *srl.UnaryExpr:
		return c.evalUnary(x)

	case *srl.BinaryExpr:
		return c.evalBinary(x)

	case *srl.InExpr:
		l := c.evalExpr(x.X)
		s := c.evalExpr(x.Set)
		t := s.v.Contains(l.v)
		out := res{v: triValue(t)}
		if t == value.Unknown {
			out.unknowns = mergeUnknowns(l, s)
		}
		return c.node(x, out, []res{l, s})

	case *srl.CallExpr:
		return c.evalCall(x)

	case *srl.IfExpr:
		cond := c.evalExpr(x.Cond)
		switch cond.v.Truthy() {
		case value.True:
			r := c.evalExpr(x.Then)
			return c.node(x, r, []res{cond, r})
		case value.False:
			r := c.evalExpr(x.Else)
			return c.node(x, r, []res{cond, r})
		}
		return c.node(x, res{v: value.Null, unknowns: cond.unknowns}, []res{cond})

	case *srl.SlabExpr:
		return c.evalSlab(x)
	}
	return res{v: value.Null}
}

func triValue(t value.Tri) value.Value {
	switch t {
	case value.True:
		return value.Bool(true)
	case value.False:
		return value.Bool(false)
	}
	return value.Null
}

func (c *evalCtx) evalObjectRef(x *srl.ObjectRef) res {
	// EnumType.MEMBER is the only object reference with a runtime value; the
	// rest (approvals, documents, fees) are names consumed by builtins and
	// effects, and are represented as text.
	if len(x.Path) == 2 {
		return c.leaf(x, known(value.Enum(x.Path[0], x.Path[1])))
	}
	return c.leaf(x, known(value.Text(x.Name())))
}

func (c *evalCtx) evalUnary(x *srl.UnaryExpr) res {
	r := c.evalExpr(x.X)
	switch x.Op {
	case srl.NOT:
		t := r.v.Truthy().Not()
		out := res{v: triValue(t)}
		if t == value.Unknown {
			out.unknowns = r.unknowns
		}
		return c.node(x, out, []res{r})
	case srl.MINUS:
		if r.v.IsUnknown() || !r.v.Numeric() {
			return c.node(x, res{v: value.Null, unknowns: r.unknowns}, []res{r})
		}
		neg := new(big.Rat).Neg(r.v.Num)
		out := r.v
		out.Num = neg
		return c.node(x, known(out), []res{r})
	}
	return res{v: value.Null}
}

func (c *evalCtx) evalBinary(x *srl.BinaryExpr) res {
	switch x.Op {
	case srl.AND, srl.OR:
		return c.evalLogical(x)
	}

	l := c.evalExpr(x.L)
	r := c.evalExpr(x.R)

	switch x.Op {
	case srl.EQ, srl.NE:
		t := l.v.Equal(r.v)
		if x.Op == srl.NE {
			t = t.Not()
		}
		out := res{v: triValue(t)}
		if t == value.Unknown {
			out.unknowns = mergeUnknowns(l, r)
		}
		return c.node(x, out, []res{l, r})

	case srl.LT, srl.LE, srl.GT, srl.GE:
		return c.node(x, c.compare(x.Op, l, r), []res{l, r})

	case srl.PLUS, srl.MINUS, srl.STAR, srl.SLASH:
		return c.node(x, c.arith(x.Op, l, r), []res{l, r})
	}
	return res{v: value.Null}
}

// evalLogical implements Kleene conjunction and disjunction with the relevance
// rule that gives an honest list of outstanding questions.
func (c *evalCtx) evalLogical(x *srl.BinaryExpr) res {
	l := c.evalExpr(x.L)
	r := c.evalExpr(x.R)
	lt, rt := l.v.Truthy(), r.v.Truthy()

	var t value.Tri
	if x.Op == srl.AND {
		t = lt.And(rt)
	} else {
		t = lt.Or(rt)
	}
	out := res{v: triValue(t)}
	if t == value.Unknown {
		// Only the operands that are themselves undetermined are responsible.
		var contributing []res
		if lt == value.Unknown {
			contributing = append(contributing, l)
		}
		if rt == value.Unknown {
			contributing = append(contributing, r)
		}
		out.unknowns = mergeUnknowns(contributing...)
	}
	return c.node(x, out, []res{l, r})
}

func (c *evalCtx) compare(op srl.Kind, l, r res) res {
	if l.v.IsUnknown() || r.v.IsUnknown() {
		return res{v: value.Null, unknowns: mergeUnknowns(l, r)}
	}
	var cmp int
	switch {
	case l.v.Numeric() && r.v.Numeric():
		n, err := l.v.CompareNum(r.v)
		if err != nil {
			// The type checker rejects this statically; reaching it means a
			// determination cannot be trusted, so it is reported as unknown
			// rather than guessed.
			return res{v: value.Null}
		}
		cmp = n
	case l.v.Kind == value.KindDate && r.v.Kind == value.KindDate:
		cmp = l.v.Date.Compare(r.v.Date)
	default:
		return res{v: value.Null}
	}
	var b bool
	switch op {
	case srl.LT:
		b = cmp < 0
	case srl.LE:
		b = cmp <= 0
	case srl.GT:
		b = cmp > 0
	case srl.GE:
		b = cmp >= 0
	}
	return known(value.Bool(b))
}

func (c *evalCtx) arith(op srl.Kind, l, r res) res {
	if l.v.IsUnknown() || r.v.IsUnknown() {
		return res{v: value.Null, unknowns: mergeUnknowns(l, r)}
	}
	if !l.v.Numeric() || !r.v.Numeric() {
		return res{v: value.Null}
	}

	// Addition and subtraction happen in the left operand's unit, so that
	// `INR 1000 + INR 50` stays in rupees and a quantity keeps the unit the
	// rule author wrote.
	switch op {
	case srl.PLUS, srl.MINUS:
		rv, err := unit.Convert(r.v.Num, r.v.Unit, l.v.Unit)
		if err != nil {
			return res{v: value.Null}
		}
		n := new(big.Rat)
		if op == srl.PLUS {
			n.Add(l.v.Num, rv)
		} else {
			n.Sub(l.v.Num, rv)
		}
		out := l.v
		out.Num = n
		return known(out)

	case srl.STAR:
		switch {
		case r.v.Unit.Dim == unit.DimScalar:
			out := l.v
			out.Num = new(big.Rat).Mul(l.v.Num, r.v.Num)
			return known(out)
		case l.v.Unit.Dim == unit.DimScalar:
			out := r.v
			out.Num = new(big.Rat).Mul(l.v.Num, r.v.Num)
			return known(out)
		}
		return res{v: value.Null}

	case srl.SLASH:
		if r.v.Num.Sign() == 0 {
			// Division by zero in a fee formula is a content defect, not a
			// panic. It surfaces as an uncomputable fee that a human resolves.
			return res{v: value.Null}
		}
		if r.v.Unit.Dim == unit.DimScalar {
			out := l.v
			out.Num = new(big.Rat).Quo(l.v.Num, r.v.Num)
			return known(out)
		}
		if l.v.Unit.Dim == r.v.Unit.Dim {
			a := unit.ToBase(l.v.Num, l.v.Unit)
			b := unit.ToBase(r.v.Num, r.v.Unit)
			return known(value.Rat(new(big.Rat).Quo(a, b)))
		}
	}
	return res{v: value.Null}
}

func (c *evalCtx) evalSlab(x *srl.SlabExpr) res {
	subj := c.evalExpr(x.Subject)
	if subj.v.IsUnknown() {
		if x.Default != nil {
			// The subject is unknown, so no arm can be selected; the schedule is
			// undetermined even though a default exists.
			return c.node(x, res{v: value.Null, unknowns: subj.unknowns}, []res{subj})
		}
		return c.node(x, res{v: value.Null, unknowns: subj.unknowns}, []res{subj})
	}
	kids := []res{subj}
	for _, arm := range x.Arms {
		bound := c.evalExpr(arm.Bound)
		kids = append(kids, bound)
		m := c.compare(arm.Op, subj, bound)
		if arm.Op == srl.EQ {
			m = res{v: triValue(subj.v.Equal(bound.v))}
		}
		switch m.v.Truthy() {
		case value.True:
			val := c.evalExpr(arm.Val)
			kids = append(kids, val)
			return c.node(x, val, kids)
		case value.Unknown:
			return c.node(x, res{v: value.Null, unknowns: mergeUnknowns(subj, bound)}, kids)
		}
	}
	if x.Default == nil {
		return c.node(x, res{v: value.Null}, kids)
	}
	d := c.evalExpr(x.Default)
	kids = append(kids, d)
	return c.node(x, d, kids)
}

func (c *evalCtx) evalCall(x *srl.CallExpr) res {
	args := make([]res, len(x.Args))
	for i, a := range x.Args {
		args[i] = c.evalExpr(a)
	}

	switch x.Fn {
	// defined() is the one builtin that does not propagate UNKNOWN: its whole
	// purpose is to let a rule branch on whether an answer exists.
	case "defined":
		return c.node(x, known(value.Bool(!args[0].v.IsUnknown())), args)

	case "min", "max":
		return c.node(x, c.minMax(x.Fn, args), args)

	case "abs":
		if args[0].v.IsUnknown() || !args[0].v.Numeric() {
			return c.node(x, res{v: value.Null, unknowns: args[0].unknowns}, args)
		}
		out := args[0].v
		out.Num = new(big.Rat).Abs(args[0].v.Num)
		return c.node(x, known(out), args)

	case "round", "floor", "ceil":
		return c.node(x, c.rounding(x.Fn, args[0]), args)

	case "point":
		if args[0].v.IsUnknown() || args[1].v.IsUnknown() {
			return c.node(x, res{v: value.Null, unknowns: mergeUnknowns(args...)}, args)
		}
		lat, _ := args[0].v.Num.Float64()
		lon, _ := args[1].v.Num.Float64()
		return c.node(x, known(value.OfGeo(value.Point(lat, lon))), args)

	case "within":
		return c.node(x, c.within(args), args)

	case "distance_to":
		return c.node(x, c.distanceTo(args), args)

	case "zone_of":
		return c.node(x, c.zoneOf(args), args)

	case "starts_with", "contains_text":
		if args[0].v.IsUnknown() || args[1].v.IsUnknown() {
			return c.node(x, res{v: value.Null, unknowns: mergeUnknowns(args...)}, args)
		}
		a, b := args[0].v.Text, args[1].v.Text
		if args[0].v.Kind == value.KindEnum {
			a = args[0].v.Enum
		}
		if x.Fn == "starts_with" {
			return c.node(x, known(value.Bool(strings.HasPrefix(a, b))), args)
		}
		return c.node(x, known(value.Bool(strings.Contains(a, b))), args)

	case "required", "exempted":
		return c.node(x, c.approvalPredicate(x, args), args)

	case "years_between", "days_between":
		if args[0].v.IsUnknown() || args[1].v.IsUnknown() {
			return c.node(x, res{v: value.Null, unknowns: mergeUnknowns(args...)}, args)
		}
		days := args[0].v.Date.DaysBetween(args[1].v.Date)
		if x.Fn == "days_between" {
			return c.node(x, known(value.Int(int64(days))), args)
		}
		// Whole years between two civil dates, counting completed anniversaries.
		y := args[1].v.Date.Year - args[0].v.Date.Year
		anniversary := value.Date{Year: args[1].v.Date.Year,
			Month: args[0].v.Date.Month, Day: args[0].v.Date.Day}
		if args[1].v.Date.Before(anniversary) {
			y--
		}
		return c.node(x, known(value.Int(int64(y))), args)
	}
	return c.node(x, res{v: value.Null}, args)
}

func (c *evalCtx) minMax(fn string, args []res) res {
	if len(args) == 0 {
		return res{v: value.Null}
	}
	best := args[0]
	if best.v.IsUnknown() {
		return res{v: value.Null, unknowns: mergeUnknowns(args...)}
	}
	for _, a := range args[1:] {
		if a.v.IsUnknown() {
			return res{v: value.Null, unknowns: mergeUnknowns(args...)}
		}
		cmp, err := a.v.CompareNum(best.v)
		if err != nil {
			return res{v: value.Null}
		}
		if (fn == "min" && cmp < 0) || (fn == "max" && cmp > 0) {
			best = a
		}
	}
	return known(best.v)
}

func (c *evalCtx) rounding(fn string, a res) res {
	if a.v.IsUnknown() || !a.v.Numeric() {
		return res{v: value.Null, unknowns: a.unknowns}
	}
	out := a.v
	out.Num = roundRat(a.v.Num, fn)
	return known(out)
}

// roundRat rounds an exact rational to a whole number. Half values round away
// from zero, which is the convention Indian fee notifications assume.
func roundRat(r *big.Rat, mode string) *big.Rat {
	num, den := r.Num(), r.Denom()
	q, rem := new(big.Int).QuoRem(num, den, new(big.Int))
	if rem.Sign() == 0 {
		return new(big.Rat).SetInt(q)
	}
	neg := r.Sign() < 0
	switch mode {
	case "floor":
		if neg {
			q.Sub(q, big.NewInt(1))
		}
	case "ceil":
		if !neg {
			q.Add(q, big.NewInt(1))
		}
	default: // round half away from zero
		twice := new(big.Int).Abs(rem)
		twice.Mul(twice, big.NewInt(2))
		if twice.Cmp(den) >= 0 {
			if neg {
				q.Sub(q, big.NewInt(1))
			} else {
				q.Add(q, big.NewInt(1))
			}
		}
	}
	return new(big.Rat).SetInt(q)
}

func (c *evalCtx) within(args []res) res {
	if args[0].v.IsUnknown() || args[1].v.IsUnknown() {
		return res{v: value.Null, unknowns: mergeUnknowns(args...)}
	}
	if c.in.Layers == nil {
		return res{v: value.Null}
	}
	t, _ := c.in.Layers.Within(args[0].v.Geo, args[1].v.Text)
	return res{v: triValue(t)}
}

func (c *evalCtx) distanceTo(args []res) res {
	if args[0].v.IsUnknown() || args[1].v.IsUnknown() {
		return res{v: value.Null, unknowns: mergeUnknowns(args...)}
	}
	if c.in.Layers == nil {
		return res{v: value.Null}
	}
	t, d, _ := c.in.Layers.DistanceTo(args[0].v.Geo, args[1].v.Text)
	if t != value.True {
		return res{v: value.Null}
	}
	return known(value.Quantity(d, unit.Metre))
}

func (c *evalCtx) zoneOf(args []res) res {
	if args[0].v.IsUnknown() || args[1].v.IsUnknown() {
		return res{v: value.Null, unknowns: mergeUnknowns(args...)}
	}
	if c.in.Layers == nil {
		return res{v: value.Null}
	}
	t, f := c.in.Layers.Within(args[0].v.Geo, args[1].v.Text)
	if t != value.True || f == nil {
		return res{v: value.Null}
	}
	return known(value.Text(f.Name))
}

// approvalPredicate reads the settled status of another approval. Compilation
// guarantees the approval has already been settled when this runs.
func (c *evalCtx) approvalPredicate(x *srl.CallExpr, args []res) res {
	ref, ok := x.Args[0].(*srl.ObjectRef)
	if !ok {
		return res{v: value.Null}
	}
	st, ok := c.approvalStatus[ref.Name()]
	if !ok || st == nil {
		return res{v: value.Null}
	}
	switch x.Fn {
	case "required":
		if st.Required == value.Unknown {
			return res{v: value.Null, unknowns: setOf(st.UnresolvedVars)}
		}
		return known(value.Bool(st.Required == value.True))
	case "exempted":
		if st.Required == value.Unknown {
			return res{v: value.Null, unknowns: setOf(st.UnresolvedVars)}
		}
		return known(value.Bool(st.Exempted))
	}
	return res{v: value.Null}
}

func setOf(ss []string) map[string]bool {
	if len(ss) == 0 {
		return nil
	}
	m := map[string]bool{}
	for _, s := range ss {
		m[s] = true
	}
	return m
}

// ---------- trace construction ----------

func (c *evalCtx) leaf(e srl.Expr, r res) res { return c.leafDetail(e, r, "") }

func (c *evalCtx) leafDetail(e srl.Expr, r res, detail string) res {
	if !c.trace {
		return r
	}
	r.children = []TraceNode{{
		Expr: srl.FormatExpr(e), Result: resultText(r), Detail: detail,
	}}
	return r
}

func (c *evalCtx) node(e srl.Expr, out res, kids []res) res {
	if !c.trace {
		out.children = nil
		return out
	}
	var children []TraceNode
	for _, k := range kids {
		children = append(children, k.children...)
	}
	out.children = []TraceNode{{
		Expr: srl.FormatExpr(e), Result: resultText(out), Children: children,
	}}
	return out
}

func resultText(r res) string {
	if r.v.IsUnknown() {
		if len(r.unknowns) > 0 {
			return "unknown (awaiting: " + strings.Join(sortedUnknowns(r.unknowns), ", ") + ")"
		}
		return "unknown"
	}
	return r.v.String()
}
