package srl

import (
	"strings"

	"github.com/setu-platform/setu/pkg/value"
)

// FormatExpr renders an expression back to canonical SRL source.
//
// It is used in two places that both matter: `setuctl fmt`, and the derivation
// trace shown to an applicant who expands "why is this required?" (P5). The
// second use is why the output is minimally parenthesised — a citizen reading
// the condition that produced their obligation should see the rule as its
// author wrote it, not as the parser stored it.
func FormatExpr(e Expr) string {
	var b strings.Builder
	writeExpr(&b, e, precLowest)
	return b.String()
}

// Precedence levels, loosest first, mirroring the parser.
const (
	precLowest = iota
	precOr
	precAnd
	precNot
	precCompare
	precAdd
	precMul
	precUnary
	precAtom
)

func exprPrec(e Expr) int {
	switch x := e.(type) {
	case *BinaryExpr:
		switch x.Op {
		case OR:
			return precOr
		case AND:
			return precAnd
		case EQ, NE, LT, LE, GT, GE:
			return precCompare
		case PLUS, MINUS:
			return precAdd
		case STAR, SLASH:
			return precMul
		}
	case *InExpr:
		return precCompare
	case *UnaryExpr:
		if x.Op == NOT {
			return precNot
		}
		return precUnary
	case *IfExpr:
		return precLowest
	}
	return precAtom
}

func writeExpr(b *strings.Builder, e Expr, min int) {
	if e == nil {
		b.WriteString("<missing>")
		return
	}
	p := exprPrec(e)
	paren := p < min
	if paren {
		b.WriteByte('(')
	}
	writeBare(b, e)
	if paren {
		b.WriteByte(')')
	}
}

func writeBare(b *strings.Builder, e Expr) {
	switch x := e.(type) {
	case *NumberLit:
		b.WriteString(value.Rat(x.Val).String())
	case *QuantityLit:
		b.WriteString(value.Rat(x.Val).String())
		b.WriteByte(' ')
		b.WriteString(x.Unit)
	case *MoneyLit:
		b.WriteString(value.Money(x.Val).String())
	case *PeriodLit:
		b.WriteString(x.Val.String())
	case *StringLit:
		b.WriteString(quote(x.Val))
	case *BoolLit:
		if x.Val {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case *UnknownLit:
		b.WriteString("unknown")
	case *DateLit:
		b.WriteString(x.Val.String())
	case *SetLit:
		b.WriteByte('[')
		for i, el := range x.Elems {
			if i > 0 {
				b.WriteString(", ")
			}
			writeExpr(b, el, precLowest)
		}
		b.WriteByte(']')
	case *ProfileRef:
		b.WriteString("profile.")
		b.WriteString(x.Var)
	case *ObjectRef:
		b.WriteString(x.Name())
	case *UnaryExpr:
		if x.Op == NOT {
			b.WriteString("not ")
			writeExpr(b, x.X, precNot)
			return
		}
		b.WriteByte('-')
		writeExpr(b, x.X, precUnary)
	case *BinaryExpr:
		p := exprPrec(x)
		writeExpr(b, x.L, p)
		b.WriteByte(' ')
		b.WriteString(opText(x.Op))
		b.WriteByte(' ')
		// Right-associate nothing: every binary operator here is left
		// associative, so the right operand needs one level tighter.
		writeExpr(b, x.R, p+1)
	case *InExpr:
		writeExpr(b, x.X, precCompare+1)
		b.WriteString(" in ")
		writeExpr(b, x.Set, precCompare+1)
	case *CallExpr:
		b.WriteString(x.Fn)
		b.WriteByte('(')
		for i, a := range x.Args {
			if i > 0 {
				b.WriteString(", ")
			}
			writeExpr(b, a, precLowest)
		}
		b.WriteByte(')')
	case *IfExpr:
		b.WriteString("if ")
		writeExpr(b, x.Cond, precLowest)
		b.WriteString(" then ")
		writeExpr(b, x.Then, precLowest)
		b.WriteString(" else ")
		writeExpr(b, x.Else, precLowest)
	case *SlabExpr:
		b.WriteString("slab ")
		writeExpr(b, x.Subject, precAdd)
		b.WriteString(" { ")
		for _, arm := range x.Arms {
			b.WriteString(opText(arm.Op))
			b.WriteByte(' ')
			writeExpr(b, arm.Bound, precAdd)
			b.WriteString(" -> ")
			writeExpr(b, arm.Val, precAdd)
			b.WriteString("; ")
		}
		if x.Default != nil {
			b.WriteString("else -> ")
			writeExpr(b, x.Default, precAdd)
			b.WriteByte(' ')
		}
		b.WriteByte('}')
	default:
		b.WriteString("<expr>")
	}
}

func opText(k Kind) string {
	switch k {
	case AND:
		return "and"
	case OR:
		return "or"
	}
	return k.String()
}

func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
