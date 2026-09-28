// Package unit provides dimensional analysis for the SETU Rule Language.
//
// Regulatory thresholds are dimensioned quantities: "60 KLD of water", "500 m
// from the coastline", "an investment of INR 4 crore". Comparing a volume flow
// to a power rating, or silently converting tonnes-per-day into
// tonnes-per-annum, is the kind of error that produces a legally wrong answer
// while looking entirely reasonable. This package makes such comparisons a
// compile-time type error.
//
// Two rules govern the design:
//
//  1. All conversion factors are exact rationals (math/big.Rat), never floats.
//     A ruleset must produce byte-identical output on every evaluation
//     (principle P1); binary floating point cannot promise that across
//     architectures and optimisation levels.
//
//  2. Some units are *nominal*: decibels, pH. They carry a scale that is not
//     linear in a base unit, so conversion is forbidden outright rather than
//     approximated. Comparing 60 dB to 60 dB is fine; converting dB to
//     anything is not.
package unit

import (
	"fmt"
	"math/big"
	"sort"
	"strings"
)

// Dimension is the physical (or economic) quantity kind a unit measures.
// Two quantities may only be compared or added when their dimensions match.
type Dimension uint8

const (
	DimScalar Dimension = iota // dimensionless number
	DimMoney
	DimCount // persons, machines, seats — countable things
	DimLength
	DimArea
	DimVolume
	DimVolumeFlow // per day
	DimPower
	DimMass
	DimMassFlowDay  // per day
	DimMassFlowYear // per annum — deliberately NOT convertible to per-day
	DimEnergy
	DimRatio   // percentages and fractions
	DimNominal // dB, pH: comparable within the same unit only
	DimTemp    // degrees Celsius
)

var dimNames = map[Dimension]string{
	DimScalar:       "Scalar",
	DimMoney:        "Money",
	DimCount:        "Count",
	DimLength:       "Length",
	DimArea:         "Area",
	DimVolume:       "Volume",
	DimVolumeFlow:   "VolumeFlow",
	DimPower:        "Power",
	DimMass:         "Mass",
	DimMassFlowDay:  "MassFlowDay",
	DimMassFlowYear: "MassFlowYear",
	DimEnergy:       "Energy",
	DimRatio:        "Ratio",
	DimNominal:      "Nominal",
	DimTemp:         "Temperature",
}

func (d Dimension) String() string {
	if n, ok := dimNames[d]; ok {
		return n
	}
	return fmt.Sprintf("Dimension(%d)", uint8(d))
}

// DimensionByName resolves a dimension name as written in a profile
// declaration, e.g. `var water: Quantity<VolumeFlow>`.
func DimensionByName(name string) (Dimension, bool) {
	for d, n := range dimNames {
		if n == name {
			return d, true
		}
	}
	return DimScalar, false
}

// DimensionNames lists every dimension name, sorted, for error messages and
// editor autocomplete in Rule Studio.
func DimensionNames() []string {
	out := make([]string, 0, len(dimNames))
	for _, n := range dimNames {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Unit is a named scale within a Dimension. Factor converts a value expressed
// in this unit into the dimension's canonical base unit, exactly.
//
// Symbol is the canonical spelling and is what every equality, ordering and
// hashing decision uses. Display is the spelling the rule author actually
// wrote, and is used only when rendering text for a person. The split matters:
// "25 workers" and "25 persons" are the same quantity and must hash the same,
// but an explanation shown to an applicant should use the word the rule used,
// not the base unit "unit".
type Unit struct {
	Symbol  string
	Display string
	Dim     Dimension
	Factor  *big.Rat // value_in_base = value_in_this_unit * Factor
	Base    bool     // true when Factor == 1 and this is the canonical unit
}

// None is the unit of a dimensionless number.
var None = Unit{Symbol: "", Dim: DimScalar, Factor: big.NewRat(1, 1), Base: true}

// IsNone reports whether u is the dimensionless unit.
func (u Unit) IsNone() bool { return u.Dim == DimScalar && u.Symbol == "" }

func (u Unit) String() string {
	if u.IsNone() {
		return "scalar"
	}
	if u.Display != "" {
		return u.Display
	}
	return u.Symbol
}

// Label is the spelling to show a person.
func (u Unit) Label() string {
	if u.Display != "" {
		return u.Display
	}
	return u.Symbol
}

// WithDisplay returns the unit rendered under an alternative spelling.
func (u Unit) WithDisplay(spelling string) Unit {
	u.Display = spelling
	return u
}

// Convertible reports whether a value in u can be expressed in v.
// Nominal units convert only to themselves.
func (u Unit) Convertible(v Unit) bool {
	if u.Dim != v.Dim {
		return false
	}
	if u.Dim == DimNominal || u.Dim == DimTemp {
		return u.Symbol == v.Symbol
	}
	return true
}

// registry holds every unit symbol known to the language. Symbols are unique
// across dimensions: "t" cannot mean both tonne and time.
var registry = map[string]Unit{}

func reg(symbol string, dim Dimension, num, den int64, aliases ...string) Unit {
	u := Unit{Symbol: symbol, Dim: dim, Factor: big.NewRat(num, den)}
	u.Base = u.Factor.Cmp(big.NewRat(1, 1)) == 0
	if _, dup := registry[symbol]; dup {
		panic("unit: duplicate symbol " + symbol)
	}
	registry[symbol] = u
	for _, a := range aliases {
		if _, dup := registry[a]; dup {
			panic("unit: duplicate alias " + a)
		}
		// An alias resolves to the canonical symbol so that formatting is stable.
		registry[a] = u
	}
	return u
}

// Canonical base units, one per dimension:
//
//	Money         INR
//	Count         unit
//	Length        m
//	Area          m2
//	Volume        m3
//	VolumeFlow    KLD  (kilolitre per day == m3 per day)
//	Power         kW
//	Mass          kg
//	MassFlowDay   kg_per_day
//	MassFlowYear  kg_per_year
//	Energy        kWh
//	Ratio         ratio
var (
	INR = reg("INR", DimMoney, 1, 1, "rupee", "rupees")

	Count = reg("unit", DimCount, 1, 1, "units", "person", "persons", "worker",
		"workers", "employee", "employees", "seat", "seats", "machine", "machines",
		"vehicle", "vehicles", "bed", "beds")

	Millimetre = reg("mm", DimLength, 1, 1000)
	Centimetre = reg("cm", DimLength, 1, 100)
	Metre      = reg("m", DimLength, 1, 1, "metre", "metres")
	Kilometre  = reg("km", DimLength, 1000, 1)
	Foot       = reg("ft", DimLength, 3048, 10000, "feet")

	SquareMetre = reg("m2", DimArea, 1, 1, "sqm")
	SquareFoot  = reg("ft2", DimArea, 9290304, 100000000, "sqft")
	Cent        = reg("cent", DimArea, 404685642240, 10000000000, "cents")
	Acre        = reg("acre", DimArea, 40468564224, 10000000, "acres")
	Hectare     = reg("hectare", DimArea, 10000, 1, "hectares", "ha")

	Litre     = reg("l", DimVolume, 1, 1000, "litre", "litres")
	CubicM    = reg("m3", DimVolume, 1, 1, "cum")
	Kilolitre = reg("kl", DimVolume, 1, 1, "kilolitre", "kilolitres")

	KLD = reg("KLD", DimVolumeFlow, 1, 1, "kld", "m3_per_day")
	MLD = reg("MLD", DimVolumeFlow, 1000, 1, "mld")
	LPD = reg("LPD", DimVolumeFlow, 1, 1000, "lpd")

	Watt       = reg("W", DimPower, 1, 1000)
	Kilowatt   = reg("kW", DimPower, 1, 1, "KW", "kw")
	Megawatt   = reg("MW", DimPower, 1000, 1, "mw")
	Horsepower = reg("HP", DimPower, 7457, 10000, "hp")

	Gram     = reg("g", DimMass, 1, 1000)
	Kilogram = reg("kg", DimMass, 1, 1, "kilogram", "kilograms")
	Tonne    = reg("tonne", DimMass, 1000, 1, "tonnes", "MT")

	KgPerDay    = reg("kg_per_day", DimMassFlowDay, 1, 1)
	TonnePerDay = reg("TPD", DimMassFlowDay, 1000, 1, "tpd")

	KgPerYear    = reg("kg_per_year", DimMassFlowYear, 1, 1)
	TonnePerYear = reg("TPA", DimMassFlowYear, 1000, 1, "tpa")

	KilowattHour = reg("kWh", DimEnergy, 1, 1, "kwh")
	MegawattHour = reg("MWh", DimEnergy, 1000, 1, "mwh")

	Ratio   = reg("ratio", DimRatio, 1, 1)
	Percent = reg("percent", DimRatio, 1, 100, "pct")

	Decibel = reg("dB", DimNominal, 1, 1, "db", "dBA")
	PH      = reg("pH", DimNominal, 1, 1, "ph")

	Celsius = reg("degC", DimTemp, 1, 1, "celsius")
)

// Lookup resolves a unit symbol or alias, remembering the spelling used so
// that explanations echo the author's words.
func Lookup(symbol string) (Unit, bool) {
	u, ok := registry[symbol]
	if ok {
		u.Display = symbol
	}
	return u, ok
}

// Symbols lists every registered symbol and alias, sorted, for the linter's
// "did you mean" suggestions.
func Symbols() []string {
	out := make([]string, 0, len(registry))
	for s := range registry {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// Base returns the canonical unit for a dimension.
func Base(d Dimension) Unit {
	for _, u := range registry {
		if u.Dim == d && u.Base {
			return u
		}
	}
	return None
}

// Convert expresses v (given in from) in the unit to, exactly.
// It returns an error rather than an approximation when the units are not
// convertible, so a rule author is told about the mistake instead of
// inheriting a silently wrong number.
func Convert(v *big.Rat, from, to Unit) (*big.Rat, error) {
	if from.Dim != to.Dim {
		return nil, fmt.Errorf("cannot convert %s (%s) to %s (%s): different dimensions",
			from, from.Dim, to, to.Dim)
	}
	if (from.Dim == DimNominal || from.Dim == DimTemp) && from.Symbol != to.Symbol {
		return nil, fmt.Errorf("cannot convert %s to %s: %s is a nominal scale and does not convert",
			from, to, from.Dim)
	}
	if from.Symbol == to.Symbol {
		return new(big.Rat).Set(v), nil
	}
	base := new(big.Rat).Mul(v, from.Factor)
	return new(big.Rat).Quo(base, to.Factor), nil
}

// ToBase converts v from its unit into the dimension's canonical base unit.
// Nominal scales are returned unchanged; they are only ever compared to
// themselves, which Convertible already enforces.
func ToBase(v *big.Rat, from Unit) *big.Rat {
	if from.Dim == DimNominal || from.Dim == DimTemp {
		return new(big.Rat).Set(v)
	}
	return new(big.Rat).Mul(v, from.Factor)
}

// SuggestSymbol finds the closest registered symbol to a misspelling, so the
// linter can say "unknown unit 'kld/day'; did you mean 'KLD'?".
func SuggestSymbol(s string) (string, bool) {
	best, bestDist := "", 1<<30
	for _, cand := range Symbols() {
		d := editDistance(strings.ToLower(s), strings.ToLower(cand))
		if d < bestDist {
			best, bestDist = cand, d
		}
	}
	if bestDist <= 2 && best != "" {
		return best, true
	}
	return "", false
}

func editDistance(a, b string) int {
	ar, br := []rune(a), []rune(b)
	prev := make([]int, len(br)+1)
	cur := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		cur[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			cur[j] = min3(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		copy(prev, cur)
	}
	return prev[len(br)]
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}
