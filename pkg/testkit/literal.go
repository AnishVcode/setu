package testkit

import (
	"fmt"
	"math/big"

	"github.com/setu-platform/setu/pkg/srl"
	"github.com/setu-platform/setu/pkg/unit"
	"github.com/setu-platform/setu/pkg/value"
)

// literalValue converts a literal expression from a test's `given` block into a
// runtime value.
//
// Only literals are accepted. A test whose input could itself be computed would
// be a test of the computation, not of the rule, and a regulatory author
// reading the case must be able to see the exact input at a glance.
func literalValue(e srl.Expr) (value.Value, error) {
	switch x := e.(type) {
	case *srl.NumberLit:
		return value.Rat(x.Val), nil
	case *srl.MoneyLit:
		return value.Money(x.Val), nil
	case *srl.PeriodLit:
		return value.OfPeriod(x.Val), nil
	case *srl.StringLit:
		return value.Text(x.Val), nil
	case *srl.BoolLit:
		return value.Bool(x.Val), nil
	case *srl.DateLit:
		return value.OfDate(x.Val), nil
	case *srl.UnknownLit:
		return value.Null, nil
	case *srl.QuantityLit:
		u, ok := unit.Lookup(x.Unit)
		if !ok {
			return value.Null, fmt.Errorf("unknown unit %q", x.Unit)
		}
		return value.Quantity(x.Val, u), nil
	case *srl.ObjectRef:
		if len(x.Path) == 2 {
			return value.Enum(x.Path[0], x.Path[1]), nil
		}
		return value.Text(x.Name()), nil
	case *srl.SetLit:
		var members []value.Value
		for _, el := range x.Elems {
			v, err := literalValue(el)
			if err != nil {
				return value.Null, err
			}
			members = append(members, v)
		}
		return value.SetOf(members), nil
	case *srl.CallExpr:
		// point(lat, lon) is the one call accepted as a test input: a location
		// has no literal syntax, and a rule keyed on where the site is cannot
		// otherwise be given a positive test case.
		if x.Fn == "point" && len(x.Args) == 2 {
			lat, err := literalValue(x.Args[0])
			if err != nil {
				return value.Null, err
			}
			lon, err := literalValue(x.Args[1])
			if err != nil {
				return value.Null, err
			}
			if !lat.Numeric() || !lon.Numeric() {
				return value.Null, fmt.Errorf("point() takes two numbers")
			}
			la, _ := lat.Num.Float64()
			lo, _ := lon.Num.Float64()
			return value.OfGeo(value.Point(la, lo)), nil
		}
		return value.Null, fmt.Errorf("test inputs must be literal values, found %s",
			srl.FormatExpr(e))

	case *srl.UnaryExpr:
		if x.Op == srl.MINUS {
			v, err := literalValue(x.X)
			if err != nil {
				return value.Null, err
			}
			if !v.Numeric() {
				return value.Null, fmt.Errorf("cannot negate a non-number")
			}
			neg := v
			neg.Num = negate(v.Num)
			return neg, nil
		}
	}
	return value.Null, fmt.Errorf("test inputs must be literal values, found %s",
		srl.FormatExpr(e))
}

func negate(r *big.Rat) *big.Rat { return new(big.Rat).Neg(r) }
