package sema

import (
	"fmt"
	"sort"
	"strings"

	"github.com/setu-platform/setu/pkg/kg"
	"github.com/setu-platform/setu/pkg/srl"
	"github.com/setu-platform/setu/pkg/unit"
)

// Checker resolves names and checks types across a whole graph.
type Checker struct {
	g     *kg.Graph
	diags []srl.Diagnostic
	// types caches the resolved type of each profile variable.
	varTypes map[string]Type
}

// Check runs name resolution and type checking over the graph and returns
// every diagnostic found. It never stops at the first error: a regulatory
// author should see all of them at once.
func Check(g *kg.Graph) []srl.Diagnostic {
	c := &Checker{g: g, varTypes: map[string]Type{}}
	c.resolveProfile()
	c.checkDeclarations()
	c.checkRules()
	c.sortDiags()
	return c.diags
}

// VarTypes exposes the resolved profile schema, which the evaluator and the
// property-based test generator both need.
func VarTypes(g *kg.Graph) (map[string]Type, []srl.Diagnostic) {
	c := &Checker{g: g, varTypes: map[string]Type{}}
	c.resolveProfile()
	return c.varTypes, c.diags
}

func (c *Checker) errorf(pos srl.Pos, code, hint, format string, args ...any) {
	c.diags = append(c.diags, srl.Diagnostic{
		Severity: srl.SevError, Pos: pos, Code: code, Hint: hint,
		Msg: fmt.Sprintf(format, args...),
	})
}

func (c *Checker) warnf(pos srl.Pos, code, hint, format string, args ...any) {
	c.diags = append(c.diags, srl.Diagnostic{
		Severity: srl.SevWarning, Pos: pos, Code: code, Hint: hint,
		Msg: fmt.Sprintf(format, args...),
	})
}

func (c *Checker) sortDiags() {
	sort.SliceStable(c.diags, func(i, j int) bool {
		a, b := c.diags[i].Pos, c.diags[j].Pos
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Col < b.Col
	})
}

// resolveProfile turns each declared variable's syntactic type into a Type.
func (c *Checker) resolveProfile() {
	for _, name := range c.g.VarNames() {
		v := c.g.Vars[name]
		c.varTypes[name] = c.resolveTypeExpr(v.Type)
	}
}

func (c *Checker) resolveTypeExpr(t srl.TypeExpr) Type {
	switch t.Name {
	case "Bool":
		return TBool
	case "Text":
		return TText
	case "Date":
		return TDate
	case "Period":
		return TPeriod
	case "Geo":
		return TGeo
	case "Money":
		return TMoney
	case "Number":
		return TScalar
	case "Quantity":
		d, ok := unit.DimensionByName(t.Param)
		if !ok {
			c.errorf(t.Pos, "SEM0001",
				"dimensions: "+strings.Join(unit.DimensionNames(), ", "),
				"unknown dimension %q", t.Param)
			return TInvalid
		}
		return Num(d)
	case "Enum":
		if _, ok := c.g.Enums[t.Param]; !ok {
			c.errorf(t.Pos, "SEM0002", "declare it with `enum "+t.Param+" { ... }`",
				"unknown enum type %q", t.Param)
			return TInvalid
		}
		return Enum(t.Param)
	case "Set":
		if t.Elem == nil {
			c.errorf(t.Pos, "SEM0003", "for example Set<Text>", "Set needs an element type")
			return TInvalid
		}
		return Set(c.resolveTypeExpr(*t.Elem))
	}
	c.errorf(t.Pos, "SEM0004",
		"types: Bool, Number, Money, Text, Date, Period, Geo, Quantity<Dim>, Enum<Name>, Set<T>",
		"unknown type %q", t.Name)
	return TInvalid
}

// checkDeclarations verifies cross-references between declared objects.
func (c *Checker) checkDeclarations() {
	for _, id := range c.g.ApprovalIDs() {
		a := c.g.Approvals[id]
		c.refDepartment(a.Department, a.Pos, "approval "+id)
		for _, d := range a.Documents {
			c.refDocument(d, a.Pos, "approval "+id)
		}
		for _, f := range a.Fees {
			c.refFee(f, a.Pos, "approval "+id)
		}
		for _, o := range a.Obligations {
			c.refObligation(o, a.Pos, "approval "+id)
		}
		for _, dep := range a.DependsOn {
			pos := dep.Pos
			if pos.Line == 0 {
				pos = a.Pos
			}
			if dep.Approval == id {
				c.errorf(pos, "SEM0011", "remove the self-reference",
					"approval %s depends on itself", id)
				continue
			}
			c.refApproval(dep.Approval, pos, "approval "+id)
		}
	}
	for _, id := range c.g.FeeIDs() {
		f := c.g.Fees[id]
		c.refDepartment(f.Department, f.Pos, "fee "+id)
		if f.Amount == nil {
			c.errorf(f.Pos, "SEM0012", "add `amount = ...`", "fee %s has no amount", id)
			continue
		}
		t := c.checkExpr(f.Amount, exprCtx{what: "fee " + id})
		if t.Kind != Invalid && !(t.Kind == Number && t.Dim == unit.DimMoney) {
			c.errorf(f.Amount.Position(), "SEM0013",
				"write amounts as `INR 1000` or `INR 30 lakh`",
				"fee %s computes %s, but a fee must be an amount of money", id, t)
		}
	}
	for _, id := range c.g.ObligationIDs() {
		o := c.g.Obligations[id]
		c.refDepartment(o.Department, o.Pos, "obligation "+id)
		for _, e := range o.Evidence {
			c.refDocument(e, o.Pos, "obligation "+id)
		}
		if o.DueMonth != 0 && (o.DueMonth < 1 || o.DueMonth > 12) {
			c.errorf(o.Pos, "SEM0014", "", "obligation %s has due month %d", id, o.DueMonth)
		}
		if o.DueDay != 0 && (o.DueDay < 1 || o.DueDay > 31) {
			c.errorf(o.Pos, "SEM0014", "", "obligation %s has due day %d", id, o.DueDay)
		}
	}
	for _, id := range c.g.IncentiveIDs() {
		in := c.g.Incentives[id]
		c.refDepartment(in.Department, in.Pos, "incentive "+id)
		for _, e := range in.Evidence {
			c.refDocument(e, in.Pos, "incentive "+id)
		}
		if in.Value != nil {
			t := c.checkExpr(in.Value, exprCtx{what: "incentive " + id})
			if t.Kind != Invalid && !(t.Kind == Number && t.Dim == unit.DimMoney) {
				c.errorf(in.Value.Position(), "SEM0013", "",
					"incentive %s computes %s, but an entitlement value must be money", id, t)
			}
		}
	}
	for _, id := range c.g.DocumentIDs() {
		d := c.g.Documents[id]
		if d.Verification == srl.VerifyAPI && d.SourceSystem == "" {
			c.warnf(d.Pos, "SEM0015",
				`name the adapter, for example: verification api "NSDL_PAN"`,
				"document %s claims API verification but names no source system", id)
		}
	}
}

func (c *Checker) refDepartment(id string, pos srl.Pos, in string) {
	if id == "" {
		return
	}
	if _, ok := c.g.Departments[id]; !ok {
		c.errorf(pos, "SEM0010", c.suggest(id, c.g.DepartmentIDs()),
			"%s references unknown department %s", in, id)
	}
}

func (c *Checker) refDocument(id string, pos srl.Pos, in string) {
	if _, ok := c.g.Documents[id]; !ok {
		c.errorf(pos, "SEM0010", c.suggest(id, c.g.DocumentIDs()),
			"%s references unknown document %s", in, id)
	}
}

func (c *Checker) refApproval(id string, pos srl.Pos, in string) {
	if _, ok := c.g.Approvals[id]; !ok {
		c.errorf(pos, "SEM0010", c.suggest(id, c.g.ApprovalIDs()),
			"%s references unknown approval %s", in, id)
	}
}

func (c *Checker) refFee(id string, pos srl.Pos, in string) {
	if _, ok := c.g.Fees[id]; !ok {
		c.errorf(pos, "SEM0010", c.suggest(id, c.g.FeeIDs()),
			"%s references unknown fee %s", in, id)
	}
}

func (c *Checker) refObligation(id string, pos srl.Pos, in string) {
	if _, ok := c.g.Obligations[id]; !ok {
		c.errorf(pos, "SEM0010", c.suggest(id, c.g.ObligationIDs()),
			"%s references unknown obligation %s", in, id)
	}
}

func (c *Checker) refIncentive(id string, pos srl.Pos, in string) {
	if _, ok := c.g.Incentives[id]; !ok {
		c.errorf(pos, "SEM0010", c.suggest(id, c.g.IncentiveIDs()),
			"%s references unknown incentive %s", in, id)
	}
}

// suggest offers the nearest declared identifier, which is by far the most
// common cause of an unresolved reference in hand-authored content.
func (c *Checker) suggest(id string, candidates []string) string {
	best, bestD := "", 1<<30
	for _, cand := range candidates {
		if d := editDistance(id, cand); d < bestD {
			best, bestD = cand, d
		}
	}
	if best != "" && bestD <= 4 {
		return "did you mean " + best + "?"
	}
	return ""
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
			m := cur[j-1] + 1
			if prev[j]+1 < m {
				m = prev[j] + 1
			}
			if prev[j-1]+cost < m {
				m = prev[j-1] + cost
			}
			cur[j] = m
		}
		copy(prev, cur)
	}
	return prev[len(br)]
}

// exprCtx carries what is being checked, for message wording.
type exprCtx struct {
	what string
}

func (c *Checker) checkRules() {
	for _, id := range c.g.RuleIDs() {
		r := c.g.Rules[id]
		ctx := exprCtx{what: "rule " + id}

		if r.When == nil {
			c.errorf(r.Pos, "SEM0020", "add a `when` clause",
				"rule %s has no condition", id)
		} else {
			t := c.checkExpr(r.When, ctx)
			if t.Kind != Invalid && t.Kind != Bool && t.Kind != Untyped {
				c.errorf(r.When.Position(), "SEM0021", "",
					"rule %s has a condition of type %s; it must be Bool", id, t)
			}
		}

		if len(r.Then) == 0 {
			c.errorf(r.Pos, "SEM0022", "add a `then { ... }` block",
				"rule %s produces no effect and can never change an outcome", id)
		}
		for _, e := range r.Then {
			c.checkEffect(r, e)
		}
		for _, o := range r.Overrides {
			if _, ok := c.g.Rules[o]; !ok {
				c.errorf(r.Pos, "SEM0010", c.suggest(o, c.g.RuleIDs()),
					"rule %s overrides unknown rule %s", id, o)
				continue
			}
			if o == id {
				c.errorf(r.Pos, "SEM0023", "", "rule %s overrides itself", id)
			}
		}
		c.checkExplanations(r)
		c.checkTests(r)
	}
}

func (c *Checker) checkEffect(r *srl.RuleDecl, e srl.Effect) {
	in := "rule " + r.ID
	switch e.Kind {
	case srl.EffectRequireApproval, srl.EffectExemptApproval:
		c.refApproval(e.Target, e.Pos, in)
	case srl.EffectRequireDocument:
		c.refDocument(e.Target, e.Pos, in)
		c.refApproval(e.On, e.Pos, in)
	case srl.EffectLevyFee, srl.EffectWaiveFee:
		c.refFee(e.Target, e.Pos, in)
		c.refApproval(e.On, e.Pos, in)
	case srl.EffectCreateObligation:
		c.refObligation(e.Target, e.Pos, in)
		c.refApproval(e.On, e.Pos, in)
	case srl.EffectOfferIncentive:
		c.refIncentive(e.Target, e.Pos, in)
	case srl.EffectDependency:
		c.refApproval(e.Target, e.Pos, in)
		c.refApproval(e.On, e.Pos, in)
		if e.Target == e.On {
			c.errorf(e.Pos, "SEM0011", "", "%s makes %s depend on itself", in, e.Target)
		}
	case srl.EffectFlag:
		if e.Flag == "" {
			c.errorf(e.Pos, "SEM0024", "", "%s raises an unnamed flag", in)
		}
	}
}

// checkExplanations validates the placeholders in every localised explanation.
// A placeholder naming a variable that does not exist would reach a citizen as
// raw template text, so it is an error rather than a lint.
func (c *Checker) checkExplanations(r *srl.RuleDecl) {
	for _, lang := range r.Explain.Langs() {
		for _, ph := range Placeholders(r.Explain[lang]) {
			switch {
			case strings.HasPrefix(ph, "profile."):
				name := strings.TrimPrefix(ph, "profile.")
				if _, ok := c.g.Vars[name]; !ok {
					c.errorf(r.Pos, "SEM0030", c.suggest(name, c.g.VarNamesSorted()),
						"rule %s explanation (%s) refers to unknown profile variable %q",
						r.ID, lang, name)
				}
			case ph == "approval.title" || ph == "rule.id" || ph == "legal_basis":
				// Built-in placeholders resolved at render time.
			default:
				c.errorf(r.Pos, "SEM0031",
					"placeholders are {profile.<var>}, {approval.title}, {legal_basis} or {rule.id}",
					"rule %s explanation (%s) uses unknown placeholder {%s}", r.ID, lang, ph)
			}
		}
	}
}

// Placeholders extracts the {…} interpolations from an explanation template.
func Placeholders(s string) []string {
	var out []string
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++ // an escaped brace is literal text
			continue
		}
		if s[i] != '{' {
			continue
		}
		j := strings.IndexByte(s[i:], '}')
		if j < 0 {
			return out
		}
		out = append(out, s[i+1:i+j])
		i += j
	}
	return out
}

func (c *Checker) checkTests(r *srl.RuleDecl) {
	for _, t := range r.Tests {
		seen := map[string]bool{}
		for _, a := range t.Given {
			vt, ok := c.varTypes[a.Var]
			if !ok {
				c.errorf(a.Pos, "SEM0010", c.suggest(a.Var, c.g.VarNamesSorted()),
					"rule %s test %q assigns unknown profile variable %q", r.ID, t.Name, a.Var)
				continue
			}
			if seen[a.Var] {
				c.errorf(a.Pos, "SEM0032", "",
					"rule %s test %q assigns %s twice", r.ID, t.Name, a.Var)
			}
			seen[a.Var] = true
			at := c.checkExpr(a.Val, exprCtx{what: "rule " + r.ID + " test " + t.Name})
			if !at.Compatible(vt) {
				c.errorf(a.Pos, "SEM0033", "",
					"rule %s test %q assigns %s to %s, which is %s",
					r.ID, t.Name, at, a.Var, vt)
			}
		}
	}
}
