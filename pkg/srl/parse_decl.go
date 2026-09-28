package srl

import (
	"math/big"

	"github.com/setu-platform/setu/pkg/unit"
	"github.com/setu-platform/setu/pkg/value"
)

func (p *Parser) parseEnum() *EnumDecl {
	pos := p.expectWord("enum").Pos
	d := &EnumDecl{Name: p.expectIdent().Text, Pos: pos}
	p.expect(LBRACE)
	for !p.at(RBRACE) && !p.at(EOF) {
		if p.accept(COMMA) || p.accept(SEMI) {
			continue
		}
		m := EnumMember{Pos: p.cur().Pos, Name: p.expectIdent().Text}
		// An optional inline title block gives the member a citizen-facing name.
		if p.at(LBRACE) {
			p.fields("enum member", func(word string, _ Pos) bool {
				if word == "title" {
					p.localised(&m.Title, "title")
					return true
				}
				return false
			})
		}
		d.Members = append(d.Members, m)
	}
	p.expect(RBRACE)
	return d
}

func (p *Parser) parseProfile() *ProfileDecl {
	pos := p.expectWord("profile").Pos
	d := &ProfileDecl{Pos: pos}
	p.expect(LBRACE)
	for !p.at(RBRACE) && !p.at(EOF) {
		if p.accept(SEMI) || p.accept(COMMA) {
			continue
		}
		if !p.atWord("var") {
			p.errorf(p.cur().Pos, "SRL0020", "profile schemas contain only `var` declarations",
				"expected `var`, found %s", p.cur())
			p.advance()
			continue
		}
		d.Vars = append(d.Vars, p.parseVar())
	}
	p.expect(RBRACE)
	return d
}

func (p *Parser) parseVar() *VarDecl {
	pos := p.expectWord("var").Pos
	v := &VarDecl{Pos: pos, Name: p.expectIdent().Text}
	p.expect(COLON)
	v.Type = p.parseTypeExpr()
	if p.at(LBRACE) {
		p.fields("var "+v.Name, func(word string, fpos Pos) bool {
			switch word {
			case "question":
				p.localised(&v.Question, "question")
			case "hint":
				p.localised(&v.Hint, "hint")
			case "group":
				p.localised(&v.Group, "group")
			case "unit":
				v.Unit = p.expectIdent().Text
				if _, ok := unit.Lookup(v.Unit); !ok {
					hint := ""
					if s, ok := unit.SuggestSymbol(v.Unit); ok {
						hint = "did you mean " + s + "?"
					}
					p.errorf(fpos, "SRL0021", hint, "unknown unit %q", v.Unit)
				}
			case "example":
				v.Example = p.expectString()
			case "sensitive":
				v.Sensitive = p.parseBoolWord()
			default:
				return false
			}
			return true
		})
	}
	return v
}

func (p *Parser) parseBoolWord() bool {
	switch {
	case p.accept(TRUE):
		return true
	case p.accept(FALSE):
		return false
	}
	p.errorf(p.cur().Pos, "SRL0022", "", "expected true or false, found %s", p.cur())
	return false
}

func (p *Parser) parseTypeExpr() TypeExpr {
	pos := p.cur().Pos
	name := p.expectIdent().Text
	t := TypeExpr{Name: name, Pos: pos}
	if p.at(LT) {
		p.advance()
		switch name {
		case "Set":
			elem := p.parseTypeExpr()
			t.Elem = &elem
		default:
			t.Param = p.expectIdent().Text
		}
		if !p.accept(GT) {
			p.errorf(p.cur().Pos, "SRL0023", "close the type parameter with >",
				"expected >, found %s", p.cur())
		}
	}
	return t
}

func (p *Parser) parseDepartment() *DepartmentDecl {
	pos := p.expectWord("department").Pos
	d := &DepartmentDecl{ID: p.qualifiedName(), Pos: pos}
	p.fields("department "+d.ID, func(word string, fpos Pos) bool {
		switch word {
		case "name":
			p.localised(&d.Name, "name")
		case "jurisdiction":
			d.Jurisdiction = p.qualifiedName()
		case "adapter_tier":
			w := p.expectIdent()
			switch w.Text {
			case "api", "file", "rpa", "assisted_manual":
				d.AdapterTier = w.Text
			default:
				p.errorf(w.Pos, "SRL0024", "one of: api, file, rpa, assisted_manual",
					"unknown adapter tier %q", w.Text)
			}
		case "contact_url":
			d.ContactURL = p.expectString()
		default:
			return p.governanceField(&d.Governance, word, fpos)
		}
		return true
	})
	return d
}

func (p *Parser) parseDocument() *DocumentDecl {
	pos := p.expectWord("document").Pos
	d := &DocumentDecl{ID: p.qualifiedName(), Pos: pos}
	p.fields("document "+d.ID, func(word string, fpos Pos) bool {
		switch word {
		case "title":
			p.localised(&d.Title, "title")
		case "issuer":
			d.Issuer = p.expectString()
		case "verification":
			w := p.expectIdent()
			v, ok := ParseVerification(w.Text)
			if !ok {
				p.errorf(w.Pos, "SRL0025", "one of: api, digilocker, officer, self_declared",
					"unknown verification pathway %q", w.Text)
			}
			d.Verification = v
			// `verification api "NSDL_PAN"` names the adapter that verifies it.
			if p.at(STRING) {
				d.SourceSystem = p.advance().Text
			}
		case "max_age":
			d.MaxAge = p.parsePeriod()
		case "credential":
			d.Credential = p.expectString()
		case "attributes":
			d.Attributes = p.stringList()
		default:
			return p.governanceField(&d.Governance, word, fpos)
		}
		return true
	})
	return d
}

func (p *Parser) parseApproval() *ApprovalDecl {
	pos := p.expectWord("approval").Pos
	a := &ApprovalDecl{ID: p.qualifiedName(), Pos: pos}
	a.Valid.Until = value.Date{}
	p.fields("approval "+a.ID, func(word string, fpos Pos) bool {
		switch word {
		case "title":
			p.localised(&a.Title, "title")
		case "summary":
			p.localised(&a.Summary, "summary")
		case "category":
			a.Category = p.expectString()
		case "department":
			a.Department = p.qualifiedName()
		case "jurisdiction":
			a.Jurisdiction = p.qualifiedName()
		case "sla":
			a.SLA = p.parsePeriod()
		case "validity":
			a.Validity = p.parsePeriod()
		case "renewal":
			p.fields("renewal", func(w2 string, _ Pos) bool {
				switch w2 {
				case "notice":
					a.RenewNotice = p.parsePeriod()
				case "cadence":
					a.RenewCadence = p.parsePeriod()
				default:
					return false
				}
				return true
			})
		case "deemed_approval":
			w := p.expectIdent()
			switch w.Text {
			case "permitted":
				a.Deemed = DeemedApproval{Permitted: true, Pos: fpos}
				if p.acceptWord("section") {
					a.Deemed.Section = p.expectString()
				}
			case "prohibited":
				a.Deemed = DeemedApproval{Permitted: false, Pos: fpos}
			default:
				p.errorf(w.Pos, "SRL0026", "one of: permitted, prohibited",
					"deemed_approval must be permitted or prohibited, found %q", w.Text)
			}
		case "documents":
			a.Documents = append(a.Documents, p.idList()...)
		case "fees":
			a.Fees = append(a.Fees, p.idList()...)
		case "obligations":
			a.Obligations = append(a.Obligations, p.idList()...)
		case "depends_on":
			a.DependsOn = append(a.DependsOn, p.parseDependsOn()...)
		case "appeal":
			ap := &AppealDecl{Pos: fpos}
			p.fields("appeal", func(w2 string, _ Pos) bool {
				switch w2 {
				case "authority":
					p.localised(&ap.Authority, "authority")
				case "within":
					ap.Within = p.parsePeriod()
				default:
					return false
				}
				return true
			})
			a.Appeal = ap
		case "legal_basis":
			a.LegalBasis = p.parseLegalBasis()
		default:
			if p.validityField(&a.Valid, word, fpos) {
				return true
			}
			return p.governanceField(&a.Governance, word, fpos)
		}
		return true
	})
	return a
}

// parseDependsOn reads either a bare list of approval IDs or a block that gives
// each dependency the reason shown to an applicant whose case is blocked.
func (p *Parser) parseDependsOn() []DependencyRef {
	var out []DependencyRef
	if p.at(LBRACK) {
		for _, id := range p.idList() {
			out = append(out, DependencyRef{Approval: id})
		}
		return out
	}
	p.expect(LBRACE)
	for !p.at(RBRACE) && !p.at(EOF) {
		if p.accept(COMMA) || p.accept(SEMI) {
			continue
		}
		d := DependencyRef{Pos: p.cur().Pos, Approval: p.qualifiedName()}
		if p.at(LBRACE) {
			p.fields("dependency", func(w string, _ Pos) bool {
				if w == "reason" {
					p.localised(&d.Reason, "reason")
					return true
				}
				return false
			})
		}
		out = append(out, d)
	}
	p.expect(RBRACE)
	return out
}

func (p *Parser) parseFee() *FeeDecl {
	pos := p.expectWord("fee").Pos
	f := &FeeDecl{ID: p.qualifiedName(), Pos: pos,
		Rounding: Rounding{Mode: "none"}}
	p.fields("fee "+f.ID, func(word string, fpos Pos) bool {
		switch word {
		case "title":
			p.localised(&f.Title, "title")
		case "head":
			f.Head = p.expectString()
		case "department":
			f.Department = p.qualifiedName()
		case "amount":
			p.expect(ASSIGN)
			f.Amount = p.parseExpr()
		case "rounding":
			f.Rounding = p.parseRounding()
		case "legal_basis":
			f.LegalBasis = p.parseLegalBasis()
		default:
			if p.validityField(&f.Valid, word, fpos) {
				return true
			}
			return p.governanceField(&f.Governance, word, fpos)
		}
		return true
	})
	return f
}

// parseRounding reads `rounding nearest INR 1` or `rounding none`.
func (p *Parser) parseRounding() Rounding {
	w := p.expectIdent()
	r := Rounding{Mode: w.Text}
	switch w.Text {
	case "none":
		return r
	case "nearest", "up", "down":
	default:
		p.errorf(w.Pos, "SRL0027", "one of: none, nearest, up, down",
			"unknown rounding mode %q", w.Text)
		return Rounding{Mode: "none"}
	}
	// The increment is written as a money literal so the unit is unmistakable.
	e := p.parseExpr()
	m, ok := e.(*MoneyLit)
	if !ok {
		p.errorf(e.Position(), "SRL0027", "for example: rounding nearest INR 10",
			"rounding increment must be an amount")
		return Rounding{Mode: "none"}
	}
	r.To = m.Val
	return r
}

func (p *Parser) parseObligation() *ObligationDecl {
	pos := p.expectWord("obligation").Pos
	o := &ObligationDecl{ID: p.qualifiedName(), Pos: pos}
	p.fields("obligation "+o.ID, func(word string, fpos Pos) bool {
		switch word {
		case "title":
			p.localised(&o.Title, "title")
		case "cadence":
			o.Cadence = p.parsePeriod()
		case "due":
			// `due month 4 day 30` fixes an annual filing date.
			p.expectWord("month")
			o.DueMonth = p.parseInt("due month")
			p.expectWord("day")
			o.DueDay = p.parseInt("due day")
		case "due_within":
			o.DueWithin = p.parsePeriod()
		case "evidence":
			o.Evidence = append(o.Evidence, p.idList()...)
		case "consequence":
			p.localised(&o.Consequence, "consequence")
		case "department":
			o.Department = p.qualifiedName()
		case "legal_basis":
			o.LegalBasis = p.parseLegalBasis()
		default:
			if p.validityField(&o.Valid, word, fpos) {
				return true
			}
			return p.governanceField(&o.Governance, word, fpos)
		}
		return true
	})
	return o
}

func (p *Parser) parseIncentive() *IncentiveDecl {
	pos := p.expectWord("incentive").Pos
	in := &IncentiveDecl{ID: p.qualifiedName(), Pos: pos}
	p.fields("incentive "+in.ID, func(word string, fpos Pos) bool {
		switch word {
		case "title":
			p.localised(&in.Title, "title")
		case "scheme":
			in.Scheme = p.expectString()
		case "department":
			in.Department = p.qualifiedName()
		case "value":
			p.expect(ASSIGN)
			in.Value = p.parseExpr()
		case "window":
			p.expectWord("from")
			in.Window.From = p.parseDate()
			if p.acceptWord("until") {
				in.Window.Until = p.parseDate()
			}
		case "evidence":
			in.Evidence = append(in.Evidence, p.idList()...)
		case "apply_url":
			in.ApplyURL = p.expectString()
		case "legal_basis":
			in.LegalBasis = p.parseLegalBasis()
		default:
			return p.governanceField(&in.Governance, word, fpos)
		}
		return true
	})
	return in
}

func (p *Parser) parseRule() *RuleDecl {
	pos := p.expectWord("rule").Pos
	r := &RuleDecl{ID: p.qualifiedName(), Pos: pos, Precedence: DefaultPrecedence}
	sawPrecedence := false
	p.fields("rule "+r.ID, func(word string, fpos Pos) bool {
		switch word {
		case "title":
			p.localised(&r.Title, "title")
		case "kind":
			w := p.expectIdent()
			k, ok := ParseRuleKind(w.Text)
			if !ok {
				p.errorf(w.Pos, "SRL0028",
					"one of: trigger, exemption, document, fee, obligation, incentive, dependency",
					"unknown rule kind %q", w.Text)
			}
			r.Kind = k
			// An exemption that does not outrank the rule it displaces would
			// never take effect, so exemptions default to a higher precedence.
			if k == RuleExemption && !sawPrecedence {
				r.Precedence = DefaultPrecedence + 100
			}
		case "precedence":
			r.Precedence = p.parseInt("precedence")
			sawPrecedence = true
		case "overrides":
			r.Overrides = append(r.Overrides, p.idList()...)
		case "tags":
			r.Tags = append(r.Tags, p.stringList()...)
		case "when":
			if r.When != nil {
				p.errorf(fpos, "SRL0029", "combine the conditions with `and`",
					"rule %s has more than one `when` clause", r.ID)
			}
			r.When = p.parseExpr()
		case "then":
			r.Then = append(r.Then, p.parseEffects()...)
		case "explain":
			p.localised(&r.Explain, "explain")
		case "test":
			r.Tests = append(r.Tests, p.parseTest())
		case "legal_basis":
			r.LegalBasis = p.parseLegalBasis()
		default:
			if p.validityField(&r.Valid, word, fpos) {
				return true
			}
			return p.governanceField(&r.Governance, word, fpos)
		}
		return true
	})
	return r
}

func (p *Parser) parseEffects() []Effect {
	var out []Effect
	p.expect(LBRACE)
	for !p.at(RBRACE) && !p.at(EOF) {
		before := p.i
		if p.accept(SEMI) || p.accept(COMMA) {
			continue
		}
		t := p.cur()
		if t.Kind != IDENT {
			p.errorf(t.Pos, "SRL0030", "", "expected an effect, found %s", t)
			p.advance()
			continue
		}
		e := Effect{Pos: t.Pos}
		p.advance()
		switch t.Text {
		case "require":
			w := p.expectIdent()
			switch w.Text {
			case "approval":
				e.Kind = EffectRequireApproval
				e.Target = p.qualifiedName()
			case "document":
				e.Kind = EffectRequireDocument
				e.Target = p.qualifiedName()
				p.expectWord("for")
				e.On = p.qualifiedName()
			default:
				p.errorf(w.Pos, "SRL0030", "`require approval X` or `require document D for X`",
					"cannot require %q", w.Text)
				continue
			}
		case "exempt":
			p.expectWord("approval")
			e.Kind = EffectExemptApproval
			e.Target = p.qualifiedName()
		case "levy":
			p.expectWord("fee")
			e.Kind = EffectLevyFee
			e.Target = p.qualifiedName()
			p.expectWord("on")
			e.On = p.qualifiedName()
		case "waive":
			p.expectWord("fee")
			e.Kind = EffectWaiveFee
			e.Target = p.qualifiedName()
			p.expectWord("on")
			e.On = p.qualifiedName()
		case "create":
			p.expectWord("obligation")
			e.Kind = EffectCreateObligation
			e.Target = p.qualifiedName()
			p.expectWord("from")
			e.On = p.qualifiedName()
		case "offer":
			p.expectWord("incentive")
			e.Kind = EffectOfferIncentive
			e.Target = p.qualifiedName()
		case "depend":
			e.Kind = EffectDependency
			e.On = p.qualifiedName()
			p.expectWord("on")
			e.Target = p.qualifiedName()
		case "flag":
			e.Kind = EffectFlag
			e.Flag = p.expectString()
		default:
			p.errorf(t.Pos, "SRL0030",
				"effects are: require, exempt, levy, waive, create, offer, depend, flag",
				"unknown effect %q", t.Text)
			p.skipField()
			if p.i == before {
				p.advance()
			}
			continue
		}
		// Any effect may carry a reason shown in place of the rule's explanation.
		if p.atWord("reason") {
			p.advance()
			p.localised(&e.Reason, "reason")
		}
		out = append(out, e)
		if p.i == before {
			p.advance()
		}
	}
	p.expect(RBRACE)
	return out
}

func (p *Parser) parseTest() *TestDecl {
	t := &TestDecl{Pos: p.cur().Pos, Name: p.expectString()}
	t.Expect = TestExpectation{Fires: value.Unknown}
	sawExpect := false
	p.fields("test "+t.Name, func(word string, fpos Pos) bool {
		switch word {
		case "given":
			t.Given = append(t.Given, p.parseAssignments()...)
		case "expect":
			sawExpect = true
			t.Expect.Pos = fpos
			switch {
			case p.accept(NOT):
				p.expectWord("fires")
				t.Expect.Fires = value.False
			case p.atWord("fires"):
				p.advance()
				t.Expect.Fires = value.True
			case p.at(UNKNOWN):
				p.advance()
				t.Expect.Fires = value.Unknown
			default:
				p.errorf(p.cur().Pos, "SRL0031", "one of: fires, not fires, unknown",
					"expected an expectation, found %s", p.cur())
			}
		default:
			return false
		}
		return true
	})
	if !sawExpect {
		p.errorf(t.Pos, "SRL0031", "add `expect fires` or `expect not fires`",
			"test %q states no expectation", t.Name)
	}
	return t
}

func (p *Parser) parseAssignments() []Assignment {
	var out []Assignment
	p.expect(LBRACE)
	for !p.at(RBRACE) && !p.at(EOF) {
		if p.accept(COMMA) || p.accept(SEMI) {
			continue
		}
		a := Assignment{Pos: p.cur().Pos}
		p.expectWord("profile")
		p.expect(DOT)
		a.Var = p.expectIdent().Text
		p.expect(ASSIGN)
		a.Val = p.parseExpr()
		out = append(out, a)
	}
	p.expect(RBRACE)
	return out
}

// ratFromToken parses a numeric literal exactly.
func (p *Parser) ratFromToken(t Token) *big.Rat {
	r, ok := new(big.Rat).SetString(t.Text)
	if !ok {
		p.errorf(t.Pos, "SRL0032", "", "invalid number %q", t.Text)
		return new(big.Rat)
	}
	return r
}
