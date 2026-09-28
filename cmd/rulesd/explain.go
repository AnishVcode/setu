package main

import (
	"sort"
	"strings"

	"github.com/setu-platform/setu/pkg/eval"
	"github.com/setu-platform/setu/pkg/srl"
	"github.com/setu-platform/setu/pkg/unit"
	"github.com/setu-platform/setu/pkg/value"
)

// answerShown is one thing the applicant told us that fed a decision.
type answerShown struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

// explanation is the plain-language account of why one approval applies.
//
// The console shows this instead of the rule's condition. A citizen is owed the
// reason for a legal obligation (P5), but the reason they are owed is "because
// you told us you will employ 25 workers and use power-driven machinery" — not
// the boolean expression that encodes it. The expression is still available to
// an officer or an author through `setuctl explain`; it is simply not the right
// artefact to put in front of an applicant.
type explanation struct {
	Because      []answerShown `json:"because"`
	Act          string        `json:"act,omitempty"`
	Provision    string        `json:"provision,omitempty"`
	Notified     string        `json:"notified,omitempty"`
	Prerequisite string        `json:"prerequisite,omitempty"`
}

// buildExplanations derives, for every required approval, the answers that
// actually produced it.
func (s *server) buildExplanations(d *eval.Determination, p *eval.Profile,
	lang string) map[string]explanation {

	out := map[string]explanation{}
	for _, a := range d.Approvals {
		ex := explanation{Because: []answerShown{}}

		// An approval required only because something else needs it first is
		// explained by that, not by a rule of its own.
		if a.ByDependency != "" {
			if dep, ok := s.graph.Approvals[a.ByDependency]; ok {
				ex.Prerequisite = dep.Title.Get(lang)
			}
		}

		if r, ok := s.graph.Rules[a.ReasonRuleID]; ok {
			ex.Act = r.LegalBasis.Act
			ex.Provision = provisionOf(r.LegalBasis)
			if r.LegalBasis.Notification != "" {
				ex.Notified = r.LegalBasis.Notification
				if !r.LegalBasis.Dated.IsZero() {
					ex.Notified += " dated " + r.LegalBasis.Dated.String()
				}
			}
			for _, name := range s.prog.RuleReadsVars[a.ReasonRuleID] {
				v := p.Get(name)
				if v.IsUnknown() {
					continue
				}
				decl, ok := s.graph.Vars[name]
				if !ok {
					continue
				}
				ex.Because = append(ex.Because, answerShown{
					Question: decl.Question.Get(lang),
					Answer:   s.showAnswer(name, v, lang),
				})
			}
		}

		// Fall back to the approval's own citation when the deciding rule has
		// none of its own, so a required approval is never shown uncited.
		if ex.Act == "" {
			if decl, ok := s.graph.Approvals[a.ID]; ok {
				ex.Act = decl.LegalBasis.Act
				ex.Provision = provisionOf(decl.LegalBasis)
			}
		}
		out[a.ID] = ex
	}
	return out
}

// provisionOf renders the section or rule of a citation, whichever it carries.
func provisionOf(lb srl.LegalBasis) string {
	var parts []string
	if lb.Section != "" {
		parts = append(parts, "Section "+lb.Section)
	}
	if lb.Rule != "" {
		parts = append(parts, lb.Rule)
	}
	return strings.Join(parts, ", ")
}

// showAnswer renders one profile answer the way it was asked for: yes or no for
// a boolean, the member's own title for an enum, and a quantity in the unit the
// question stated it in.
func (s *server) showAnswer(name string, v value.Value, lang string) string {
	switch v.Kind {
	case value.KindBool:
		if v.Bool {
			return "Yes"
		}
		return "No"
	case value.KindEnum:
		if m, ok := s.graph.EnumMember(v.EnumTy, v.Enum); ok {
			if t := m.Title.Get(lang); t != "" {
				return t
			}
		}
		return v.Enum
	case value.KindGeo:
		return "the site you marked on the map"
	case value.KindNumber:
		if decl, ok := s.graph.Vars[name]; ok && decl.Unit != "" {
			if u, ok := unit.Lookup(decl.Unit); ok {
				if conv, ok := v.In(u); ok {
					return conv.String()
				}
			}
		}
	}
	return v.String()
}

// buildTitles maps every identifier the determination mentions to a name a
// person would recognise, so the console never has to display one.
func (s *server) buildTitles(lang string) map[string]string {
	t := map[string]string{}
	for _, id := range s.graph.ApprovalIDs() {
		t[id] = s.graph.Approvals[id].Title.Get(lang)
	}
	for _, id := range s.graph.DocumentIDs() {
		t[id] = s.graph.Documents[id].Title.Get(lang)
	}
	for _, id := range s.graph.FeeIDs() {
		t[id] = s.graph.Fees[id].Title.Get(lang)
	}
	for _, id := range s.graph.ObligationIDs() {
		t[id] = s.graph.Obligations[id].Title.Get(lang)
	}
	for _, id := range s.graph.IncentiveIDs() {
		t[id] = s.graph.Incentives[id].Title.Get(lang)
	}
	for _, id := range s.graph.DepartmentIDs() {
		t[id] = s.graph.Departments[id].Name.Get(lang)
	}
	return t
}

// departmentsInvolved lists, by name, every authority the applicant will deal
// with and what each one is deciding.
type departmentBlock struct {
	Name      string   `json:"name"`
	Approvals []string `json:"approvals"`
}

func (s *server) departmentsInvolved(d *eval.Determination, lang string) []departmentBlock {
	byDept := map[string][]string{}
	for _, a := range d.Approvals {
		name := a.DepartmentName
		if name == "" {
			name = a.Department
		}
		byDept[name] = append(byDept[name], a.Title)
	}
	names := make([]string, 0, len(byDept))
	for n := range byDept {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]departmentBlock, 0, len(names))
	for _, n := range names {
		sort.Strings(byDept[n])
		out = append(out, departmentBlock{Name: n, Approvals: byDept[n]})
	}
	return out
}
