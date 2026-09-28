package srl

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/setu-platform/setu/pkg/value"
)

// Parser is a recursive-descent parser for SRL.
//
// It is deliberately forgiving in recovery and strict in what it accepts: a
// regulatory author should get every error in a file in one pass, with a
// position and a hint, rather than one error per compile.
type Parser struct {
	toks []Token
	i    int
	file string
	errs []Diagnostic
}

// Parse parses one SRL source file, returning the file and every diagnostic
// found. A non-nil file may still accompany errors; callers must check the
// diagnostics before using it.
func Parse(path, src string) (*File, []Diagnostic) {
	lx := NewLexer(path, src)
	toks := lx.Tokens()
	p := &Parser{toks: toks, file: path, errs: lx.Diagnostics()}
	f := p.parseFile()
	return f, p.errs
}

// ---------- token helpers ----------

func (p *Parser) cur() Token { return p.toks[p.i] }

func (p *Parser) at(k Kind) bool { return p.cur().Kind == k }

func (p *Parser) atWord(w string) bool {
	t := p.cur()
	return t.Kind == IDENT && t.Text == w
}

func (p *Parser) advance() Token {
	t := p.toks[p.i]
	if t.Kind != EOF {
		p.i++
	}
	return t
}

func (p *Parser) accept(k Kind) bool {
	if p.at(k) {
		p.advance()
		return true
	}
	return false
}

func (p *Parser) acceptWord(w string) bool {
	if p.atWord(w) {
		p.advance()
		return true
	}
	return false
}

func (p *Parser) errorf(pos Pos, code, hint, format string, args ...any) {
	// Cap diagnostics so a badly mangled file cannot flood the console.
	if len(p.errs) > 200 {
		return
	}
	p.errs = append(p.errs, Diagnostic{
		Severity: SevError, Pos: pos, Code: code, Hint: hint,
		Msg: fmt.Sprintf(format, args...),
	})
}

func (p *Parser) expect(k Kind) Token {
	if p.at(k) {
		return p.advance()
	}
	p.errorf(p.cur().Pos, "SRL0010", "", "expected %s, found %s", k, p.cur())
	return Token{Kind: k, Pos: p.cur().Pos}
}

func (p *Parser) expectWord(w string) Token {
	if p.atWord(w) {
		return p.advance()
	}
	p.errorf(p.cur().Pos, "SRL0010", "", "expected %q, found %s", w, p.cur())
	return Token{Kind: IDENT, Text: w, Pos: p.cur().Pos}
}

func (p *Parser) expectIdent() Token {
	if p.at(IDENT) {
		return p.advance()
	}
	p.errorf(p.cur().Pos, "SRL0010", "", "expected an identifier, found %s", p.cur())
	return Token{Kind: IDENT, Pos: p.cur().Pos}
}

func (p *Parser) expectString() string {
	if p.at(STRING) {
		return p.advance().Text
	}
	p.errorf(p.cur().Pos, "SRL0010", "quote the text", "expected a quoted string, found %s", p.cur())
	if p.cur().Kind != EOF && p.cur().Kind != RBRACE {
		p.advance()
	}
	return ""
}

// qualified reads a dotted identifier path such as TN.FACTORIES.LICENCE.
func (p *Parser) qualified() []string {
	var out []string
	out = append(out, p.expectIdent().Text)
	for p.at(DOT) {
		// A dot followed by something that is not an identifier ends the path.
		if p.toks[p.i+1].Kind != IDENT {
			break
		}
		p.advance()
		out = append(out, p.advance().Text)
	}
	return out
}

func (p *Parser) qualifiedName() string { return strings.Join(p.qualified(), ".") }

// ---------- file ----------

func (p *Parser) parseFile() *File {
	f := &File{Path: p.file, Meta: Block{}}

	p.expectWord("package")
	f.Package = p.qualifiedName()

	for !p.at(EOF) {
		t := p.cur()
		if t.Kind != IDENT {
			p.errorf(t.Pos, "SRL0011", "declarations start with a keyword such as `rule` or `approval`",
				"expected a declaration, found %s", t)
			p.advance()
			continue
		}
		switch t.Text {
		case "meta":
			p.advance()
			p.parseMeta(f)
		case "enum":
			f.Enums = append(f.Enums, p.parseEnum())
		case "profile":
			d := p.parseProfile()
			if f.Profile != nil {
				p.errorf(d.Pos, "SRL0012",
					"merge the declarations; a jurisdiction has exactly one profile schema",
					"duplicate profile declaration in this file")
			}
			f.Profile = d
		case "department":
			f.Departments = append(f.Departments, p.parseDepartment())
		case "document":
			f.Documents = append(f.Documents, p.parseDocument())
		case "approval":
			f.Approvals = append(f.Approvals, p.parseApproval())
		case "fee":
			f.Fees = append(f.Fees, p.parseFee())
		case "obligation":
			f.Obligations = append(f.Obligations, p.parseObligation())
		case "incentive":
			f.Incentives = append(f.Incentives, p.parseIncentive())
		case "rule":
			f.Rules = append(f.Rules, p.parseRule())
		default:
			p.errorf(t.Pos, "SRL0011", "valid declarations: meta, enum, profile, department, document, approval, fee, obligation, incentive, rule",
				"unknown declaration %q", t.Text)
			p.skipToNextDecl()
		}
	}
	return f
}

// skipToNextDecl recovers after a bad declaration by discarding tokens until a
// plausible declaration keyword at brace depth zero.
func (p *Parser) skipToNextDecl() {
	depth := 0
	for !p.at(EOF) {
		switch p.cur().Kind {
		case LBRACE:
			depth++
		case RBRACE:
			depth--
			if depth <= 0 {
				p.advance()
				return
			}
		case IDENT:
			if depth == 0 && isDeclKeyword(p.cur().Text) {
				return
			}
		}
		p.advance()
	}
}

func isDeclKeyword(s string) bool {
	switch s {
	case "meta", "enum", "profile", "department", "document", "approval",
		"fee", "obligation", "incentive", "rule":
		return true
	}
	return false
}

// fields runs a field loop over a braced block, calling handle for each field
// keyword. handle returns false when it does not recognise the keyword.
func (p *Parser) fields(what string, handle func(word string, pos Pos) bool) {
	p.expect(LBRACE)
	for !p.at(RBRACE) && !p.at(EOF) {
		if p.accept(SEMI) || p.accept(COMMA) {
			continue
		}
		t := p.cur()
		if t.Kind != IDENT {
			p.errorf(t.Pos, "SRL0013", "", "expected a field name in %s, found %s", what, t)
			p.advance()
			continue
		}
		before := p.i
		p.advance()
		if !handle(t.Text, t.Pos) {
			p.errorf(t.Pos, "SRL0014", "", "unknown field %q in %s", t.Text, what)
			p.skipField()
		}
		if p.i == before { // defensive: never loop without consuming a token
			p.advance()
		}
	}
	p.expect(RBRACE)
}

// skipField discards the remainder of an unrecognised field. It always
// consumes at least one token, so that no recovery path can spin.
func (p *Parser) skipField() {
	start := p.i
	defer func() {
		if p.i == start && !p.at(EOF) && !p.at(RBRACE) {
			p.advance()
		}
	}()
	depth := 0
	for !p.at(EOF) {
		switch p.cur().Kind {
		case LBRACE, LBRACK:
			depth++
		case RBRACE, RBRACK:
			if depth == 0 {
				return
			}
			depth--
			p.advance()
			if depth == 0 {
				return
			}
			continue
		case SEMI:
			if depth == 0 {
				p.advance()
				return
			}
		case IDENT:
			// A field keyword at depth zero starts the next field.
			if depth == 0 && p.toks[p.i-1].Kind != IDENT {
				return
			}
		}
		p.advance()
	}
}

func (p *Parser) parseMeta(f *File) {
	p.fields("meta", func(word string, pos Pos) bool {
		f.Meta[word] = p.expectString()
		return true
	})
}

// ---------- localised text, legal basis, governance ----------

// localised reads `<field> <lang> "text"` and accumulates into m.
func (p *Parser) localised(m *LocalisedText, field string) {
	if *m == nil {
		*m = LocalisedText{}
	}
	lang := p.expectIdent().Text
	txt := p.expectString()
	if _, dup := (*m)[lang]; dup {
		p.errorf(p.cur().Pos, "SRL0015", "", "duplicate %s text for language %q", field, lang)
	}
	(*m)[lang] = txt
}

func (p *Parser) parseLegalBasis() LegalBasis {
	lb := LegalBasis{Pos: p.cur().Pos}
	p.fields("legal_basis", func(word string, pos Pos) bool {
		switch word {
		case "act":
			lb.Act = p.expectString()
		case "section":
			lb.Section = p.expectString()
		case "rule":
			lb.Rule = p.expectString()
		case "notification":
			lb.Notification = p.expectString()
		case "dated":
			lb.Dated = p.parseDate()
		case "url":
			lb.URL = p.expectString()
		default:
			return false
		}
		return true
	})
	return lb
}

// governanceField handles the ownership fields common to every regulatory
// object. Returning false lets the caller try its own field names.
func (p *Parser) governanceField(g *Governance, word string, pos Pos) bool {
	switch word {
	case "owner":
		g.Owner = p.expectString()
		g.Pos = pos
	case "review_status":
		w := p.expectIdent()
		st, ok := ParseReviewStatus(w.Text)
		if !ok {
			p.errorf(w.Pos, "SRL0016", "one of: unverified, in_review, verified, deprecated",
				"unknown review status %q", w.Text)
		}
		g.ReviewStatus = st
	case "last_verified":
		g.LastVerified = p.parseDate()
	case "review_cadence":
		g.ReviewCadence = p.parsePeriod()
	default:
		return false
	}
	return true
}

// validityField handles `effective from D [until D]`.
func (p *Parser) validityField(v *Validity, word string, pos Pos) bool {
	if word != "effective" {
		return false
	}
	p.expectWord("from")
	v.From = p.parseDate()
	if p.acceptWord("until") {
		v.Until = p.parseDate()
	}
	return true
}

func (p *Parser) parseDate() value.Date {
	t := p.cur()
	if t.Kind != DATE {
		p.errorf(t.Pos, "SRL0017", "dates are written YYYY-MM-DD", "expected a date, found %s", t)
		if t.Kind != EOF && t.Kind != RBRACE {
			p.advance()
		}
		return value.Date{}
	}
	p.advance()
	d, err := value.ParseDate(t.Text)
	if err != nil {
		p.errorf(t.Pos, "SRL0017", "", "invalid date %q: %v", t.Text, err)
	}
	return d
}

var periodWords = map[string]string{
	"day": "d", "days": "d", "week": "w", "weeks": "w",
	"month": "m", "months": "m", "year": "y", "years": "y",
}

// parsePeriod reads a calendar period such as `30 days` or `1 year 6 months`.
func (p *Parser) parsePeriod() value.Period {
	var out value.Period
	got := false
	for p.at(NUMBER) && p.toks[p.i+1].Kind == IDENT {
		w, ok := periodWords[p.toks[p.i+1].Text]
		if !ok {
			break
		}
		numTok := p.advance()
		p.advance()
		n, ok := parseInt(numTok.Text)
		if !ok {
			p.errorf(numTok.Pos, "SRL0018", "", "period length must be a whole number, found %q", numTok.Text)
		}
		switch w {
		case "d":
			out.Days += n
		case "w":
			out.Days += 7 * n
		case "m":
			out.Months += n
		case "y":
			out.Years += n
		}
		got = true
	}
	if !got {
		p.errorf(p.cur().Pos, "SRL0018", "for example: 30 days, 1 year, 1 year 6 months",
			"expected a period, found %s", p.cur())
		if p.cur().Kind != EOF && p.cur().Kind != RBRACE {
			p.advance()
		}
	}
	return out
}

func (p *Parser) parseInt(field string) int {
	t := p.cur()
	if t.Kind != NUMBER {
		p.errorf(t.Pos, "SRL0019", "", "%s expects a whole number, found %s", field, t)
		if t.Kind != EOF && t.Kind != RBRACE {
			p.advance()
		}
		return 0
	}
	p.advance()
	n, ok := parseInt(t.Text)
	if !ok {
		p.errorf(t.Pos, "SRL0019", "", "%s expects a whole number, found %q", field, t.Text)
	}
	return n
}

func parseInt(s string) (int, bool) {
	r, ok := new(big.Rat).SetString(s)
	if !ok || !r.IsInt() {
		return 0, false
	}
	return int(r.Num().Int64()), true
}

// idList reads `[ A.B, C.D ]`.
func (p *Parser) idList() []string {
	var out []string
	p.expect(LBRACK)
	for !p.at(RBRACK) && !p.at(EOF) {
		if p.accept(COMMA) {
			continue
		}
		out = append(out, p.qualifiedName())
	}
	p.expect(RBRACK)
	return out
}

// stringList reads `[ "a", "b" ]`.
func (p *Parser) stringList() []string {
	var out []string
	p.expect(LBRACK)
	for !p.at(RBRACK) && !p.at(EOF) {
		if p.accept(COMMA) {
			continue
		}
		out = append(out, p.expectString())
	}
	p.expect(RBRACK)
	return out
}
