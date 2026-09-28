package eval

import (
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strings"

	"github.com/setu-platform/setu/pkg/compile"
	"github.com/setu-platform/setu/pkg/sema"
	"github.com/setu-platform/setu/pkg/unit"
	"github.com/setu-platform/setu/pkg/value"
)

// ParseProfileJSON reads a profile from JSON, coercing each value against the
// declared schema.
//
// The format is deliberately the shape a person would write by hand:
//
//	{
//	  "workers_total": 25,
//	  "uses_power": true,
//	  "water_draw": "60 KLD",
//	  "investment_plant_machinery": 40000000,
//	  "pollution_category": "ORANGE",
//	  "premise": [13.08, 80.27],
//	  "commencement_date": "2026-09-01"
//	}
//
// A bare number for a quantity is read in the unit the profile declares, so an
// answer can never silently arrive in the wrong unit; a string may name a
// different unit explicitly ("60 KLD", "0.06 MLD") and is converted exactly.
// An unrecognised key is an error rather than an ignored field: silently
// dropping an answer would produce a determination that looks complete and is
// not.
func ParseProfileJSON(prog *compile.Program, data []byte) (*Profile, error) {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}

	p := NewProfile("")
	if id, ok := raw["_id"].(string); ok {
		p.ID = id
		delete(raw, "_id")
	}
	if v, ok := raw["_version"].(float64); ok {
		p.Version = int(v)
		delete(raw, "_version")
	}

	var keys []string
	for k := range raw {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		t, ok := prog.VarTypes[k]
		if !ok {
			return nil, fmt.Errorf("%q is not a profile variable in this jurisdiction; "+
				"the declared variables are: %s", k, strings.Join(prog.Graph.VarNamesSorted(), ", "))
		}
		if raw[k] == nil {
			continue // an explicit null means "not answered", which is the default
		}
		v, err := coerce(prog, k, t, raw[k])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", k, err)
		}
		p.Set(k, v)
	}
	return p, nil
}

func coerce(prog *compile.Program, name string, t sema.Type, raw any) (value.Value, error) {
	switch t.Kind {
	case sema.Bool:
		b, ok := raw.(bool)
		if !ok {
			return value.Null, fmt.Errorf("expected true or false, got %T", raw)
		}
		return value.Bool(b), nil

	case sema.Text:
		s, ok := raw.(string)
		if !ok {
			return value.Null, fmt.Errorf("expected a string, got %T", raw)
		}
		return value.Text(s), nil

	case sema.Date:
		s, ok := raw.(string)
		if !ok {
			return value.Null, fmt.Errorf("expected a date as \"YYYY-MM-DD\", got %T", raw)
		}
		d, err := value.ParseDate(s)
		if err != nil {
			return value.Null, err
		}
		return value.OfDate(d), nil

	case sema.EnumT:
		s, ok := raw.(string)
		if !ok {
			return value.Null, fmt.Errorf("expected an enum member name, got %T", raw)
		}
		if _, ok := prog.Graph.EnumMember(t.Name, s); !ok {
			var members []string
			if e, ok := prog.Graph.Enums[t.Name]; ok {
				for _, m := range e.Members {
					members = append(members, m.Name)
				}
			}
			return value.Null, fmt.Errorf("%q is not a member of %s; valid values are: %s",
				s, t.Name, strings.Join(members, ", "))
		}
		return value.Enum(t.Name, s), nil

	case sema.Geo:
		pair, ok := raw.([]any)
		if !ok || len(pair) != 2 {
			return value.Null, fmt.Errorf("expected a location as [latitude, longitude]")
		}
		lat, ok1 := pair[0].(float64)
		lon, ok2 := pair[1].(float64)
		if !ok1 || !ok2 {
			return value.Null, fmt.Errorf("latitude and longitude must be numbers")
		}
		return value.OfGeo(value.Point(lat, lon)), nil

	case sema.Number:
		return coerceNumber(prog, name, t, raw)

	case sema.SetT:
		items, ok := raw.([]any)
		if !ok {
			return value.Null, fmt.Errorf("expected an array")
		}
		var members []value.Value
		for _, it := range items {
			v, err := coerce(prog, name, *t.Elem, it)
			if err != nil {
				return value.Null, err
			}
			members = append(members, v)
		}
		return value.SetOf(members), nil
	}
	return value.Null, fmt.Errorf("values of type %s cannot be given in a profile", t)
}

func coerceNumber(prog *compile.Program, name string, t sema.Type, raw any) (value.Value, error) {
	// The unit a bare number is read in: the one the profile declares, or the
	// dimension's base unit if it declares none.
	target := unit.Base(t.Dim)
	if v, ok := prog.Graph.Vars[name]; ok && v.Unit != "" {
		if u, ok := unit.Lookup(v.Unit); ok && u.Dim == t.Dim {
			target = u
		}
	}

	switch x := raw.(type) {
	case float64:
		// JSON numbers arrive as float64. Converting through the decimal text
		// rather than the binary value keeps 0.1 exactly 1/10, which is what a
		// fee schedule means by it.
		r, ok := new(big.Rat).SetString(trimFloat(x))
		if !ok {
			return value.Null, fmt.Errorf("cannot read %v as a number", x)
		}
		if t.Dim == unit.DimMoney {
			return value.Money(r), nil
		}
		return value.Quantity(r, target), nil

	case string:
		return parseQuantityString(x, t, target)
	}
	return value.Null, fmt.Errorf("expected a number or a quantity like \"60 KLD\", got %T", raw)
}

// parseQuantityString reads "60 KLD", "INR 4 crore" or "1200".
func parseQuantityString(s string, t sema.Type, target unit.Unit) (value.Value, error) {
	s = strings.TrimSpace(s)
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return value.Null, fmt.Errorf("empty value")
	}

	if t.Dim == unit.DimMoney {
		amount := fields
		if strings.EqualFold(fields[0], "INR") || strings.EqualFold(fields[0], "Rs") {
			amount = fields[1:]
		}
		if len(amount) == 0 {
			return value.Null, fmt.Errorf("no amount given")
		}
		r, ok := new(big.Rat).SetString(strings.ReplaceAll(amount[0], ",", ""))
		if !ok {
			return value.Null, fmt.Errorf("cannot read %q as an amount", amount[0])
		}
		if len(amount) > 1 {
			mult, ok := moneyMultiplier(amount[1])
			if !ok {
				return value.Null, fmt.Errorf("unknown multiplier %q; use lakh or crore", amount[1])
			}
			r = new(big.Rat).Mul(r, new(big.Rat).SetInt64(mult))
		}
		return value.Money(r), nil
	}

	r, ok := new(big.Rat).SetString(strings.ReplaceAll(fields[0], ",", ""))
	if !ok {
		return value.Null, fmt.Errorf("cannot read %q as a number", fields[0])
	}
	u := target
	if len(fields) > 1 {
		found, ok := unit.Lookup(fields[1])
		if !ok {
			hint := ""
			if s, ok := unit.SuggestSymbol(fields[1]); ok {
				hint = "; did you mean " + s + "?"
			}
			return value.Null, fmt.Errorf("unknown unit %q%s", fields[1], hint)
		}
		if found.Dim != t.Dim {
			return value.Null, fmt.Errorf("%s measures %s, but this answer must be a %s",
				found.Symbol, found.Dim, t.Dim)
		}
		u = found
	}
	return value.Quantity(r, u), nil
}

func moneyMultiplier(s string) (int64, bool) {
	switch strings.ToLower(s) {
	case "thousand":
		return 1_000, true
	case "lakh", "lakhs":
		return 100_000, true
	case "crore", "crores":
		return 10_000_000, true
	}
	return 0, false
}

// trimFloat renders a float64 in the shortest decimal form that round-trips,
// which is the text the author most likely wrote.
func trimFloat(f float64) string {
	b, _ := json.Marshal(f)
	return string(b)
}

// MarshalProfileJSON writes a profile in the same hand-writable form, so a
// what-if scenario or a generated counterexample can be saved and replayed.
func MarshalProfileJSON(prog *compile.Program, p *Profile) ([]byte, error) {
	out := map[string]any{}
	if p.ID != "" {
		out["_id"] = p.ID
	}
	for _, name := range p.Names() {
		v := p.Get(name)
		if v.IsUnknown() {
			continue
		}
		switch v.Kind {
		case value.KindBool:
			out[name] = v.Bool
		case value.KindText:
			out[name] = v.Text
		case value.KindEnum:
			out[name] = v.Enum
		case value.KindDate:
			out[name] = v.Date.String()
		case value.KindGeo:
			if v.Geo != nil {
				out[name] = []float64{v.Geo.Lat, v.Geo.Lon}
			}
		case value.KindMoney:
			out[name] = v.Num.RatString()
		case value.KindNumber:
			if v.Unit.IsNone() {
				out[name] = v.Num.RatString()
			} else {
				out[name] = v.Num.RatString() + " " + v.Unit.Label()
			}
		default:
			out[name] = v.String()
		}
	}
	return json.MarshalIndent(out, "", "  ")
}
