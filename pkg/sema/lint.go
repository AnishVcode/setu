package sema

import (
	"fmt"
	"sort"
	"strings"

	"github.com/setu-platform/setu/pkg/kg"
	"github.com/setu-platform/setu/pkg/srl"
	"github.com/setu-platform/setu/pkg/value"
)

// LintConfig parameterises the regulatory-content lints.
//
// Today is supplied rather than read from the clock so that a lint run is
// reproducible: the same ruleset and the same date always produce the same
// staleness findings, which matters when a publication decision is later
// reviewed.
type LintConfig struct {
	// Languages every citizen-facing string must be available in (P10).
	Languages []string
	// RequireVerified promotes governance findings from warning to error. It is
	// set when publishing to a production jurisdiction, so that content which
	// has not been through legal review cannot reach an applicant (spec 5.5.5).
	RequireVerified bool
	// Today anchors staleness checks against review cadences (spec 13).
	Today value.Date
}

// DefaultLintConfig is the configuration for authoring and CI, where content is
// expected to be incomplete and unverified.
func DefaultLintConfig(today value.Date) LintConfig {
	return LintConfig{Languages: []string{"en"}, Today: today}
}

// Lint applies the regulatory-content rules of spec 13 to a graph.
func Lint(g *kg.Graph, cfg LintConfig) []srl.Diagnostic {
	l := &linter{g: g, cfg: cfg}
	l.collectUsage()
	l.lintRules()
	l.lintApprovals()
	l.lintDocuments()
	l.lintFees()
	l.lintObligations()
	l.lintIncentives()
	l.lintDepartments()
	l.lintProfile()
	sort.SliceStable(l.diags, func(i, j int) bool {
		a, b := l.diags[i], l.diags[j]
		if a.Pos.File != b.Pos.File {
			return a.Pos.File < b.Pos.File
		}
		if a.Pos.Line != b.Pos.Line {
			return a.Pos.Line < b.Pos.Line
		}
		if a.Pos.Col != b.Pos.Col {
			return a.Pos.Col < b.Pos.Col
		}
		return a.Code < b.Code
	})
	return l.diags
}

type linter struct {
	g     *kg.Graph
	cfg   LintConfig
	diags []srl.Diagnostic

	requiredApprovals  map[string]bool // approvals some rule can require
	dependedApprovals  map[string]bool // approvals reached through a dependency edge
	usedDocuments      map[string]bool
	leviedFees         map[string]bool
	createdObligations map[string]bool
	offeredIncentives  map[string]bool
	readVars           map[string]bool
}

func (l *linter) add(sev srl.Severity, pos srl.Pos, code, hint, format string, args ...any) {
	l.diags = append(l.diags, srl.Diagnostic{
		Severity: sev, Pos: pos, Code: code, Hint: hint,
		Msg: fmt.Sprintf(format, args...),
	})
}

// governanceSeverity is warning while authoring and error when publishing to
// production, which is what makes "verified once, by a named owner" a gate
// rather than an aspiration.
func (l *linter) governanceSeverity() srl.Severity {
	if l.cfg.RequireVerified {
		return srl.SevError
	}
	return srl.SevWarning
}

func (l *linter) collectUsage() {
	l.requiredApprovals = map[string]bool{}
	l.dependedApprovals = map[string]bool{}
	l.usedDocuments = map[string]bool{}
	l.leviedFees = map[string]bool{}
	l.createdObligations = map[string]bool{}
	l.offeredIncentives = map[string]bool{}
	l.readVars = map[string]bool{}

	for _, id := range l.g.RuleIDs() {
		r := l.g.Rules[id]
		srl.Walk(r.When, func(e srl.Expr) {
			if p, ok := e.(*srl.ProfileRef); ok {
				l.readVars[p.Var] = true
			}
		})
		for _, e := range r.Then {
			switch e.Kind {
			case srl.EffectRequireApproval:
				l.requiredApprovals[e.Target] = true
			case srl.EffectRequireDocument:
				l.usedDocuments[e.Target] = true
			case srl.EffectLevyFee:
				l.leviedFees[e.Target] = true
			case srl.EffectCreateObligation:
				l.createdObligations[e.Target] = true
			case srl.EffectOfferIncentive:
				l.offeredIncentives[e.Target] = true
			case srl.EffectDependency:
				l.dependedApprovals[e.Target] = true
			}
		}
		for _, t := range r.Tests {
			for _, a := range t.Given {
				l.readVars[a.Var] = true
			}
		}
	}

	for _, id := range l.g.ApprovalIDs() {
		a := l.g.Approvals[id]
		for _, d := range a.Documents {
			l.usedDocuments[d] = true
		}
		for _, f := range a.Fees {
			l.leviedFees[f] = true
		}
		for _, o := range a.Obligations {
			l.createdObligations[o] = true
		}
		for _, d := range a.DependsOn {
			l.dependedApprovals[d.Approval] = true
		}
	}
	for _, id := range l.g.FeeIDs() {
		srl.Walk(l.g.Fees[id].Amount, func(e srl.Expr) {
			if p, ok := e.(*srl.ProfileRef); ok {
				l.readVars[p.Var] = true
			}
		})
	}
	for _, id := range l.g.IncentiveIDs() {
		in := l.g.Incentives[id]
		srl.Walk(in.Value, func(e srl.Expr) {
			if p, ok := e.(*srl.ProfileRef); ok {
				l.readVars[p.Var] = true
			}
		})
		for _, d := range in.Evidence {
			l.usedDocuments[d] = true
		}
	}
	for _, id := range l.g.ObligationIDs() {
		for _, d := range l.g.Obligations[id].Evidence {
			l.usedDocuments[d] = true
		}
	}
}

// checkLangs reports missing translations of a citizen-facing string.
func (l *linter) checkLangs(t srl.LocalisedText, pos srl.Pos, what, field string) {
	if len(t) == 0 {
		l.add(srl.SevError, pos, "LINT0001", "add `"+field+" en \"...\"`",
			"%s has no %s", what, field)
		return
	}
	var missing []string
	for _, lang := range l.cfg.Languages {
		if strings.TrimSpace(t[lang]) == "" {
			missing = append(missing, lang)
		}
	}
	if len(missing) > 0 {
		sev := srl.SevWarning
		if l.cfg.RequireVerified {
			sev = srl.SevError
		}
		l.add(sev, pos, "LINT0002",
			"every citizen-facing string ships in every supported language",
			"%s has no %s in: %s", what, field, strings.Join(missing, ", "))
	}
}

func (l *linter) checkLegalBasis(lb srl.LegalBasis, pos srl.Pos, what string) {
	if lb.IsEmpty() {
		l.add(srl.SevError, pos, "LINT0003",
			`add legal_basis { act "..." section "..." }`,
			"%s cites no statutory basis", what)
	}
}

func (l *linter) checkGovernance(gv srl.Governance, pos srl.Pos, what string) {
	if strings.TrimSpace(gv.Owner) == "" {
		l.add(l.governanceSeverity(), pos, "LINT0004",
			"name the role accountable for this content, not a person",
			"%s has no named owner", what)
	}
	if gv.ReviewStatus != srl.StatusVerified {
		sev := srl.SevInfo
		if l.cfg.RequireVerified {
			sev = srl.SevError
		}
		l.add(sev, pos, "LINT0005",
			"content reaches production only after legal review and department sign-off",
			"%s is %s, not verified", what, gv.ReviewStatus)
	}
	// Staleness: content past its review date is the mechanism by which encoded
	// law silently drifts from real law (spec 15).
	if !gv.LastVerified.IsZero() && !gv.ReviewCadence.IsZero() && !l.cfg.Today.IsZero() {
		due := gv.LastVerified.AddPeriod(gv.ReviewCadence)
		if l.cfg.Today.After(due) {
			l.add(srl.SevWarning, pos, "LINT0006",
				"re-verify the content against the current statute and update last_verified",
				"%s was last verified on %s and was due for review on %s",
				what, gv.LastVerified, due)
		}
	}
}

func (l *linter) lintRules() {
	for _, id := range l.g.RuleIDs() {
		r := l.g.Rules[id]
		what := "rule " + id
		l.checkLegalBasis(r.LegalBasis, r.Pos, what)
		l.checkGovernance(r.Governance, r.Pos, what)

		// An explanation is what the applicant reads. Rules that produce a
		// citizen-visible obligation must have one (P5).
		switch r.Kind {
		case srl.RuleTrigger, srl.RuleExemption, srl.RuleDocument, srl.RuleObligation,
			srl.RuleIncentive:
			l.checkLangs(r.Explain, r.Pos, what, "explain")
		}

		// Positive and negative test coverage is a hard gate: spec 5.5.5 forbids
		// publishing a rule without both.
		var pos, neg int
		for _, t := range r.Tests {
			switch t.Expect.Fires {
			case value.True:
				pos++
			case value.False:
				neg++
			}
		}
		if pos == 0 {
			l.add(srl.SevError, r.Pos, "LINT0007",
				"add a test with `expect fires` showing when the rule applies",
				"%s has no positive test case", what)
		}
		if neg == 0 {
			l.add(srl.SevError, r.Pos, "LINT0008",
				"add a test with `expect not fires` showing when the rule does not apply",
				"%s has no negative test case", what)
		}

		// An exemption that does not outrank what it displaces never takes effect.
		for _, o := range r.Overrides {
			if other, ok := l.g.Rules[o]; ok && r.Precedence <= other.Precedence {
				l.add(srl.SevError, r.Pos, "LINT0009",
					fmt.Sprintf("raise this rule's precedence above %d", other.Precedence),
					"%s overrides %s but has precedence %d, which does not exceed %d",
					what, o, r.Precedence, other.Precedence)
			}
		}
	}
}

func (l *linter) lintApprovals() {
	for _, id := range l.g.ApprovalIDs() {
		a := l.g.Approvals[id]
		what := "approval " + id
		l.checkLangs(a.Title, a.Pos, what, "title")
		l.checkLegalBasis(a.LegalBasis, a.Pos, what)
		l.checkGovernance(a.Governance, a.Pos, what)

		if a.Department == "" {
			l.add(srl.SevError, a.Pos, "LINT0010", "",
				"%s names no department; no one would be accountable for it", what)
		}
		if a.SLA.IsZero() {
			l.add(srl.SevError, a.Pos, "LINT0011",
				"every approval has a statutory or notified timeline; record it",
				"%s has no SLA", what)
		}
		if a.Category == "" {
			l.add(srl.SevWarning, a.Pos, "LINT0012",
				"one of: licence, registration, consent, noc, permit, certificate, intimation",
				"%s has no category", what)
		}
		// Deemed approval without a citation is exactly the abuse spec 15 warns of.
		if a.Deemed.Permitted && strings.TrimSpace(a.Deemed.Section) == "" {
			l.add(srl.SevError, a.Deemed.Pos, "LINT0013",
				`cite the enabling provision: deemed_approval permitted section "6(3)"`,
				"%s permits deemed approval without citing the provision that allows it", what)
		}
		// Renewal cadence on a perpetual licence is a modelling mistake that
		// would generate obligations for a licence that never expires.
		if !a.RenewCadence.IsZero() && a.Validity.IsZero() {
			l.add(srl.SevWarning, a.Pos, "LINT0014",
				"set `validity` or remove the renewal cadence",
				"%s has a renewal cadence but no validity period", what)
		}
		if !a.Validity.IsZero() && a.RenewNotice.IsZero() {
			l.add(srl.SevWarning, a.Pos, "LINT0015",
				"set `renewal { notice ... }` so the Compliance Twin can warn in time",
				"%s expires but gives no renewal notice period", what)
		}
		if !l.requiredApprovals[id] && !l.dependedApprovals[id] {
			l.add(srl.SevError, a.Pos, "LINT0016",
				"either write a rule that requires it, or remove it",
				"%s can never be required: no rule triggers it and nothing depends on it", what)
		}
	}
}

func (l *linter) lintDocuments() {
	for _, id := range l.g.DocumentIDs() {
		d := l.g.Documents[id]
		what := "document " + id
		l.checkLangs(d.Title, d.Pos, what, "title")
		l.checkGovernance(d.Governance, d.Pos, what)
		if !l.usedDocuments[id] {
			l.add(srl.SevWarning, d.Pos, "LINT0020",
				"asking for a document no approval needs is a data-minimisation defect",
				"%s is never required by anything", what)
		}
		if d.Verification == srl.VerifySelfDeclared {
			l.add(srl.SevInfo, d.Pos, "LINT0021",
				"self-declared facts are flagged as unverified to every officer who sees them",
				"%s is self-declared and carries no verification", what)
		}
	}
}

func (l *linter) lintFees() {
	for _, id := range l.g.FeeIDs() {
		f := l.g.Fees[id]
		what := "fee " + id
		l.checkLangs(f.Title, f.Pos, what, "title")
		l.checkLegalBasis(f.LegalBasis, f.Pos, what)
		l.checkGovernance(f.Governance, f.Pos, what)
		if f.Head == "" {
			l.add(srl.SevError, f.Pos, "LINT0030",
				"the treasury head is how a consolidated payment is split to departments",
				"%s has no government accounting head", what)
		}
		if !l.leviedFees[id] {
			l.add(srl.SevWarning, f.Pos, "LINT0031", "",
				"%s is never levied on any approval", what)
		}
	}
}

func (l *linter) lintObligations() {
	for _, id := range l.g.ObligationIDs() {
		o := l.g.Obligations[id]
		what := "obligation " + id
		l.checkLangs(o.Title, o.Pos, what, "title")
		l.checkLegalBasis(o.LegalBasis, o.Pos, what)
		l.checkGovernance(o.Governance, o.Pos, what)
		l.checkLangs(o.Consequence, o.Pos, what, "consequence")
		if o.Cadence.IsZero() && o.DueWithin.IsZero() && o.DueMonth == 0 {
			l.add(srl.SevError, o.Pos, "LINT0040",
				"set `cadence`, `due month ... day ...`, or `due_within`",
				"%s has no schedule, so it can never appear on a compliance calendar", what)
		}
		if !l.createdObligations[id] {
			l.add(srl.SevWarning, o.Pos, "LINT0041", "",
				"%s is never created by any approval or rule", what)
		}
	}
}

func (l *linter) lintIncentives() {
	for _, id := range l.g.IncentiveIDs() {
		in := l.g.Incentives[id]
		what := "incentive " + id
		l.checkLangs(in.Title, in.Pos, what, "title")
		l.checkGovernance(in.Governance, in.Pos, what)
		if !l.offeredIncentives[id] {
			l.add(srl.SevWarning, in.Pos, "LINT0050",
				"write a rule with `offer incentive` so eligible businesses are told",
				"%s is never offered by any rule", what)
		}
		if in.Value == nil {
			l.add(srl.SevWarning, in.Pos, "LINT0051",
				"applicants need an estimate of what the scheme is worth",
				"%s states no entitlement value", what)
		}
	}
}

func (l *linter) lintDepartments() {
	for _, id := range l.g.DepartmentIDs() {
		d := l.g.Departments[id]
		what := "department " + id
		l.checkLangs(d.Name, d.Pos, what, "name")
		l.checkGovernance(d.Governance, d.Pos, what)
		if d.AdapterTier == "" {
			l.add(srl.SevWarning, d.Pos, "LINT0060",
				"even a department with no system integrates at the assisted_manual tier",
				"%s declares no adapter tier", what)
		}
	}
}

func (l *linter) lintProfile() {
	for _, name := range l.g.VarNames() {
		v := l.g.Vars[name]
		what := "profile variable " + name
		// The question is the applicant-facing text used when this variable
		// leaves a determination unresolved, so it must exist and be translated.
		l.checkLangs(v.Question, v.Pos, what, "question")
		if !l.readVars[name] {
			l.add(srl.SevWarning, v.Pos, "LINT0070",
				"asking a question no rule uses collects data for no purpose",
				"%s is never read by any rule, fee or incentive", what)
		}
	}
}
