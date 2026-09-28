// Package sema performs semantic analysis of a knowledge graph: name
// resolution, type and dimension checking, and the regulatory-content lints
// that decide whether a ruleset may be published.
//
// The type system exists to catch one specific class of defect. A rule that
// compares an investment in rupees to a water draw in kilolitres, or a
// tonnes-per-day threshold to a tonnes-per-annum declaration, produces a
// confident, wrong, legally consequential answer. Every such comparison is
// rejected here, before the rule can reach an applicant.
package sema

import (
	"github.com/setu-platform/setu/pkg/unit"
)

// Kind is a static type constructor.
type Kind uint8

const (
	Invalid Kind = iota
	Bool
	Number // dimensioned; Money is Number with unit.DimMoney
	Text
	Date
	Period
	EnumT
	SetT
	Geo
	ApprovalRef
	DocumentRef
	FeeRef
	ObligationRef
	IncentiveRef
	Untyped // the `unknown` literal, compatible with everything
)

// Type is a static type.
type Type struct {
	Kind Kind
	Dim  unit.Dimension // meaningful when Kind is Number
	Name string         // enum type name when Kind is EnumT
	Elem *Type          // element type when Kind is SetT
}

// Common types.
var (
	TBool    = Type{Kind: Bool}
	TText    = Type{Kind: Text}
	TDate    = Type{Kind: Date}
	TPeriod  = Type{Kind: Period}
	TGeo     = Type{Kind: Geo}
	TInvalid = Type{Kind: Invalid}
	TUntyped = Type{Kind: Untyped}
	TScalar  = Type{Kind: Number, Dim: unit.DimScalar}
	TMoney   = Type{Kind: Number, Dim: unit.DimMoney}
	TLength  = Type{Kind: Number, Dim: unit.DimLength}
)

// Num constructs a dimensioned numeric type.
func Num(d unit.Dimension) Type { return Type{Kind: Number, Dim: d} }

// Set constructs a set type.
func Set(elem Type) Type { return Type{Kind: SetT, Elem: &elem} }

// Enum constructs an enum type.
func Enum(name string) Type { return Type{Kind: EnumT, Name: name} }

func (t Type) String() string {
	switch t.Kind {
	case Invalid:
		return "<invalid>"
	case Untyped:
		return "unknown"
	case Bool:
		return "Bool"
	case Number:
		if t.Dim == unit.DimMoney {
			return "Money"
		}
		if t.Dim == unit.DimScalar {
			return "Number"
		}
		return "Quantity<" + t.Dim.String() + ">"
	case Text:
		return "Text"
	case Date:
		return "Date"
	case Period:
		return "Period"
	case EnumT:
		return "Enum<" + t.Name + ">"
	case SetT:
		if t.Elem == nil {
			return "Set<?>"
		}
		return "Set<" + t.Elem.String() + ">"
	case Geo:
		return "Geo"
	case ApprovalRef:
		return "Approval"
	case DocumentRef:
		return "Document"
	case FeeRef:
		return "Fee"
	case ObligationRef:
		return "Obligation"
	case IncentiveRef:
		return "Incentive"
	}
	return "?"
}

// IsNumeric reports whether the type participates in arithmetic.
func (t Type) IsNumeric() bool { return t.Kind == Number }

// Equal reports exact type identity.
func (t Type) Equal(o Type) bool {
	if t.Kind != o.Kind {
		return false
	}
	switch t.Kind {
	case Number:
		return t.Dim == o.Dim
	case EnumT:
		return t.Name == o.Name
	case SetT:
		if t.Elem == nil || o.Elem == nil {
			return t.Elem == o.Elem
		}
		return t.Elem.Equal(*o.Elem)
	}
	return true
}

// Compatible reports whether two types may meet in a comparison or a branch.
// Untyped (the `unknown` literal) is compatible with everything, so that a test
// case may assert that a rule is undetermined without naming a type.
func (t Type) Compatible(o Type) bool {
	if t.Kind == Untyped || o.Kind == Untyped {
		return true
	}
	if t.Kind == Invalid || o.Kind == Invalid {
		return true // an error was already reported; do not cascade
	}
	return t.Equal(o)
}
