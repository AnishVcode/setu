package sema

import (
	"strings"

	"github.com/setu-platform/setu/pkg/srl"
	"github.com/setu-platform/setu/pkg/unit"
)

// checkExpr computes the static type of an expression, reporting every type
// error it finds. It returns TInvalid after reporting so that a single mistake
// does not cascade into a page of noise.
func (c *Checker) checkExpr(e srl.Expr, ctx exprCtx) Type {
	switch x := e.(type) {
	case *srl.NumberLit:
		return TScalar
	case *srl.MoneyLit:
		return TMoney
	case *srl.PeriodLit:
		return TPeriod
	case *srl.StringLit:
		return TText
	case *srl.BoolLit:
		return TBool
	case *srl.DateLit:
		return TDate
	case *srl.UnknownLit:
		return TUntyped

	case *srl.QuantityLit:
		u, ok := unit.Lookup(x.Unit)
		if !ok {
			hint := ""
			if s, ok := unit.SuggestSymbol(x.Unit); ok {
				hint = "did you mean " + s + "?"
			}
			c.errorf(x.Pos, "SEM0040", hint, "unknown unit %q", x.Unit)
			return TInvalid
		}
		return Num(u.Dim)

	case *srl.SetLit:
		return c.checkSetLit(x, ctx)

	case *srl.ProfileRef:
		t, ok := c.varTypes[x.Var]
		if !ok {
			c.errorf(x.Pos, "SEM0010", c.suggest(x.Var, c.g.VarNamesSorted()),
				"%s reads unknown profile variable %q", ctx.what, x.Var)
			return TInvalid
		}
		return t

	case *srl.ObjectRef:
		return c.checkObjectRef(x, ctx)

	case *srl.UnaryExpr:
		return c.checkUnary(x, ctx)

	case *srl.BinaryExpr:
		return c.checkBinary(x, ctx)

	case *srl.InExpr:
		return c.checkIn(x, ctx)

	case *srl.CallExpr:
		return c.checkCall(x, ctx)

	case *srl.IfExpr:
		return c.checkIf(x, ctx)

	case *srl.SlabExpr:
		return c.checkSlab(x, ctx)
	}
	return TInvalid
}

func (c *Checker) checkSetLit(x *srl.SetLit, ctx exprCtx) Type {
	if len(x.Elems) == 0 {
		c.errorf(x.Pos, "SEM0041", "an empty set can never match anything",
			"%s contains an empty set literal", ctx.what)
		return TInvalid
	}
	elem := c.checkExpr(x.Elems[0], ctx)
	for _, el := range x.Elems[1:] {
		t := c.checkExpr(el, ctx)
		if !t.Compatible(elem) {
			c.errorf(el.Position(), "SEM0042", "every member of a set must have the same type",
				"%s mixes %s and %s in one set", ctx.what, elem, t)
			return TInvalid
		}
	}
	return Set(elem)
}

func (c *Checker) checkObjectRef(x *srl.ObjectRef, ctx exprCtx) Type {
	name := x.Name()

	// EnumType.MEMBER
	if len(x.Path) == 2 {
		if _, ok := c.g.Enums[x.Path[0]]; ok {
			if _, ok := c.g.EnumMember(x.Path[0], x.Path[1]); !ok {
				var members []string
				for _, m := range c.g.Enums[x.Path[0]].Members {
					members = append(members, m.Name)
				}
				c.errorf(x.Pos, "SEM0043", "members are: "+strings.Join(members, ", "),
					"%s is not a member of enum %s", name, x.Path[0])
				return TInvalid
			}
			return Enum(x.Path[0])
		}
	}

	if _, ok := c.g.Approvals[name]; ok {
		return Type{Kind: ApprovalRef, Name: name}
	}
	if _, ok := c.g.Documents[name]; ok {
		return Type{Kind: DocumentRef, Name: name}
	}
	if _, ok := c.g.Fees[name]; ok {
		return Type{Kind: FeeRef, Name: name}
	}
	if _, ok := c.g.Obligations[name]; ok {
		return Type{Kind: ObligationRef, Name: name}
	}
	if _, ok := c.g.Incentives[name]; ok {
		return Type{Kind: IncentiveRef, Name: name}
	}

	hint := c.suggest(name, c.g.ApprovalIDs())
	if len(x.Path) == 1 {
		if _, ok := c.g.Vars[x.Path[0]]; ok {
			hint = "profile variables are written with the prefix: profile." + x.Path[0]
		}
	}
	c.errorf(x.Pos, "SEM0010", hint, "%s refers to unknown identifier %s", ctx.what, name)
	return TInvalid
}

func (c *Checker) checkUnary(x *srl.UnaryExpr, ctx exprCtx) Type {
	t := c.checkExpr(x.X, ctx)
	switch x.Op {
	case srl.NOT:
		if t.Kind != Bool && t.Kind != Invalid && t.Kind != Untyped {
			c.errorf(x.Pos, "SEM0044", "", "`not` needs a Bool, found %s", t)
			return TInvalid
		}
		return TBool
	case srl.MINUS:
		if !t.IsNumeric() && t.Kind != Invalid {
			c.errorf(x.Pos, "SEM0044", "", "cannot negate %s", t)
			return TInvalid
		}
		return t
	}
	return TInvalid
}

func (c *Checker) checkBinary(x *srl.BinaryExpr, ctx exprCtx) Type {
	lt := c.checkExpr(x.L, ctx)
	rt := c.checkExpr(x.R, ctx)
	if lt.Kind == Invalid || rt.Kind == Invalid {
		return TInvalid
	}

	switch x.Op {
	case srl.AND, srl.OR:
		if lt.Kind != Bool || rt.Kind != Bool {
			c.errorf(x.Pos, "SEM0045", "",
				"`%s` needs Bool operands, found %s and %s", x.Op, lt, rt)
			return TInvalid
		}
		return TBool

	case srl.EQ, srl.NE:
		if !c.comparable(lt, rt) {
			c.errorf(x.Pos, "SEM0046", c.dimHint(lt, rt),
				"cannot compare %s with %s", lt, rt)
			return TInvalid
		}
		return TBool

	case srl.LT, srl.LE, srl.GT, srl.GE:
		if lt.IsNumeric() && rt.IsNumeric() {
			if lt.Dim != rt.Dim {
				c.errorf(x.Pos, "SEM0047", c.dimHint(lt, rt),
					"cannot order %s against %s: %s and %s are different dimensions",
					lt, rt, lt.Dim, rt.Dim)
				return TInvalid
			}
			return TBool
		}
		if lt.Kind == Date && rt.Kind == Date {
			return TBool
		}
		c.errorf(x.Pos, "SEM0047", "only numbers, quantities, amounts and dates can be ordered",
			"cannot order %s against %s", lt, rt)
		return TInvalid

	case srl.PLUS, srl.MINUS:
		if !lt.IsNumeric() || !rt.IsNumeric() {
			c.errorf(x.Pos, "SEM0048", "", "cannot add %s and %s", lt, rt)
			return TInvalid
		}
		if lt.Dim != rt.Dim {
			c.errorf(x.Pos, "SEM0048", c.dimHint(lt, rt),
				"cannot add %s to %s: %s and %s are different dimensions",
				rt, lt, lt.Dim, rt.Dim)
			return TInvalid
		}
		return lt

	case srl.STAR:
		if !lt.IsNumeric() || !rt.IsNumeric() {
			c.errorf(x.Pos, "SEM0049", "", "cannot multiply %s by %s", lt, rt)
			return TInvalid
		}
		switch {
		case lt.Dim == unit.DimScalar:
			return rt
		case rt.Dim == unit.DimScalar:
			return lt
		}
		// Compound dimensions (kW·h from kW and h) are outside the language:
		// no statutory threshold in scope needs one, and supporting them would
		// mean a dimension algebra a legal author cannot audit.
		c.errorf(x.Pos, "SEM0049",
			"multiply a quantity by a plain number, for example `0.25 * profile.investment`",
			"cannot multiply %s by %s: at least one side must be a plain number", lt, rt)
		return TInvalid

	case srl.SLASH:
		if !lt.IsNumeric() || !rt.IsNumeric() {
			c.errorf(x.Pos, "SEM0049", "", "cannot divide %s by %s", lt, rt)
			return TInvalid
		}
		if rt.Dim == unit.DimScalar {
			return lt
		}
		if lt.Dim == rt.Dim {
			return TScalar
		}
		c.errorf(x.Pos, "SEM0049", "",
			"cannot divide %s by %s: divide by a plain number or by the same dimension", lt, rt)
		return TInvalid
	}
	return TInvalid
}

// comparable decides whether == and != are meaningful between two types.
func (c *Checker) comparable(lt, rt Type) bool {
	if lt.Kind == Untyped || rt.Kind == Untyped {
		return true
	}
	if lt.IsNumeric() && rt.IsNumeric() {
		return lt.Dim == rt.Dim
	}
	return lt.Equal(rt)
}

// dimHint turns the commonest dimension mistakes into an actionable message.
func (c *Checker) dimHint(lt, rt Type) string {
	if lt.IsNumeric() && rt.IsNumeric() {
		if lt.Dim != unit.DimScalar && rt.Dim == unit.DimScalar {
			return "give the number a unit, for example `10 " + unit.Base(lt.Dim).Symbol + "`"
		}
		if rt.Dim != unit.DimScalar && lt.Dim == unit.DimScalar {
			return "give the number a unit, for example `10 " + unit.Base(rt.Dim).Symbol + "`"
		}
		if lt.Dim == unit.DimMassFlowDay && rt.Dim == unit.DimMassFlowYear ||
			lt.Dim == unit.DimMassFlowYear && rt.Dim == unit.DimMassFlowDay {
			return "per-day and per-annum rates do not convert; " +
				"encode the threshold in the unit the statute uses"
		}
	}
	return ""
}

func (c *Checker) checkIn(x *srl.InExpr, ctx exprCtx) Type {
	et := c.checkExpr(x.X, ctx)
	st := c.checkExpr(x.Set, ctx)
	if st.Kind == Invalid || et.Kind == Invalid {
		return TInvalid
	}
	if st.Kind != SetT {
		c.errorf(x.Pos, "SEM0050", "the right of `in` is a set, written [a, b, c]",
			"`in` needs a set on the right, found %s", st)
		return TInvalid
	}
	if st.Elem != nil && !et.Compatible(*st.Elem) {
		c.errorf(x.Pos, "SEM0050", c.dimHint(et, *st.Elem),
			"cannot test whether %s is in %s", et, st)
		return TInvalid
	}
	return TBool
}

func (c *Checker) checkIf(x *srl.IfExpr, ctx exprCtx) Type {
	ct := c.checkExpr(x.Cond, ctx)
	if ct.Kind != Bool && ct.Kind != Invalid {
		c.errorf(x.Cond.Position(), "SEM0051", "", "`if` needs a Bool condition, found %s", ct)
	}
	tt := c.checkExpr(x.Then, ctx)
	et := c.checkExpr(x.Else, ctx)
	if !tt.Compatible(et) {
		c.errorf(x.Pos, "SEM0052", "both branches must produce the same type",
			"`if` branches produce %s and %s", tt, et)
		return TInvalid
	}
	if tt.Kind == Untyped {
		return et
	}
	return tt
}

func (c *Checker) checkSlab(x *srl.SlabExpr, ctx exprCtx) Type {
	st := c.checkExpr(x.Subject, ctx)
	var result Type
	haveResult := false

	consider := func(t Type, e srl.Expr) {
		if t.Kind == Invalid {
			return
		}
		if !haveResult {
			result, haveResult = t, true
			return
		}
		if !t.Compatible(result) {
			c.errorf(e.Position(), "SEM0053", "every arm of a slab must produce the same type",
				"slab arms produce %s and %s", result, t)
		}
	}

	for _, arm := range x.Arms {
		bt := c.checkExpr(arm.Bound, ctx)
		if bt.Kind != Invalid && st.Kind != Invalid && !c.comparable(st, bt) {
			c.errorf(arm.Pos, "SEM0054", c.dimHint(st, bt),
				"slab bound %s cannot be compared with the subject, which is %s", bt, st)
		}
		consider(c.checkExpr(arm.Val, ctx), arm.Val)
	}
	if x.Default != nil {
		consider(c.checkExpr(x.Default, ctx), x.Default)
	}
	if !haveResult {
		return TInvalid
	}
	return result
}

// builtinSig describes a builtin's parameter and result types.
type builtinSig struct {
	params []Type // nil entry means "any type"
	result Type
	// variadicSameType marks min and max, which take one or more arguments of
	// a single numeric type and return that type.
	variadicSameType bool
	// resultFromArg makes the result type follow an argument's type.
	resultFromArg int // -1 when the result is fixed
}

var builtinSigs = map[string]builtinSig{
	"min":           {variadicSameType: true, resultFromArg: 0},
	"max":           {variadicSameType: true, resultFromArg: 0},
	"abs":           {params: []Type{{Kind: Number}}, resultFromArg: 0},
	"round":         {params: []Type{{Kind: Number}}, resultFromArg: 0},
	"floor":         {params: []Type{{Kind: Number}}, resultFromArg: 0},
	"ceil":          {params: []Type{{Kind: Number}}, resultFromArg: 0},
	"defined":       {params: []Type{TInvalid}, result: TBool, resultFromArg: -1},
	"within":        {params: []Type{TGeo, TText}, result: TBool, resultFromArg: -1},
	"distance_to":   {params: []Type{TGeo, TText}, result: TLength, resultFromArg: -1},
	"zone_of":       {params: []Type{TGeo, TText}, result: TText, resultFromArg: -1},
	"starts_with":   {params: []Type{TText, TText}, result: TBool, resultFromArg: -1},
	"contains_text": {params: []Type{TText, TText}, result: TBool, resultFromArg: -1},
	"required":      {params: []Type{{Kind: ApprovalRef}}, result: TBool, resultFromArg: -1},
	"exempted":      {params: []Type{{Kind: ApprovalRef}}, result: TBool, resultFromArg: -1},
	"years_between": {params: []Type{TDate, TDate}, result: TScalar, resultFromArg: -1},
	"days_between":  {params: []Type{TDate, TDate}, result: TScalar, resultFromArg: -1},
	"point":         {params: []Type{TScalar, TScalar}, result: TGeo, resultFromArg: -1},
}

func (c *Checker) checkCall(x *srl.CallExpr, ctx exprCtx) Type {
	sig, ok := builtinSigs[x.Fn]
	if !ok {
		c.errorf(x.Pos, "SEM0055", "functions: "+strings.Join(srl.BuiltinNames(), ", "),
			"unknown function %q", x.Fn)
		for _, a := range x.Args {
			c.checkExpr(a, ctx)
		}
		return TInvalid
	}

	argTypes := make([]Type, len(x.Args))
	for i, a := range x.Args {
		argTypes[i] = c.checkExpr(a, ctx)
	}

	if sig.variadicSameType {
		if len(x.Args) == 0 {
			c.errorf(x.Pos, "SEM0056", "", "%s needs at least one argument", x.Fn)
			return TInvalid
		}
		first := argTypes[0]
		if !first.IsNumeric() && first.Kind != Invalid {
			c.errorf(x.Args[0].Position(), "SEM0056", "",
				"%s needs numbers, found %s", x.Fn, first)
			return TInvalid
		}
		for i, t := range argTypes[1:] {
			if t.Kind == Invalid {
				continue
			}
			if !t.Equal(first) {
				c.errorf(x.Args[i+1].Position(), "SEM0056", c.dimHint(first, t),
					"%s mixes %s and %s", x.Fn, first, t)
				return TInvalid
			}
		}
		return first
	}

	if len(x.Args) != len(sig.params) {
		c.errorf(x.Pos, "SEM0056", "",
			"%s takes %d argument(s), found %d", x.Fn, len(sig.params), len(x.Args))
		return TInvalid
	}
	for i, want := range sig.params {
		got := argTypes[i]
		if got.Kind == Invalid {
			continue
		}
		if want.Kind == Invalid { // "any"
			continue
		}
		if want.Kind == Number && want.Dim == unit.DimScalar && got.IsNumeric() {
			continue // a numeric parameter accepts any dimension
		}
		if !got.Equal(want) {
			c.errorf(x.Args[i].Position(), "SEM0056", "",
				"%s argument %d is %s, expected %s", x.Fn, i+1, got, want)
			return TInvalid
		}
	}
	if sig.resultFromArg >= 0 && sig.resultFromArg < len(argTypes) {
		return argTypes[sig.resultFromArg]
	}
	return sig.result
}
