package value

import (
	"fmt"
	"math/big"
	"sort"
	"strings"

	"github.com/setu-platform/setu/pkg/unit"
)

// Kind is the runtime type tag of a Value.
type Kind uint8

const (
	KindNull Kind = iota // an unanswered profile variable
	KindBool
	KindNumber // scalar number, or a dimensioned quantity when Unit is set
	KindMoney
	KindText
	KindDate
	KindPeriod
	KindEnum
	KindSet
	KindGeo
)

func (k Kind) String() string {
	switch k {
	case KindNull:
		return "null"
	case KindBool:
		return "bool"
	case KindNumber:
		return "number"
	case KindMoney:
		return "money"
	case KindText:
		return "text"
	case KindDate:
		return "date"
	case KindPeriod:
		return "period"
	case KindEnum:
		return "enum"
	case KindSet:
		return "set"
	case KindGeo:
		return "geo"
	}
	return "?"
}

// Value is a runtime value.
//
// Numbers are exact rationals. No evaluation in this engine ever touches a
// float64: a fee computed as 1/3 of a slab must produce the same paise on every
// machine, forever, and be reproducible in a tribunal years later (P1, P6).
type Value struct {
	Kind   Kind
	Known  bool // false means UNKNOWN: the profile did not answer
	Bool   bool
	Num    *big.Rat
	Unit   unit.Unit
	Text   string
	Enum   string // enum member name
	EnumTy string // enum type name
	Date   Date
	Period Period
	Set    []Value // sorted and deduplicated on construction
	Geo    *Geo
}

// Null is the unknown value. Every profile variable the applicant has not
// answered evaluates to this.
var Null = Value{Kind: KindNull, Known: false}

// Bool constructs a known boolean.
func Bool(b bool) Value { return Value{Kind: KindBool, Known: true, Bool: b} }

// Int constructs a dimensionless integer.
func Int(n int64) Value {
	return Value{Kind: KindNumber, Known: true, Num: big.NewRat(n, 1), Unit: unit.None}
}

// Rat constructs a dimensionless exact number.
func Rat(r *big.Rat) Value {
	return Value{Kind: KindNumber, Known: true, Num: new(big.Rat).Set(r), Unit: unit.None}
}

// In returns the same quantity expressed in another unit of the same
// dimension, for rendering an answer in the unit a rule author declared.
func (v Value) In(u unit.Unit) (Value, bool) {
	if !v.Numeric() || v.IsUnknown() || !v.Unit.Convertible(u) {
		return v, false
	}
	n, err := unit.Convert(v.Num, v.Unit, u)
	if err != nil {
		return v, false
	}
	out := v
	out.Num = n
	out.Unit = u
	return out, true
}

// Quantity constructs a dimensioned number.
func Quantity(r *big.Rat, u unit.Unit) Value {
	return Value{Kind: KindNumber, Known: true, Num: new(big.Rat).Set(r), Unit: u}
}

// Money constructs an amount in rupees.
func Money(r *big.Rat) Value {
	return Value{Kind: KindMoney, Known: true, Num: new(big.Rat).Set(r), Unit: unit.INR}
}

// MoneyInt constructs a whole-rupee amount.
func MoneyInt(n int64) Value { return Money(big.NewRat(n, 1)) }

// Text constructs a string value.
func Text(s string) Value { return Value{Kind: KindText, Known: true, Text: s} }

// Enum constructs an enum member of a named enum type.
func Enum(typeName, member string) Value {
	return Value{Kind: KindEnum, Known: true, EnumTy: typeName, Enum: member}
}

// OfDate constructs a date value.
func OfDate(d Date) Value { return Value{Kind: KindDate, Known: true, Date: d} }

// OfPeriod constructs a period value.
func OfPeriod(p Period) Value { return Value{Kind: KindPeriod, Known: true, Period: p} }

// OfGeo constructs a geometry value.
func OfGeo(g *Geo) Value { return Value{Kind: KindGeo, Known: true, Geo: g} }

// SetOf constructs a set, sorting and deduplicating members so that the same
// logical set always has the same representation and the same hash.
func SetOf(members []Value) Value {
	cp := make([]Value, len(members))
	copy(cp, members)
	sort.SliceStable(cp, func(i, j int) bool { return cp[i].canonical() < cp[j].canonical() })
	out := cp[:0]
	var last string
	for i, m := range cp {
		c := m.canonical()
		if i > 0 && c == last {
			continue
		}
		last = c
		out = append(out, m)
	}
	return Value{Kind: KindSet, Known: true, Set: out}
}

// IsUnknown reports whether the value is the third truth value's data analogue.
func (v Value) IsUnknown() bool { return !v.Known || v.Kind == KindNull }

// Truthy converts a boolean value to a Tri, mapping unknown values to Unknown.
func (v Value) Truthy() Tri {
	if v.IsUnknown() {
		return Unknown
	}
	if v.Kind != KindBool {
		return Unknown
	}
	return TriOf(v.Bool)
}

// Numeric reports whether the value participates in arithmetic.
func (v Value) Numeric() bool { return v.Kind == KindNumber || v.Kind == KindMoney }

// String renders a value for explanations and traces. Numbers render exactly:
// a repeating rational is shown as a fraction rather than a rounded decimal, so
// nothing is lost between the engine and the citizen-facing explanation.
func (v Value) String() string {
	if v.IsUnknown() {
		return "unknown"
	}
	switch v.Kind {
	case KindBool:
		if v.Bool {
			return "yes"
		}
		return "no"
	case KindNumber:
		s := formatRat(v.Num)
		if v.Unit.IsNone() {
			return s
		}
		return s + " " + v.Unit.Label()
	case KindMoney:
		return FormatINR(v.Num)
	case KindText:
		return v.Text
	case KindEnum:
		return v.Enum
	case KindDate:
		return v.Date.String()
	case KindPeriod:
		return v.Period.String()
	case KindSet:
		parts := make([]string, len(v.Set))
		for i, m := range v.Set {
			parts[i] = m.String()
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case KindGeo:
		return v.Geo.String()
	}
	return "?"
}

// canonical is a total, stable ordering key used for set normalisation and
// canonical serialisation. It must never depend on map iteration order.
func (v Value) canonical() string {
	if v.IsUnknown() {
		return "0:null"
	}
	switch v.Kind {
	case KindBool:
		return fmt.Sprintf("1:%t", v.Bool)
	case KindNumber:
		return fmt.Sprintf("2:%s:%s", v.Unit.Symbol, v.Num.RatString())
	case KindMoney:
		return "3:" + v.Num.RatString()
	case KindText:
		return "4:" + v.Text
	case KindEnum:
		return "5:" + v.EnumTy + "." + v.Enum
	case KindDate:
		return "6:" + v.Date.String()
	case KindPeriod:
		return fmt.Sprintf("7:%d/%d/%d", v.Period.Years, v.Period.Months, v.Period.Days)
	case KindSet:
		parts := make([]string, len(v.Set))
		for i, m := range v.Set {
			parts[i] = m.canonical()
		}
		return "8:[" + strings.Join(parts, "|") + "]"
	case KindGeo:
		return "9:" + v.Geo.canonical()
	}
	return "?"
}

// Canonical exposes the stable ordering key for canonical serialisation.
func (v Value) Canonical() string { return v.canonical() }

// Equal is exact structural equality. Quantities of the same dimension compare
// equal across units: 1 MLD equals 1000 KLD.
func (v Value) Equal(o Value) Tri {
	if v.IsUnknown() || o.IsUnknown() {
		return Unknown
	}
	if v.Numeric() && o.Numeric() {
		c, err := v.CompareNum(o)
		if err != nil {
			return Unknown
		}
		return TriOf(c == 0)
	}
	if v.Kind != o.Kind {
		return False
	}
	return TriOf(v.canonical() == o.canonical())
}

// CompareNum orders two numeric values, converting to a common base.
// It errors when the dimensions differ, which the type checker should already
// have prevented; reaching it at runtime is a bug in the checker, not a reason
// to guess an ordering.
func (v Value) CompareNum(o Value) (int, error) {
	if !v.Numeric() || !o.Numeric() {
		return 0, fmt.Errorf("cannot order %s and %s", v.Kind, o.Kind)
	}
	if !v.Unit.Convertible(o.Unit) {
		return 0, fmt.Errorf("cannot compare %s with %s: %s and %s are not convertible",
			v, o, v.Unit, o.Unit)
	}
	a := unit.ToBase(v.Num, v.Unit)
	b := unit.ToBase(o.Num, o.Unit)
	return a.Cmp(b), nil
}

// Contains reports set membership with three-valued semantics.
func (v Value) Contains(m Value) Tri {
	if v.IsUnknown() || m.IsUnknown() {
		return Unknown
	}
	if v.Kind != KindSet {
		return Unknown
	}
	for _, e := range v.Set {
		if e.Equal(m) == True {
			return True
		}
	}
	return False
}

// formatRat renders an exact rational: as an integer when whole, as a finite
// decimal when the denominator permits, and as a fraction otherwise.
func formatRat(r *big.Rat) string {
	if r.IsInt() {
		return r.Num().String()
	}
	if s, ok := exactDecimal(r); ok {
		return s
	}
	return r.RatString()
}

// exactDecimal renders r as a terminating decimal when one exists, i.e. when
// the reduced denominator has only the factors 2 and 5.
func exactDecimal(r *big.Rat) (string, bool) {
	den := new(big.Int).Set(r.Denom())
	two, five := big.NewInt(2), big.NewInt(5)
	zero, m := big.NewInt(0), new(big.Int)
	for {
		if m.Mod(den, two).Cmp(zero) == 0 {
			den.Div(den, two)
			continue
		}
		if m.Mod(den, five).Cmp(zero) == 0 {
			den.Div(den, five)
			continue
		}
		break
	}
	if den.Cmp(big.NewInt(1)) != 0 {
		return "", false
	}
	// At most ~20 places for any denominator we accept in rule sources.
	s := strings.TrimRight(r.FloatString(20), "0")
	s = strings.TrimSuffix(s, ".")
	return s, true
}

// FormatINR renders an amount in Indian digit grouping with two decimal places
// when there are paise. Fee notices are read by people who expect 1,20,000.
func FormatINR(r *big.Rat) string {
	neg := r.Sign() < 0
	abs := new(big.Rat).Abs(r)
	whole := new(big.Int).Quo(abs.Num(), abs.Denom())
	frac := new(big.Rat).Sub(abs, new(big.Rat).SetInt(whole))

	s := groupIndian(whole.String())
	if frac.Sign() != 0 {
		fs := frac.FloatString(2)
		s += fs[1:] // keep ".NN"
	}
	if neg {
		return "-INR " + s
	}
	return "INR " + s
}

// groupIndian applies the 2-2-3 grouping used across India.
func groupIndian(s string) string {
	if len(s) <= 3 {
		return s
	}
	head, tail := s[:len(s)-3], s[len(s)-3:]
	var parts []string
	for len(head) > 2 {
		parts = append([]string{head[len(head)-2:]}, parts...)
		head = head[:len(head)-2]
	}
	if head != "" {
		parts = append([]string{head}, parts...)
	}
	return strings.Join(parts, ",") + "," + tail
}
