package eval

import (
	"fmt"
	"sort"

	"github.com/setu-platform/setu/pkg/compile"
	"github.com/setu-platform/setu/pkg/srl"
	"github.com/setu-platform/setu/pkg/unit"
	"github.com/setu-platform/setu/pkg/value"
)

// Options control what an evaluation produces. They never change the answer,
// only how much evidence is retained alongside it.
type Options struct {
	// Trace retains the full derivation. The applicant console requests it when
	// a user expands "why?"; batch evaluation omits it for size.
	Trace bool
	// RulesetVersion and RulesetHash are stamped into the determination for
	// provenance. They come from the published snapshot, not from the engine.
	RulesetVersion string
	RulesetHash    string
}

// Engine evaluates profiles against one compiled ruleset.
//
// An Engine is immutable after construction and safe for concurrent use: it
// holds no per-evaluation state, which is what allows a single process to serve
// discovery for many applicants without any risk of one determination
// influencing another.
type Engine struct {
	prog *compile.Program

	// activeResults is set for the duration of one Evaluate call so that
	// dependency resolution can see which conditional edges are live. Evaluate
	// takes a copy of the engine for this reason, keeping the shared Engine
	// itself immutable and safe for concurrent use.
	activeResults map[string]*ruleResult
}

// New constructs an engine over a compiled program.
func New(prog *compile.Program) *Engine { return &Engine{prog: prog} }

// Program exposes the compiled ruleset, for analysis and tooling.
func (e *Engine) Program() *compile.Program { return e.prog }

// ruleResult is one rule's evaluation outcome.
type ruleResult struct {
	fired    value.Tri
	unknowns []string
	inForce  bool
	trace    []TraceNode
}

// Evaluate computes a determination. It never returns an error: an
// insufficient profile is a normal outcome reported through Unresolved, and a
// contradiction in the encoded law is reported through Conflicts. There is no
// input for which the engine has nothing to say.
func (e *Engine) Evaluate(in Input, opt Options) *Determination {
	g := e.prog.Graph
	if in.Profile == nil {
		in.Profile = NewProfile("")
	}
	if in.Language == "" {
		in.Language = "en"
	}

	d := &Determination{
		Package:        g.Package,
		RulesetVersion: opt.RulesetVersion,
		RulesetHash:    opt.RulesetHash,
		ProfileID:      in.Profile.ID,
		ProfileVersion: in.Profile.Version,
		AsOf:           in.AsOf,
		Language:       in.Language,
		statuses:       map[string]*ApprovalStatus{},
	}
	if in.Layers != nil {
		d.LayersVersion = in.Layers.Version
	}

	// Evaluate runs against a per-call copy so that concurrent evaluations never
	// share the transient state below.
	ev := &Engine{prog: e.prog, activeResults: map[string]*ruleResult{}}
	ctx := &evalCtx{in: &in, approvalStatus: d.statuses, trace: opt.Trace}
	results := ev.activeResults

	// One pass over the compiled order: evaluate each rule when reached, settle
	// each approval once every rule and dependency bearing on it has been seen.
	for _, node := range e.prog.Order {
		switch node.Kind {
		case compile.NodeRule:
			results[node.ID] = ev.evalRule(ctx, node.ID, in, opt, d)
		case compile.NodeApproval:
			ev.settleApproval(node.ID, in, results, d)
		}
	}

	ev.buildOutputs(ctx, in, opt, results, d)
	d.computeHash()
	return d
}

// evalRule evaluates one rule's condition and records its trace.
func (e *Engine) evalRule(ctx *evalCtx, id string, in Input, opt Options,
	d *Determination) *ruleResult {

	r := e.prog.Graph.Rules[id]
	rr := &ruleResult{inForce: r.Valid.Covers(in.AsOf)}

	// A rule that was not in force on the anchor date does not apply, however
	// perfectly the profile matches it. This is the mechanism by which a case
	// submitted last year is still judged by last year's law (P6).
	if !rr.inForce {
		rr.fired = value.False
		if opt.Trace {
			d.Trace = append(d.Trace, RuleTrace{
				RuleID: id, Title: r.Title.Get(in.Language), Kind: r.Kind.String(),
				Precedence: r.Precedence, Fired: "false", InForce: false,
				Condition:  srl.FormatExpr(r.When),
				LegalBasis: r.LegalBasis.Cite(),
			})
		}
		return rr
	}

	out := ctx.evalExpr(r.When)
	rr.fired = out.v.Truthy()
	rr.unknowns = sortedUnknowns(out.unknowns)
	rr.trace = out.children

	if opt.Trace {
		var effects []string
		for _, ef := range r.Then {
			effects = append(effects, describeEffect(ef))
		}
		d.Trace = append(d.Trace, RuleTrace{
			RuleID: id, Title: r.Title.Get(in.Language), Kind: r.Kind.String(),
			Precedence: r.Precedence, Fired: rr.fired.String(), InForce: true,
			Condition:  srl.FormatExpr(r.When),
			LegalBasis: r.LegalBasis.Cite(),
			Unresolved: rr.unknowns,
			Detail:     out.children,
			Effects:    effects,
		})
	}
	return rr
}

func describeEffect(e srl.Effect) string {
	switch e.Kind {
	case srl.EffectRequireDocument, srl.EffectLevyFee, srl.EffectWaiveFee:
		return fmt.Sprintf("%s %s on %s", e.Kind, e.Target, e.On)
	case srl.EffectCreateObligation:
		return fmt.Sprintf("%s %s from %s", e.Kind, e.Target, e.On)
	case srl.EffectDependency:
		return fmt.Sprintf("%s %s on %s", e.Kind, e.On, e.Target)
	case srl.EffectFlag:
		return fmt.Sprintf("flag %q", e.Flag)
	}
	return fmt.Sprintf("%s %s", e.Kind, e.Target)
}

// settleApproval decides whether one approval is required.
//
// Resolution is by precedence, and it is total: for any set of fired rules
// there is exactly one outcome, or an explicit conflict. Nothing is decided by
// rule ordering, file ordering or map iteration.
//
// The subtle part is what an undetermined rule does to a determined one. A rule
// that fired at precedence 100 does not settle the question if a rule at
// precedence 200 that would exempt the applicant is still undetermined — the
// answer genuinely depends on a question nobody has asked yet. Reporting
// "required" there would be the engine guessing, which P1 forbids and which the
// monotonicity property in the test suite exists to catch.
func (e *Engine) settleApproval(id string, in Input, results map[string]*ruleResult,
	d *Determination) {

	a := e.prog.Graph.Approvals[id]
	st := &ApprovalStatus{ID: id, Required: value.False}
	d.statuses[id] = st

	// An approval type not in force on the anchor date cannot be required.
	if a != nil && !a.Valid.Covers(in.AsOf) {
		st.RequiredText = st.Required.String()
		return
	}

	type effect struct {
		ruleID   string
		prec     int
		exempt   bool
		unknowns []string
	}
	var definite, pending []effect

	for _, rid := range e.prog.ApprovalRules[id] {
		r := e.prog.Graph.Rules[rid]
		rr := results[rid]
		if rr == nil {
			continue
		}
		for _, ef := range r.Then {
			if ef.Target != id {
				continue
			}
			isExempt := ef.Kind == srl.EffectExemptApproval
			isRequire := ef.Kind == srl.EffectRequireApproval
			if !isExempt && !isRequire {
				continue
			}
			kind := "require"
			if isExempt {
				kind = "exempt"
			}
			st.Contributions = append(st.Contributions, Contribution{
				RuleID: rid, Effect: kind, Precedence: r.Precedence,
				Fired: rr.fired.String(),
			})
			e := effect{ruleID: rid, prec: r.Precedence, exempt: isExempt, unknowns: rr.unknowns}
			switch rr.fired {
			case value.True:
				definite = append(definite, e)
			case value.Unknown:
				pending = append(pending, e)
			}
		}
	}

	sort.SliceStable(st.Contributions, func(i, j int) bool {
		if st.Contributions[i].Precedence != st.Contributions[j].Precedence {
			return st.Contributions[i].Precedence > st.Contributions[j].Precedence
		}
		return st.Contributions[i].RuleID < st.Contributions[j].RuleID
	})

	if len(definite) > 0 {
		maxPrec := definite[0].prec
		for _, ef := range definite[1:] {
			if ef.prec > maxPrec {
				maxPrec = ef.prec
			}
		}
		var requires, exempts []string
		for _, ef := range definite {
			if ef.prec != maxPrec {
				continue
			}
			if ef.exempt {
				exempts = append(exempts, ef.ruleID)
			} else {
				requires = append(requires, ef.ruleID)
			}
		}
		sort.Strings(requires)
		sort.Strings(exempts)

		for i := range st.Contributions {
			if st.Contributions[i].Precedence < maxPrec && st.Contributions[i].Fired == "true" {
				st.Contributions[i].Overridden = true
			}
		}

		if len(requires) > 0 && len(exempts) > 0 {
			// Two provisions of equal authority say opposite things about this
			// applicant. Guessing either way would be a legal error, so the
			// determination is marked undetermined and routed to a human (P12).
			d.Conflicts = append(d.Conflicts, Conflict{
				ApprovalID: id, Precedence: maxPrec,
				RequireRules: requires, ExemptRules: exempts,
				Resolution: "undetermined: rules of equal precedence conflict; " +
					"referred for departmental clarification",
			})
			st.Required = value.Unknown
			st.Precedence = maxPrec
			st.RequiredText = st.Required.String()
			return
		}

		decisionExempt := len(exempts) > 0

		// A pending rule can only change this outcome if it outranks or matches
		// the deciding precedence AND pulls the other way. Anything below is
		// outranked whatever it turns out to be, and anything agreeing changes
		// nothing, so neither is a reason to withhold an answer.
		unk := map[string]bool{}
		var blockers []string
		for _, p := range pending {
			if p.prec < maxPrec || p.exempt == decisionExempt {
				continue
			}
			blockers = append(blockers, p.ruleID)
			for _, v := range p.unknowns {
				unk[v] = true
			}
		}
		if len(blockers) > 0 {
			sort.Strings(blockers)
			st.Required = value.Unknown
			st.Precedence = maxPrec
			st.UnresolvedVars = sortedUnknowns(unk)
			st.BlockedBy = blockers
			st.RequiredText = st.Required.String()
			return
		}

		if decisionExempt {
			st.Required = value.False
			st.Exempted = true
			st.DecidedBy = exempts[0]
		} else {
			st.Required = value.True
			st.DecidedBy = requires[0]
		}
		st.Precedence = maxPrec
		st.RequiredText = st.Required.String()
		return
	}

	// No rule fired definitely. If any rule that could require this approval is
	// undetermined, so is the approval, and the questions that would settle it
	// are carried forward verbatim. Pending exemptions alone change nothing:
	// with nothing requiring it, the approval is not required either way.
	unk := map[string]bool{}
	var blockers []string
	for _, p := range pending {
		if p.exempt {
			continue
		}
		blockers = append(blockers, p.ruleID)
		for _, v := range p.unknowns {
			unk[v] = true
		}
	}
	if len(blockers) > 0 {
		sort.Strings(blockers)
		st.Required = value.Unknown
		st.UnresolvedVars = sortedUnknowns(unk)
		st.BlockedBy = blockers
		st.RequiredText = st.Required.String()
		e.applyDependency(id, st, d)
		return
	}

	e.applyDependency(id, st, d)
	st.RequiredText = st.Required.String()
}

// applyDependency propagates requirement down the prerequisite edges.
//
// An approval that another required approval depends on is itself required:
// there is no way to obtain a fire NOC that depends on a building plan approval
// without obtaining the building plan approval. The propagation carries the
// lowest authority in the system, so an explicit statutory exemption always
// outranks it.
// activeDependents lists the approvals that need id as a prerequisite under
// this profile: the edges declared on the approvals themselves, plus the edges
// created by rules that actually fired.
func (e *Engine) activeDependents(id string, results map[string]*ruleResult) []string {
	var out []string
	for _, edge := range e.prog.EdgesByPrereq[id] {
		if edge.Conditional() {
			rr := results[edge.RuleID]
			if rr == nil || !rr.inForce || rr.fired != value.True {
				continue
			}
		}
		out = append(out, edge.Dependent)
	}
	sort.Strings(out)
	return dedupeSorted(out)
}

// activePrereqs is the mirror: the prerequisites id actually has here.
func (e *Engine) activePrereqs(id string, results map[string]*ruleResult) []string {
	var out []string
	for _, edge := range e.prog.EdgesByDependent[id] {
		if edge.Conditional() {
			rr := results[edge.RuleID]
			if rr == nil || !rr.inForce || rr.fired != value.True {
				continue
			}
		}
		out = append(out, edge.Prereq)
	}
	sort.Strings(out)
	return dedupeSorted(out)
}

func dedupeSorted(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	out := s[:1]
	for _, v := range s[1:] {
		if v != out[len(out)-1] {
			out = append(out, v)
		}
	}
	return out
}

func (e *Engine) applyDependency(id string, st *ApprovalStatus, d *Determination) {
	if st.Exempted || st.Required == value.True {
		return
	}
	// If a rule that could exempt this approval is still undetermined, a
	// dependency cannot settle it either: an exemption outranks the propagation.
	for _, rid := range st.BlockedBy {
		if r, ok := e.prog.Graph.Rules[rid]; ok {
			for _, ef := range r.Then {
				if ef.Target == id && ef.Kind == srl.EffectExemptApproval {
					return
				}
			}
		}
	}
	dependents := e.activeDependents(id, e.activeResults)
	for _, dep := range dependents {
		ds, ok := d.statuses[dep]
		if !ok {
			continue
		}
		if ds.Required == value.True {
			st.Required = value.True
			st.ByDependency = dep
			st.UnresolvedVars = nil
			st.RequiredText = st.Required.String()
			return
		}
	}
	if st.Required == value.Unknown {
		return
	}
	for _, dep := range dependents {
		ds, ok := d.statuses[dep]
		if !ok {
			continue
		}
		if ds.Required == value.Unknown {
			st.Required = value.Unknown
			st.ByDependency = dep
			st.UnresolvedVars = mergeStrings(st.UnresolvedVars, ds.UnresolvedVars)
			st.RequiredText = st.Required.String()
			return
		}
	}
}

func mergeStrings(a, b []string) []string {
	m := map[string]bool{}
	for _, s := range append(append([]string{}, a...), b...) {
		m[s] = true
	}
	return sortedUnknowns(m)
}

// EvaluateProfile is a convenience for callers that only need the approvals.
func (e *Engine) EvaluateProfile(p *Profile, asOf value.Date) *Determination {
	return e.Evaluate(Input{Profile: p, AsOf: asOf, Language: "en"}, Options{})
}

// varType renders a profile variable's type for an unresolved question.
func (e *Engine) varType(name string) string {
	if t, ok := e.prog.VarTypes[name]; ok {
		return t.String()
	}
	return ""
}

// unitFor picks the display unit for a quantity question.
func (e *Engine) unitFor(name string) (unit.Unit, bool) {
	v, ok := e.prog.Graph.Vars[name]
	if !ok || v.Unit == "" {
		return unit.None, false
	}
	return unit.Lookup(v.Unit)
}
