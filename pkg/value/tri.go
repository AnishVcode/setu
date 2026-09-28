// Package value defines the runtime values of the SETU Rule Language and the
// three-valued logic the evaluator uses.
//
// The third truth value is the point of the package. When an applicant has not
// yet told us how much water the unit will draw, the condition
// `water > 100 KLD` is neither true nor false — it is UNKNOWN. A conventional
// engine coerces that to false and quietly omits an approval the applicant
// legally needs. SETU instead propagates UNKNOWN to the determination and
// reports it as an unresolved determination with the exact question to ask
// (principle: "complete or honest", spec 5.5.4).
package value

// Tri is a Kleene three-valued truth value.
type Tri uint8

const (
	False Tri = iota
	True
	Unknown
)

func (t Tri) String() string {
	switch t {
	case False:
		return "false"
	case True:
		return "true"
	default:
		return "unknown"
	}
}

// TriOf lifts a Go bool.
func TriOf(b bool) Tri {
	if b {
		return True
	}
	return False
}

// And is Kleene conjunction. UNKNOWN and FALSE is FALSE, because the result is
// determined regardless of what the missing answer turns out to be.
func (t Tri) And(o Tri) Tri {
	if t == False || o == False {
		return False
	}
	if t == Unknown || o == Unknown {
		return Unknown
	}
	return True
}

// Or is Kleene disjunction. UNKNOWN or TRUE is TRUE.
func (t Tri) Or(o Tri) Tri {
	if t == True || o == True {
		return True
	}
	if t == Unknown || o == Unknown {
		return Unknown
	}
	return False
}

// Not is Kleene negation; the negation of UNKNOWN is still UNKNOWN.
func (t Tri) Not() Tri {
	switch t {
	case True:
		return False
	case False:
		return True
	default:
		return Unknown
	}
}

// Definite reports whether the value is settled either way.
func (t Tri) Definite() bool { return t != Unknown }
