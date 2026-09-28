// Package srl implements the SETU Rule Language: its lexer, abstract syntax
// tree, parser and canonical formatter.
//
// Rules are data, not code (spec 5.5.3). SRL exists so that a regulatory
// content officer with legal training and no programming background can read a
// rule, see the statute it comes from, and satisfy themselves that the encoding
// matches the law. Every construct in the language is therefore chosen for
// legibility to that reader first and expressive power second.
package srl

import "fmt"

// Kind classifies a lexical token.
type Kind uint8

const (
	EOF Kind = iota
	IDENT
	NUMBER
	STRING
	DATE

	LPAREN
	RPAREN
	LBRACE
	RBRACE
	LBRACK
	RBRACK
	COMMA
	SEMI
	COLON
	DOT
	ARROW // ->

	ASSIGN // =
	EQ     // ==
	NE     // !=
	LT
	LE
	GT
	GE

	PLUS
	MINUS
	STAR
	SLASH

	// Hard keywords. Every other word in the language is contextual, so that
	// rule authors are not tripped by a declaration field name colliding with
	// an identifier they wanted to use.
	AND
	OR
	NOT
	IN
	TRUE
	FALSE
	IF
	THEN
	ELSE
	UNKNOWN
)

var kindNames = map[Kind]string{
	EOF: "end of file", IDENT: "identifier", NUMBER: "number", STRING: "string",
	DATE: "date", LPAREN: "(", RPAREN: ")", LBRACE: "{", RBRACE: "}",
	LBRACK: "[", RBRACK: "]", COMMA: ",", SEMI: ";", COLON: ":", DOT: ".",
	ARROW: "->", ASSIGN: "=", EQ: "==", NE: "!=", LT: "<", LE: "<=", GT: ">",
	GE: ">=", PLUS: "+", MINUS: "-", STAR: "*", SLASH: "/", AND: "and",
	OR: "or", NOT: "not", IN: "in", TRUE: "true", FALSE: "false", IF: "if",
	THEN: "then", ELSE: "else", UNKNOWN: "unknown",
}

func (k Kind) String() string {
	if n, ok := kindNames[k]; ok {
		return n
	}
	return fmt.Sprintf("token(%d)", uint8(k))
}

// keywords are the words a rule author may not use as an identifier.
//
// `then` is deliberately absent. It is both the separator inside an `if`
// expression and the name of a rule's effect block, and reserving it would
// stop `then { ... }` from reaching the field dispatcher. The parser accepts
// it contextually instead, which is unambiguous at both sites.
var keywords = map[string]Kind{
	"and": AND, "or": OR, "not": NOT, "in": IN, "true": TRUE, "false": FALSE,
	"if": IF, "else": ELSE, "unknown": UNKNOWN,
}

// Pos is a source position. Rule Studio surfaces these directly in its editor,
// and every diagnostic the toolchain emits carries one.
type Pos struct {
	File string
	Line int
	Col  int
}

func (p Pos) String() string {
	if p.File == "" {
		return fmt.Sprintf("%d:%d", p.Line, p.Col)
	}
	return fmt.Sprintf("%s:%d:%d", p.File, p.Line, p.Col)
}

// Token is a lexed token with its source text and position.
type Token struct {
	Kind Kind
	Text string
	Pos  Pos
}

func (t Token) String() string {
	if t.Kind == IDENT || t.Kind == NUMBER || t.Kind == STRING || t.Kind == DATE {
		return fmt.Sprintf("%s %q", t.Kind, t.Text)
	}
	return t.Kind.String()
}
