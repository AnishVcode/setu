package eval

import (
	"math/big"
	"sort"
	"strings"

	"github.com/setu-platform/setu/pkg/srl"
	"github.com/setu-platform/setu/pkg/value"
)

// buildOutputs assembles the determination from the settled approval statuses
// and the rules that fired. Everything it produces is sorted, so two runs of
// the same input yield identical bytes.
func (e *Engine) buildOutputs(ctx *evalCtx, in Input, opt Options,
	results map[string]*ruleResult, d *Determination) {

	g := e.prog.Graph
	lang := in.Language

	// Which approvals are actually required.
	required := map[string]bool{}
	for _, id := range g.ApprovalIDs() {
		if st, ok := d.statuses[id]; ok && st.Required == value.True {
			required[id] = true
		}
	}

	// Rule-added attachments, gathered only from rules that definitely fired.
	docsByApproval := map[string]map[string]bool{}
	feesByApproval := map[string]map[string]bool{}
	waived := map[string]bool{} // "feeID|approvalID"
	obligByApproval := map[string]map[string]bool{}
	incentiveRule := map[string]string{}
	flags := map[string]bool{}

	addTo := func(m map[string]map[string]bool, approval, item string) {
		if m[approval] == nil {
			m[approval] = map[string]bool{}
		}
		m[approval][item] = true
	}

	for _, rid := range g.RuleIDs() {
		rr := results[rid]
		if rr == nil || !rr.inForce || rr.fired != value.True {
			continue
		}
		for _, ef := range g.Rules[rid].Then {
			switch ef.Kind {
			case srl.EffectRequireDocument:
				addTo(docsByApproval, ef.On, ef.Target)
			case srl.EffectLevyFee:
				addTo(feesByApproval, ef.On, ef.Target)
			case srl.EffectWaiveFee:
				waived[ef.Target+"|"+ef.On] = true
			case srl.EffectCreateObligation:
				addTo(obligByApproval, ef.On, ef.Target)
			case srl.EffectOfferIncentive:
				if _, seen := incentiveRule[ef.Target]; !seen {
					incentiveRule[ef.Target] = rid
				}
			case srl.EffectFlag:
				flags[ef.Flag] = true
			}
		}
	}
	d.Flags = sortedUnknowns(flags)

	// Declared attachments on each required approval.
	for id := range required {
		a := g.Approvals[id]
		if a == nil {
			continue
		}
		for _, doc := range a.Documents {
			addTo(docsByApproval, id, doc)
		}
		for _, f := range a.Fees {
			addTo(feesByApproval, id, f)
		}
		for _, o := range a.Obligations {
			addTo(obligByApproval, id, o)
		}
	}

	e.buildApprovals(in, results, required, docsByApproval, feesByApproval, obligByApproval, d)
	e.buildDocuments(lang, required, docsByApproval, d)
	e.buildFees(ctx, lang, required, feesByApproval, waived, d)
	e.buildObligations(lang, required, obligByApproval, d)
	e.buildIncentives(ctx, in, results, incentiveRule, d)
	e.buildDAG(lang, results, required, d)
	e.buildUnresolved(in, d)
}

func (e *Engine) buildApprovals(in Input, results map[string]*ruleResult,
	required map[string]bool, docs, fees, oblig map[string]map[string]bool,
	d *Determination) {

	g := e.prog.Graph
	lang := in.Language
	for _, id := range g.ApprovalIDs() {
		if !required[id] {
			continue
		}
		a := g.Approvals[id]
		st := d.statuses[id]

		out := ApplicableApproval{
			ID:              id,
			Title:           a.Title.Get(lang),
			Summary:         a.Summary.Get(lang),
			Category:        a.Category,
			Department:      a.Department,
			LegalBasis:      a.LegalBasis.Cite(),
			SLADays:         a.SLA.ApproxDays(),
			SLA:             a.SLA.String(),
			DeemedPermitted: a.Deemed.Permitted,
			DeemedSection:   a.Deemed.Section,
			ByDependency:    st.ByDependency,
			Documents:       sortedUnknowns(docs[id]),
			Fees:            sortedUnknowns(fees[id]),
			Obligations:     sortedUnknowns(oblig[id]),
		}
		if dep, ok := g.Departments[a.Department]; ok {
			out.DepartmentName = dep.Name.Get(lang)
		}
		if !a.Validity.IsZero() {
			out.Validity = a.Validity.String()
		}
		if !a.RenewNotice.IsZero() {
			out.RenewalIn = a.RenewNotice.String()
		}
		if a.Appeal != nil {
			out.AppealAuthority = a.Appeal.Authority.Get(lang)
			if !a.Appeal.Within.IsZero() {
				out.AppealWithin = a.Appeal.Within.String()
			}
		}
		for _, prereq := range e.activePrereqs(id, results) {
			if !required[prereq] {
				continue
			}
			dep := Dependency{ID: prereq}
			if pa, ok := g.Approvals[prereq]; ok {
				dep.Title = pa.Title.Get(lang)
			}
			dep.Reason = e.prog.DependencyReasonFor(id, prereq).Get(lang)
			out.DependsOn = append(out.DependsOn, dep)
		}
		sort.SliceStable(out.DependsOn, func(i, j int) bool {
			return out.DependsOn[i].ID < out.DependsOn[j].ID
		})

		out.Reason, out.ReasonRuleID = e.explain(in, id, st, a)
		d.Approvals = append(d.Approvals, out)
	}
	sort.SliceStable(d.Approvals, func(i, j int) bool { return d.Approvals[i].ID < d.Approvals[j].ID })
}

// explain renders the applicant-facing reason for one approval.
func (e *Engine) explain(in Input, id string, st *ApprovalStatus,
	a *srl.ApprovalDecl) (string, string) {

	lang := in.Language
	if st.ByDependency != "" {
		title := st.ByDependency
		if da, ok := e.prog.Graph.Approvals[st.ByDependency]; ok {
			title = da.Title.Get(lang)
		}
		reason := e.prog.DependencyReasonFor(st.ByDependency, id).Get(lang)
		if reason != "" {
			return reason, ""
		}
		return "This is a prerequisite for " + title + ", which you need.", ""
	}
	if st.DecidedBy == "" {
		return "", ""
	}
	r := e.prog.Graph.Rules[st.DecidedBy]
	if r == nil {
		return "", ""
	}
	// An effect-level reason overrides the rule's general explanation, so a
	// rule that requires several approvals can say something specific about each.
	for _, ef := range r.Then {
		if ef.Target == id && len(ef.Reason) > 0 {
			return e.renderTemplate(ef.Reason.Get(lang), in, a, r), st.DecidedBy
		}
	}
	return e.renderTemplate(r.Explain.Get(lang), in, a, r), st.DecidedBy
}

// renderTemplate interpolates {profile.x}, {approval.title}, {legal_basis} and
// {rule.id} into an explanation. Placeholders are validated at compile time, so
// an unresolved one here means the profile simply has no answer yet.
func (e *Engine) renderTemplate(tmpl string, in Input, a *srl.ApprovalDecl,
	r *srl.RuleDecl) string {

	if tmpl == "" {
		return ""
	}
	var b strings.Builder
	for i := 0; i < len(tmpl); i++ {
		if tmpl[i] == '\\' && i+1 < len(tmpl) && (tmpl[i+1] == '{' || tmpl[i+1] == '}') {
			b.WriteByte(tmpl[i+1])
			i++
			continue
		}
		if tmpl[i] != '{' {
			b.WriteByte(tmpl[i])
			continue
		}
		j := strings.IndexByte(tmpl[i:], '}')
		if j < 0 {
			b.WriteString(tmpl[i:])
			break
		}
		key := tmpl[i+1 : i+j]
		b.WriteString(e.resolvePlaceholder(key, in, a, r))
		i += j
	}
	return b.String()
}

func (e *Engine) resolvePlaceholder(key string, in Input, a *srl.ApprovalDecl,
	r *srl.RuleDecl) string {

	switch {
	case strings.HasPrefix(key, "profile."):
		name := strings.TrimPrefix(key, "profile.")
		v := in.Profile.Get(name)
		if v.IsUnknown() {
			return "(not stated)"
		}
		// Render in the unit the profile declares, so the explanation and the
		// threshold the rule tested are stated in the same terms.
		if u, ok := e.unitFor(name); ok {
			if conv, ok := v.In(u); ok {
				return conv.String()
			}
		}
		return v.String()
	case key == "approval.title":
		if a != nil {
			return a.Title.Get(in.Language)
		}
	case key == "legal_basis":
		if r != nil {
			return r.LegalBasis.Cite()
		}
	case key == "rule.id":
		if r != nil {
			return r.ID
		}
	}
	return "{" + key + "}"
}

func (e *Engine) buildDocuments(lang string, required map[string]bool,
	docs map[string]map[string]bool, d *Determination) {

	g := e.prog.Graph
	byDoc := map[string][]string{}
	for approval, set := range docs {
		if !required[approval] {
			continue
		}
		for doc := range set {
			byDoc[doc] = append(byDoc[doc], approval)
		}
	}
	ids := make([]string, 0, len(byDoc))
	for id := range byDoc {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		decl, ok := g.Documents[id]
		if !ok {
			continue
		}
		approvals := byDoc[id]
		sort.Strings(approvals)
		req := DocumentRequirement{
			ID:           id,
			Title:        decl.Title.Get(lang),
			Issuer:       decl.Issuer,
			Verification: decl.Verification.String(),
			SourceSystem: decl.SourceSystem,
			Credential:   decl.Credential,
			Attributes:   append([]string(nil), decl.Attributes...),
			RequiredFor:  approvals,
		}
		if !decl.MaxAge.IsZero() {
			req.MaxAge = decl.MaxAge.String()
		}
		d.Documents = append(d.Documents, req)
	}
}

func (e *Engine) buildFees(ctx *evalCtx, lang string, required map[string]bool,
	fees map[string]map[string]bool, waived map[string]bool, d *Determination) {

	g := e.prog.Graph
	total := new(big.Rat)
	complete := true

	type pair struct{ fee, approval string }
	var pairs []pair
	for approval, set := range fees {
		if !required[approval] {
			continue
		}
		for fee := range set {
			if waived[fee+"|"+approval] {
				continue
			}
			pairs = append(pairs, pair{fee: fee, approval: approval})
		}
	}
	sort.SliceStable(pairs, func(i, j int) bool {
		if pairs[i].approval != pairs[j].approval {
			return pairs[i].approval < pairs[j].approval
		}
		return pairs[i].fee < pairs[j].fee
	})

	for _, p := range pairs {
		decl, ok := g.Fees[p.fee]
		if !ok {
			continue
		}
		line := FeeLine{
			FeeID: p.fee, Title: decl.Title.Get(lang), ApprovalID: p.approval,
			Department: decl.Department, Head: decl.Head,
			LegalBasis: decl.LegalBasis.Cite(),
		}
		r := ctx.evalExpr(decl.Amount)
		if r.v.IsUnknown() || !r.v.Numeric() {
			line.Computed = false
			line.Amount = "not yet computable"
			line.Unresolved = sortedUnknowns(r.unknowns)
			complete = false
		} else {
			amt := applyRounding(r.v.Num, decl.Rounding)
			line.Computed = true
			line.Amount = value.Money(amt).String()
			line.AmountRaw = amt.RatString()
			total.Add(total, amt)
		}
		d.Fees = append(d.Fees, line)
	}
	sort.SliceStable(d.Fees, func(i, j int) bool {
		if d.Fees[i].ApprovalID != d.Fees[j].ApprovalID {
			return d.Fees[i].ApprovalID < d.Fees[j].ApprovalID
		}
		return d.Fees[i].FeeID < d.Fees[j].FeeID
	})
	d.FeeTotal = value.Money(total).String()
	d.FeeComplete = complete
}

// applyRounding reduces an exact computed amount to a payable one.
func applyRounding(amount *big.Rat, r srl.Rounding) *big.Rat {
	if r.Mode == "" || r.Mode == "none" || r.To == nil || r.To.Sign() == 0 {
		return new(big.Rat).Set(amount)
	}
	q := new(big.Rat).Quo(amount, r.To)
	var mode string
	switch r.Mode {
	case "up":
		mode = "ceil"
	case "down":
		mode = "floor"
	default:
		mode = "nearest"
	}
	return new(big.Rat).Mul(roundRat(q, mode), r.To)
}

func (e *Engine) buildObligations(lang string, required map[string]bool,
	oblig map[string]map[string]bool, d *Determination) {

	g := e.prog.Graph
	type pair struct{ approval, obligation string }
	var pairs []pair
	for approval, set := range oblig {
		if !required[approval] {
			continue
		}
		for o := range set {
			pairs = append(pairs, pair{approval: approval, obligation: o})
		}
	}
	sort.SliceStable(pairs, func(i, j int) bool {
		if pairs[i].approval != pairs[j].approval {
			return pairs[i].approval < pairs[j].approval
		}
		return pairs[i].obligation < pairs[j].obligation
	})

	for _, p := range pairs {
		decl, ok := g.Obligations[p.obligation]
		if !ok {
			continue
		}
		inst := ObligationInstance{
			ID: p.obligation, Title: decl.Title.Get(lang), ApprovalID: p.approval,
			Department: decl.Department, DueMonth: decl.DueMonth, DueDay: decl.DueDay,
			Evidence:    append([]string(nil), decl.Evidence...),
			Consequence: decl.Consequence.Get(lang),
			LegalBasis:  decl.LegalBasis.Cite(),
		}
		if !decl.Cadence.IsZero() {
			inst.Cadence = decl.Cadence.String()
		}
		if !decl.DueWithin.IsZero() {
			inst.DueWithin = decl.DueWithin.String()
		}
		d.Obligations = append(d.Obligations, inst)
	}
}

func (e *Engine) buildIncentives(ctx *evalCtx, in Input, results map[string]*ruleResult,
	incentiveRule map[string]string, d *Determination) {

	g := e.prog.Graph
	ids := make([]string, 0, len(incentiveRule))
	for id := range incentiveRule {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		decl, ok := g.Incentives[id]
		if !ok {
			continue
		}
		// A scheme outside its application window is not offered, however well
		// the applicant qualifies.
		if !decl.Window.From.IsZero() || !decl.Window.Until.IsZero() {
			if !decl.Window.Covers(in.AsOf) {
				continue
			}
		}
		rid := incentiveRule[id]
		m := IncentiveMatch{
			ID: id, Title: decl.Title.Get(in.Language), Scheme: decl.Scheme,
			Department: decl.Department, Evidence: append([]string(nil), decl.Evidence...),
			ApplyURL: decl.ApplyURL, RuleID: rid, LegalBasis: decl.LegalBasis.Cite(),
		}
		if !decl.Window.From.IsZero() {
			m.WindowFrom = decl.Window.From.String()
		}
		if !decl.Window.Until.IsZero() {
			m.WindowTo = decl.Window.Until.String()
		}
		if r, ok := g.Rules[rid]; ok {
			m.Reason = e.renderTemplate(r.Explain.Get(in.Language), in, nil, r)
		}
		if decl.Value != nil {
			res := ctx.evalExpr(decl.Value)
			if res.v.IsUnknown() || !res.v.Numeric() {
				m.Computed = false
				m.Unresolved = sortedUnknowns(res.unknowns)
			} else {
				m.Computed = true
				m.Value = value.Money(res.v.Num).String()
			}
		}
		d.Incentives = append(d.Incentives, m)
	}
}

// buildDAG lays out the required approvals as a dependency graph and computes
// the critical path: the chain that actually determines the finish date.
func (e *Engine) buildDAG(lang string, results map[string]*ruleResult,
	required map[string]bool, d *Determination) {
	g := e.prog.Graph
	var ids []string
	for id := range required {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	slaOf := func(id string) int {
		if a, ok := g.Approvals[id]; ok {
			return a.SLA.ApproxDays()
		}
		return 0
	}

	// Edges among required approvals only: a prerequisite that is not itself
	// required cannot be on the path.
	prereqs := map[string][]string{}
	for _, id := range ids {
		for _, p := range e.activePrereqs(id, results) {
			if required[p] {
				prereqs[id] = append(prereqs[id], p)
				reason := e.prog.DependencyReasonFor(id, p).Get(lang)
				d.DAG.Edges = append(d.DAG.Edges, DAGEdge{From: p, To: id, Reason: reason})
			}
		}
		sort.Strings(prereqs[id])
	}
	sort.SliceStable(d.DAG.Edges, func(i, j int) bool {
		if d.DAG.Edges[i].From != d.DAG.Edges[j].From {
			return d.DAG.Edges[i].From < d.DAG.Edges[j].From
		}
		return d.DAG.Edges[i].To < d.DAG.Edges[j].To
	})

	// Earliest start and finish, assuming every independent approval is
	// dispatched at once and each takes its full statutory SLA.
	start := map[string]int{}
	finish := map[string]int{}
	pred := map[string]string{}
	var visit func(id string) int
	visiting := map[string]bool{}
	visit = func(id string) int {
		if f, ok := finish[id]; ok {
			return f
		}
		if visiting[id] {
			return 0 // compilation guarantees acyclicity; this is belt and braces
		}
		visiting[id] = true
		best, bestFrom := 0, ""
		for _, p := range prereqs[id] {
			if f := visit(p); f > best {
				best, bestFrom = f, p
			}
		}
		visiting[id] = false
		start[id] = best
		pred[id] = bestFrom
		finish[id] = best + slaOf(id)
		return finish[id]
	}
	for _, id := range ids {
		visit(id)
	}

	// Waves: approvals that can be dispatched together.
	waveOf := map[string]int{}
	var waveIdx func(id string) int
	waveIdx = func(id string) int {
		if w, ok := waveOf[id]; ok {
			return w
		}
		w := 0
		for _, p := range prereqs[id] {
			if pw := waveIdx(p) + 1; pw > w {
				w = pw
			}
		}
		waveOf[id] = w
		return w
	}
	maxWave := 0
	for _, id := range ids {
		if w := waveIdx(id); w > maxWave {
			maxWave = w
		}
	}
	d.DAG.Waves = make([][]string, maxWave+1)
	for _, id := range ids {
		w := waveOf[id]
		d.DAG.Waves[w] = append(d.DAG.Waves[w], id)
	}
	for i := range d.DAG.Waves {
		sort.Strings(d.DAG.Waves[i])
	}

	// The critical path ends at the approval that finishes last; ties break on
	// identifier so the path shown is stable.
	last, lastFinish := "", -1
	for _, id := range ids {
		if finish[id] > lastFinish || (finish[id] == lastFinish && id < last) {
			last, lastFinish = id, finish[id]
		}
	}
	critical := map[string]bool{}
	for cur := last; cur != ""; cur = pred[cur] {
		critical[cur] = true
		d.DAG.CriticalPath = append([]string{cur}, d.DAG.CriticalPath...)
		if pred[cur] == "" {
			break
		}
	}

	seq := 0
	for _, id := range ids {
		seq += slaOf(id)
		title := id
		if a, ok := g.Approvals[id]; ok {
			title = a.Title.Get(lang)
		}
		d.DAG.Nodes = append(d.DAG.Nodes, DAGNode{
			ID: id, Title: title, SLADays: slaOf(id),
			EarliestDay: start[id], FinishDay: finish[id],
			Critical: critical[id], Wave: waveOf[id],
		})
	}
	if lastFinish < 0 {
		lastFinish = 0
	}
	d.DAG.TotalDays = lastFinish
	d.DAG.SequentialDays = seq
}

// buildUnresolved turns every undetermined outcome into a question with the
// exact wording to put to the applicant.
func (e *Engine) buildUnresolved(in Input, d *Determination) {
	byVar := map[string]*UnresolvedDetermination{}
	get := func(name string) *UnresolvedDetermination {
		if u, ok := byVar[name]; ok {
			return u
		}
		u := &UnresolvedDetermination{Var: name, Type: e.varType(name)}
		if v, ok := e.prog.Graph.Vars[name]; ok {
			u.Question = v.Question.Get(in.Language)
			u.Hint = v.Hint.Get(in.Language)
			u.Example = v.Example
		}
		if u.Question == "" {
			u.Question = "Please provide: " + name
		}
		if un, ok := e.unitFor(name); ok {
			if u.Hint == "" {
				u.Hint = "Answer in " + un.Symbol + "."
			}
		}
		byVar[name] = u
		return u
	}

	for _, st := range d.Statuses() {
		if st.Required != value.Unknown {
			continue
		}
		for _, v := range st.UnresolvedVars {
			u := get(v)
			u.AffectsApprovals = append(u.AffectsApprovals, st.ID)
		}
	}
	for _, f := range d.Fees {
		for _, v := range f.Unresolved {
			u := get(v)
			u.AffectsFees = append(u.AffectsFees, f.FeeID)
		}
	}
	for _, m := range d.Incentives {
		for _, v := range m.Unresolved {
			u := get(v)
			u.AffectsIncentives = append(u.AffectsIncentives, m.ID)
		}
	}

	names := make([]string, 0, len(byVar))
	for k := range byVar {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, n := range names {
		u := byVar[n]
		sort.Strings(u.AffectsApprovals)
		sort.Strings(u.AffectsFees)
		sort.Strings(u.AffectsIncentives)
		u.AffectsApprovals = dedupeStrings(u.AffectsApprovals)
		u.AffectsFees = dedupeStrings(u.AffectsFees)
		u.AffectsIncentives = dedupeStrings(u.AffectsIncentives)
		d.Unresolved = append(d.Unresolved, *u)
	}
}

func dedupeStrings(s []string) []string {
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
