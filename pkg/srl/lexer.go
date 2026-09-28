package srl

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Lexer turns SRL source into tokens.
type Lexer struct {
	src  string
	file string
	off  int
	line int
	col  int
	errs []Diagnostic
}

// Diagnostic is a compiler message. Severity separates the errors that block
// publication from the lints that merely require an author to look.
type Diagnostic struct {
	Severity Severity
	Pos      Pos
	Msg      string
	Hint     string // an actionable suggestion, shown in Rule Studio
	Code     string // stable code, e.g. SRL0007, so messages can be catalogued (spec 19)
}

// Severity ranks a diagnostic.
type Severity uint8

const (
	SevError Severity = iota
	SevWarning
	SevInfo
)

func (s Severity) String() string {
	switch s {
	case SevError:
		return "error"
	case SevWarning:
		return "warning"
	}
	return "info"
}

func (d Diagnostic) String() string {
	s := fmt.Sprintf("%s: %s: %s", d.Pos, d.Severity, d.Msg)
	if d.Code != "" {
		s = fmt.Sprintf("%s: %s [%s]: %s", d.Pos, d.Severity, d.Code, d.Msg)
	}
	if d.Hint != "" {
		s += "\n    hint: " + d.Hint
	}
	return s
}

// NewLexer constructs a lexer over one source file.
func NewLexer(file, src string) *Lexer {
	return &Lexer{src: src, file: file, line: 1, col: 1}
}

// Diagnostics returns any lexical errors collected.
func (l *Lexer) Diagnostics() []Diagnostic { return l.errs }

func (l *Lexer) errorf(pos Pos, code, hint, format string, args ...any) {
	l.errs = append(l.errs, Diagnostic{
		Severity: SevError, Pos: pos, Code: code, Hint: hint,
		Msg: fmt.Sprintf(format, args...),
	})
}

func (l *Lexer) pos() Pos { return Pos{File: l.file, Line: l.line, Col: l.col} }

func (l *Lexer) peek() rune {
	if l.off >= len(l.src) {
		return 0
	}
	r, _ := utf8.DecodeRuneInString(l.src[l.off:])
	return r
}

func (l *Lexer) peekAt(n int) rune {
	off := l.off
	for i := 0; i < n; i++ {
		if off >= len(l.src) {
			return 0
		}
		_, w := utf8.DecodeRuneInString(l.src[off:])
		off += w
	}
	if off >= len(l.src) {
		return 0
	}
	r, _ := utf8.DecodeRuneInString(l.src[off:])
	return r
}

func (l *Lexer) next() rune {
	if l.off >= len(l.src) {
		return 0
	}
	r, w := utf8.DecodeRuneInString(l.src[l.off:])
	l.off += w
	if r == '\n' {
		l.line++
		l.col = 1
	} else {
		l.col++
	}
	return r
}

// Tokens lexes the whole source.
func (l *Lexer) Tokens() []Token {
	var out []Token
	for {
		t := l.scan()
		out = append(out, t)
		if t.Kind == EOF {
			return out
		}
	}
}

func (l *Lexer) scan() Token {
	l.skipSpaceAndComments()
	pos := l.pos()
	if l.off >= len(l.src) {
		return Token{Kind: EOF, Pos: pos}
	}
	r := l.peek()

	switch {
	case isIdentStart(r):
		return l.scanIdent(pos)
	case unicode.IsDigit(r):
		return l.scanNumberOrDate(pos)
	case r == '"':
		return l.scanString(pos)
	}

	l.next()
	two := func(second rune, ifTwo, ifOne Kind) Token {
		if l.peek() == second {
			l.next()
			return Token{Kind: ifTwo, Text: ifTwo.String(), Pos: pos}
		}
		return Token{Kind: ifOne, Text: ifOne.String(), Pos: pos}
	}

	switch r {
	case '(':
		return Token{Kind: LPAREN, Text: "(", Pos: pos}
	case ')':
		return Token{Kind: RPAREN, Text: ")", Pos: pos}
	case '{':
		return Token{Kind: LBRACE, Text: "{", Pos: pos}
	case '}':
		return Token{Kind: RBRACE, Text: "}", Pos: pos}
	case '[':
		return Token{Kind: LBRACK, Text: "[", Pos: pos}
	case ']':
		return Token{Kind: RBRACK, Text: "]", Pos: pos}
	case ',':
		return Token{Kind: COMMA, Text: ",", Pos: pos}
	case ';':
		return Token{Kind: SEMI, Text: ";", Pos: pos}
	case ':':
		return Token{Kind: COLON, Text: ":", Pos: pos}
	case '.':
		return Token{Kind: DOT, Text: ".", Pos: pos}
	case '+':
		return Token{Kind: PLUS, Text: "+", Pos: pos}
	case '*':
		return Token{Kind: STAR, Text: "*", Pos: pos}
	case '/':
		return Token{Kind: SLASH, Text: "/", Pos: pos}
	case '-':
		if l.peek() == '>' {
			l.next()
			return Token{Kind: ARROW, Text: "->", Pos: pos}
		}
		return Token{Kind: MINUS, Text: "-", Pos: pos}
	case '=':
		return two('=', EQ, ASSIGN)
	case '<':
		return two('=', LE, LT)
	case '>':
		return two('=', GE, GT)
	case '!':
		if l.peek() == '=' {
			l.next()
			return Token{Kind: NE, Text: "!=", Pos: pos}
		}
		l.errorf(pos, "SRL0001", "use `not` for negation", "unexpected %q", "!")
		return l.scan()
	}

	l.errorf(pos, "SRL0001", "", "unexpected character %q", string(r))
	return l.scan()
}

func (l *Lexer) skipSpaceAndComments() {
	for l.off < len(l.src) {
		r := l.peek()
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			l.next()
			continue
		}
		if r == '/' && l.peekAt(1) == '/' {
			for l.off < len(l.src) && l.peek() != '\n' {
				l.next()
			}
			continue
		}
		if r == '/' && l.peekAt(1) == '*' {
			start := l.pos()
			l.next()
			l.next()
			closed := false
			for l.off < len(l.src) {
				if l.peek() == '*' && l.peekAt(1) == '/' {
					l.next()
					l.next()
					closed = true
					break
				}
				l.next()
			}
			if !closed {
				l.errorf(start, "SRL0002", "close it with */", "unterminated block comment")
			}
			continue
		}
		return
	}
}

func isIdentStart(r rune) bool {
	return r == '_' || unicode.IsLetter(r)
}

func isIdentPart(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

func (l *Lexer) scanIdent(pos Pos) Token {
	start := l.off
	for l.off < len(l.src) && isIdentPart(l.peek()) {
		l.next()
	}
	text := l.src[start:l.off]
	if k, ok := keywords[text]; ok {
		return Token{Kind: k, Text: text, Pos: pos}
	}
	return Token{Kind: IDENT, Text: text, Pos: pos}
}

// scanNumberOrDate handles the one genuine ambiguity in the lexer: 2019-06-12
// is a date, while 2019 - 06 is subtraction. A four-digit run followed by
// -DD-DD is taken as a date, which no arithmetic expression in a rule has ever
// needed to mean otherwise.
func (l *Lexer) scanNumberOrDate(pos Pos) Token {
	start := l.off
	if l.looksLikeDate() {
		for i := 0; i < 10; i++ {
			l.next()
		}
		return Token{Kind: DATE, Text: l.src[start:l.off], Pos: pos}
	}
	seenDot := false
	for l.off < len(l.src) {
		r := l.peek()
		if unicode.IsDigit(r) || r == '_' {
			l.next()
			continue
		}
		if r == '.' && !seenDot && unicode.IsDigit(l.peekAt(1)) {
			seenDot = true
			l.next()
			continue
		}
		break
	}
	text := strings.ReplaceAll(l.src[start:l.off], "_", "")
	return Token{Kind: NUMBER, Text: text, Pos: pos}
}

func (l *Lexer) looksLikeDate() bool {
	if l.off+10 > len(l.src) {
		return false
	}
	s := l.src[l.off : l.off+10]
	for i, c := range s {
		switch i {
		case 4, 7:
			if c != '-' {
				return false
			}
		default:
			if c < '0' || c > '9' {
				return false
			}
		}
	}
	// Reject 2019-06-123 style: a digit may not immediately follow.
	if l.off+10 < len(l.src) {
		if c := l.src[l.off+10]; c >= '0' && c <= '9' {
			return false
		}
	}
	return true
}

func (l *Lexer) scanString(pos Pos) Token {
	l.next() // opening quote
	var b strings.Builder
	for {
		if l.off >= len(l.src) {
			l.errorf(pos, "SRL0003", "add a closing quote", "unterminated string")
			break
		}
		r := l.next()
		if r == '"' {
			break
		}
		if r == '\n' {
			l.errorf(pos, "SRL0003", "strings may not span lines; use \\n",
				"unterminated string")
			break
		}
		if r == '\\' {
			esc := l.next()
			switch esc {
			case 'n':
				b.WriteRune('\n')
			case 't':
				b.WriteRune('\t')
			case '"':
				b.WriteRune('"')
			case '\\':
				b.WriteRune('\\')
			case '{':
				b.WriteString("\\{")
			case '}':
				b.WriteString("\\}")
			default:
				l.errorf(l.pos(), "SRL0004", "", "unknown escape \\%s", string(esc))
			}
			continue
		}
		b.WriteRune(r)
	}
	return Token{Kind: STRING, Text: b.String(), Pos: pos}
}
