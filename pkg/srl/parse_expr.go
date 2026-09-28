package srl

import (
	"math/big"

	"github.com/setu-platform/setu/pkg/unit"
	"github.com/setu-platform/setu/pkg/value"
)

// Expression grammar, loosest binding first:
//
//	expr        := or
//	or          := and ( "or" and )*
//	and         := not ( "and" not )*
//	not         := "not" not | comparison
//	comparison  := additive ( ( "==" | "!=" | "<" | "<=" | ">" | ">=" ) additive
//	                        | "in" additive )?
//	additive    := multiplicative ( ( "+" | "-" ) multiplicative )*
//	multiplicative := unary ( ( "*" | "/" ) unary )*
//	unary       := "-" unary | primary
//	primary     := literal | profile_ref | object_ref | call | slab | if | "(" expr ")"
//
// Comparison is deliberately non-associative: `a < b < c` is a mistake in a
// legal condition, not a chained comparison, and the parser says so.

func (p *Parser) parseExpr() Expr { return p.parseOr() }

func (p *Parser) parseOr() Expr {
	l := p.parseAnd()
	for p.at(OR) {
		pos := p.advance().Pos
		r := p.parseAnd()
		l = &BinaryExpr{Op: OR, L: l, R: r, Pos: pos}
	}
	return l
}

func (p *Parser) parseAnd() Expr {
	l := p.parseNot()
	for p.at(AND) {
		pos := p.advance().Pos
		r := p.parseNot()
		l = &BinaryExpr{Op: AND, L: l, R: r, Pos: pos}
	}
	return l
}

func (p *Parser) parseNot() Expr {
	if p.at(NOT) {
		pos := p.advance().Pos
		return &UnaryExpr{Op: NOT, X: p.parseNot(), Pos: pos}
	}
	return p.parseComparison()
}

func (p *Parser) parseComparison() Expr {
	l := p.parseAdditive()
	switch p.cur().Kind {
	case EQ, NE, LT, LE, GT, GE:
		t := p.advance()
		r := p.parseAdditive()
		out := Expr(&BinaryExpr{Op: t.Kind, L: l, R: r, Pos: t.Pos})
		if k := p.cur().Kind; k == EQ || k == NE || k == LT || k == LE || k == GT || k == GE {
			p.errorf(p.cur().Pos, "SRL0033",
				"write it as two comparisons joined by `and`",
				"comparisons cannot be chained")
			p.advance()
			p.parseAdditive()
		}
		return out
	case IN:
		t := p.advance()
		return &InExpr{X: l, Set: p.parseAdditive(), Pos: t.Pos}
	}
	return l
}

func (p *Parser) parseAdditive() Expr {
	l := p.parseMultiplicative()
	for p.at(PLUS) || p.at(MINUS) {
		t := p.advance()
		r := p.parseMultiplicative()
		l = &BinaryExpr{Op: t.Kind, L: l, R: r, Pos: t.Pos}
	}
	return l
}

func (p *Parser) parseMultiplicative() Expr {
	l := p.parseUnary()
	for p.at(STAR) || p.at(SLASH) {
		t := p.advance()
		r := p.parseUnary()
		l = &BinaryExpr{Op: t.Kind, L: l, R: r, Pos: t.Pos}
	}
	return l
}

func (p *Parser) parseUnary() Expr {
	if p.at(MINUS) {
		t := p.advance()
		return &UnaryExpr{Op: MINUS, X: p.parseUnary(), Pos: t.Pos}
	}
	return p.parsePrimary()
}

// moneyMultipliers lets a fee schedule be written the way the notification
// writes it: "INR 30 lakh", not "INR 3000000".
var moneyMultipliers = map[string]int64{
	"thousand": 1_000,
	"lakh":     100_000,
	"lakhs":    100_000,
	"crore":    10_000_000,
	"crores":   10_000_000,
}

func (p *Parser) parsePrimary() Expr {
	t := p.cur()
	switch t.Kind {
	case NUMBER:
		return p.parseNumeric()
	case STRING:
		p.advance()
		return &StringLit{Val: t.Text, Pos: t.Pos}
	case DATE:
		return &DateLit{Val: p.parseDate(), Pos: t.Pos}
	case TRUE:
		p.advance()
		return &BoolLit{Val: true, Pos: t.Pos}
	case FALSE:
		p.advance()
		return &BoolLit{Val: false, Pos: t.Pos}
	case UNKNOWN:
		p.advance()
		return &UnknownLit{Pos: t.Pos}
	case LPAREN:
		p.advance()
		e := p.parseExpr()
		p.expect(RPAREN)
		return e
	case LBRACK:
		return p.parseSetLit()
	case IF:
		p.advance()
		e := &IfExpr{Pos: t.Pos, Cond: p.parseExpr()}
		p.expectWord("then")
		e.Then = p.parseExpr()
		p.expect(ELSE)
		e.Else = p.parseExpr()
		return e
	case IDENT:
		return p.parseIdentExpr()
	}
	p.errorf(t.Pos, "SRL0034", "", "expected an expression, found %s", t)
	if t.Kind != EOF && t.Kind != RBRACE {
		p.advance()
	}
	return &UnknownLit{Pos: t.Pos}
}

// parseNumeric reads a bare number, a dimensioned quantity or a period.
func (p *Parser) parseNumeric() Expr {
	t := p.advance()
	r := p.ratFromToken(t)

	if p.at(IDENT) {
		w := p.cur().Text
		if _, isPeriod := periodWords[w]; isPeriod {
			p.i-- // let parsePeriod re-read the number
			return &PeriodLit{Val: p.parsePeriod(), Pos: t.Pos}
		}
		if _, ok := unit.Lookup(w); ok {
			p.advance()
			// Keep the spelling the author wrote, not the canonical symbol.
			// The derivation trace shown to an applicant quotes the condition
			// back at them, and "10 unit" where the rule says "10 workers" is a
			// worse explanation of the same fact.
			return &QuantityLit{Val: r, Unit: w, Pos: t.Pos}
		}
	}
	return &NumberLit{Val: r, Pos: t.Pos}
}

func (p *Parser) parseSetLit() Expr {
	pos := p.expect(LBRACK).Pos
	s := &SetLit{Pos: pos}
	for !p.at(RBRACK) && !p.at(EOF) {
		if p.accept(COMMA) {
			continue
		}
		s.Elems = append(s.Elems, p.parseExpr())
	}
	p.expect(RBRACK)
	return s
}

// builtins are the functions available in a rule condition or fee formula.
//
// The set is closed on purpose. A rule author cannot call out to arbitrary
// code, because evaluation must be total, deterministic and free of network
// access (spec 5.5.4). Every builtin here is a pure function of its arguments
// and the pinned boundary layers.
var builtins = map[string]int{ // name -> arity, -1 for variadic
	"min":           -1,
	"max":           -1,
	"abs":           1,
	"round":         1,
	"floor":         1,
	"ceil":          1,
	"defined":       1,
	"within":        2, // within(profile.premise, "eco_sensitive_zone")
	"distance_to":   2, // distance_to(profile.premise, "coastline")
	"zone_of":       2, // zone_of(profile.premise, "land_use")
	"starts_with":   2,
	"contains_text": 2,
	"required":      1, // required(TN.FACT.LICENCE) — reads another rule's outcome
	"exempted":      1,
	"years_between": 2,
	"days_between":  2,
	"point":         2, // point(latitude, longitude) — a site location
}

// BuiltinArity reports a builtin's arity, or false when the name is unknown.
func BuiltinArity(name string) (int, bool) {
	n, ok := builtins[name]
	return n, ok
}

// BuiltinNames lists every builtin, sorted, for editor autocomplete.
func BuiltinNames() []string {
	out := make([]string, 0, len(builtins))
	for k := range builtins {
		out = append(out, k)
	}
	sortStrings(out)
	return out
}

func (p *Parser) parseIdentExpr() Expr {
	t := p.cur()

	// A money literal: `INR 4 crore`.
	if t.Text == "INR" && p.toks[p.i+1].Kind == NUMBER {
		p.advance()
		numTok := p.advance()
		r := p.ratFromToken(numTok)
		if p.at(IDENT) {
			if m, ok := moneyMultipliers[p.cur().Text]; ok {
				p.advance()
				r = new(big.Rat).Mul(r, new(big.Rat).SetInt64(m))
			}
		}
		return &MoneyLit{Val: r, Pos: t.Pos}
	}

	// A profile variable: `profile.workers`.
	if t.Text == "profile" && p.toks[p.i+1].Kind == DOT {
		p.advance()
		p.advance()
		name := p.expectIdent()
		return &ProfileRef{Var: name.Text, Pos: t.Pos}
	}

	// A slab table: `slab profile.workers { <= 20 -> INR 1000 ; else -> INR 5000 }`.
	if t.Text == "slab" {
		return p.parseSlab()
	}

	// A builtin call.
	if p.toks[p.i+1].Kind == LPAREN {
		if _, ok := builtins[t.Text]; !ok {
			hint := "available functions: " + joinComma(BuiltinNames())
			p.errorf(t.Pos, "SRL0035", hint, "unknown function %q", t.Text)
		}
		p.advance()
		p.advance()
		c := &CallExpr{Fn: t.Text, Pos: t.Pos}
		for !p.at(RPAREN) && !p.at(EOF) {
			if p.accept(COMMA) {
				continue
			}
			c.Args = append(c.Args, p.parseExpr())
		}
		p.expect(RPAREN)
		return c
	}

	// Otherwise a reference to a declared object or an enum member.
	pos := t.Pos
	return &ObjectRef{Path: p.qualified(), Pos: pos}
}

// parseArmValue parses the amount on the right of a slab arrow. It binds
// tighter than comparison on purpose: an arm value is never a comparison, and
// parsing one greedily would let `-> INR 1000` swallow the next arm's bound.
func (p *Parser) parseArmValue() Expr { return p.parseAdditive() }

func (p *Parser) parseSlab() Expr {
	pos := p.expectWord("slab").Pos
	s := &SlabExpr{Pos: pos, Subject: p.parseAdditive()}
	p.expect(LBRACE)
	for !p.at(RBRACE) && !p.at(EOF) {
		before := p.i
		if p.accept(SEMI) || p.accept(COMMA) {
			continue
		}
		if p.at(ELSE) {
			p.advance()
			p.expect(ARROW)
			if s.Default != nil {
				p.errorf(p.cur().Pos, "SRL0036", "", "slab has more than one `else` arm")
			}
			s.Default = p.parseArmValue()
			continue
		}
		arm := SlabArm{Pos: p.cur().Pos}
		switch p.cur().Kind {
		case LE, LT, GE, GT, EQ:
			arm.Op = p.advance().Kind
		default:
			p.errorf(p.cur().Pos, "SRL0036",
				"each arm starts with a comparison, for example `<= 20 -> INR 1000`",
				"expected a slab bound, found %s", p.cur())
			p.skipField()
			if p.i == before {
				p.advance()
			}
			continue
		}
		arm.Bound = p.parseAdditive()
		p.expect(ARROW)
		arm.Val = p.parseArmValue()
		s.Arms = append(s.Arms, arm)
		if p.i == before {
			p.advance()
		}
	}
	p.expect(RBRACE)
	if s.Default == nil {
		p.errorf(s.Pos, "SRL0037",
			"add `else -> <amount>` so the schedule is total",
			"slab has no `else` arm and would be undefined outside its bounds")
	}
	return s
}

func joinComma(ss []string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}

// PeriodValue exposes a period literal's value to other packages.
func (n *PeriodLit) PeriodValue() value.Period { return n.Val }
