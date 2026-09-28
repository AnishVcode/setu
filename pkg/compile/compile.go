// Package compile turns a checked knowledge graph into an executable Program:
// a deterministic evaluation order, resolved cross-references, and the indexes
// the evaluator needs to settle every approval exactly once.
//
// Most rules are independent — they read the applicant's profile and nothing
// else — and could be evaluated in any order. Two constructs create order
// dependencies, and both are real in law:
//
//	required(X)          a rule that applies only when another approval applies
//	depends_on           an approval that cannot be granted before another
//
// Compilation lays these out as a single directed graph over rules and
// approvals, topologically sorts it, and refuses to produce a Program when the
// graph contains a cycle. A cycle would mean the encoded law has no fixed
// point: the answer would depend on evaluation order, which P1 forbids.
package compile

import (
	"fmt"
	"sort"
	"strings"

	"github.com/setu-platform/setu/pkg/kg"
	"github.com/setu-platform/setu/pkg/sema"
	"github.com/setu-platform/setu/pkg/srl"
)

// NodeKind distinguishes the two node types in the evaluation order.
type NodeKind uint8

const (
	// NodeRule evaluates a rule's condition and records its effects.
	NodeRule NodeKind = iota
	// NodeApproval settles whether an approval is required, once every rule and
	// dependency that could affect it has been processed.
	NodeApproval
)

// Node is one step of the evaluation order.
type Node struct {
	Kind NodeKind
	ID   string
}

// Key is the node's stable identity in the dependency graph.
func (n Node) Key() string {
	if n.Kind == NodeRule {
		return "R:" + n.ID
	}
	return "A:" + n.ID
}

func (n Node) String() string {
	if n.Kind == NodeRule {
		return "rule " + n.ID
	}
	return "approval " + n.ID
}

// Program is a compiled ruleset, ready for evaluation.
type Program struct {
	Graph    *kg.Graph
	VarTypes map[string]sema.Type

	// Order is the topologically sorted evaluation sequence.
	Order []Node

	// RuleReadsApprovals maps a rule to the approvals its condition interrogates.
	RuleReadsApprovals map[string][]string
	// RuleReadsVars maps a rule to the profile variables its condition reads.
	RuleReadsVars map[string][]string
	// RuleTargets maps a rule to the approvals its effects act on.
	RuleTargets map[string][]string
	// ApprovalRules maps an approval to the rules that can require or exempt it.
	ApprovalRules map[string][]string
	// ApprovalPrereqs maps an approval to every prerequisite it may have, whether
	// declared on the approval or created conditionally by a rule. It is the
	// union used for ordering and cycle detection: a cycle among edges that
	// might exist is a modelling error even if no profile activates them all.
	ApprovalPrereqs map[string][]string
	// ApprovalDependents is the reverse: prerequisite to the approvals needing it.
	ApprovalDependents map[string][]string

	// Edges carries every dependency edge with its provenance. An edge created
	// by a rule applies only when that rule fires, which is how one approval can
	// wait on whichever of two alternative authorities actually has
	// jurisdiction.
	Edges            []DepEdge
	EdgesByPrereq    map[string][]DepEdge
	EdgesByDependent map[string][]DepEdge

	// DependencyRules maps a prerequisite to the rules that may create an edge
	// into it, so those rules are evaluated before it is settled.
	DependencyRules map[string][]string

	// DependencyReason carries the applicant-facing explanation for each edge,
	// keyed "dependent->prerequisite".
	DependencyReason map[string]srl.LocalisedText
}

// DepEdge is one dependency relation between approvals.
type DepEdge struct {
	// Dependent cannot be granted until Prereq is.
	Dependent string
	Prereq    string
	// RuleID is empty for an edge declared on the approval itself, and names the
	// rule for a conditional edge.
	RuleID string
	Reason srl.LocalisedText
}

// Conditional reports whether the edge depends on a rule firing.
func (e DepEdge) Conditional() bool { return e.RuleID != "" }

// Compile builds a Program, reporting cycles and unresolved order dependencies.
// It assumes sema.Check has already run; it does not re-check types.
func Compile(g *kg.Graph) (*Program, []srl.Diagnostic) {
	varTypes, diags := sema.VarTypes(g)
	p := &Program{
		Graph:              g,
		VarTypes:           varTypes,
		RuleReadsApprovals: map[string][]string{},
		RuleReadsVars:      map[string][]string{},
		RuleTargets:        map[string][]string{},
		ApprovalRules:      map[string][]string{},
		ApprovalPrereqs:    map[string][]string{},
		ApprovalDependents: map[string][]string{},
		EdgesByPrereq:      map[string][]DepEdge{},
		EdgesByDependent:   map[string][]DepEdge{},
		DependencyRules:    map[string][]string{},
		DependencyReason:   map[string]srl.LocalisedText{},
	}

	p.indexRules()
	p.indexDependencies()

	sort.SliceStable(p.Edges, func(i, j int) bool {
		if p.Edges[i].Prereq != p.Edges[j].Prereq {
			return p.Edges[i].Prereq < p.Edges[j].Prereq
		}
		if p.Edges[i].Dependent != p.Edges[j].Dependent {
			return p.Edges[i].Dependent < p.Edges[j].Dependent
		}
		return p.Edges[i].RuleID < p.Edges[j].RuleID
	})
	for k := range p.EdgesByPrereq {
		sortEdges(p.EdgesByPrereq[k])
	}
	for k := range p.EdgesByDependent {
		sortEdges(p.EdgesByDependent[k])
	}

	order, cycleDiags := p.topoSort()
	diags = append(diags, cycleDiags...)
	p.Order = order
	return p, diags
}

// indexRules records what each rule reads and what it affects.
func (p *Program) indexRules() {
	g := p.Graph
	for _, id := range g.RuleIDs() {
		r := g.Rules[id]

		readsApprovals := map[string]bool{}
		readsVars := map[string]bool{}
		srl.Walk(r.When, func(e srl.Expr) {
			switch x := e.(type) {
			case *srl.ProfileRef:
				readsVars[x.Var] = true
			case *srl.CallExpr:
				if x.Fn != "required" && x.Fn != "exempted" {
					return
				}
				if len(x.Args) != 1 {
					return
				}
				if ref, ok := x.Args[0].(*srl.ObjectRef); ok {
					if _, isApproval := g.Approvals[ref.Name()]; isApproval {
						readsApprovals[ref.Name()] = true
					}
				}
			}
		})
		p.RuleReadsApprovals[id] = sortedSet(readsApprovals)
		p.RuleReadsVars[id] = sortedSet(readsVars)

		targets := map[string]bool{}
		for _, e := range r.Then {
			switch e.Kind {
			case srl.EffectRequireApproval, srl.EffectExemptApproval:
				targets[e.Target] = true
				p.ApprovalRules[e.Target] = append(p.ApprovalRules[e.Target], id)
			case srl.EffectRequireDocument, srl.EffectLevyFee, srl.EffectWaiveFee,
				srl.EffectCreateObligation:
				// These attach to an approval but do not change whether it is
				// required, so they create no ordering constraint on settling.
				targets[e.On] = true
			case srl.EffectDependency:
				// A conditional dependency edge: `depend A on B` means A cannot
				// proceed until B is granted, so requiring A implies requiring B
				// — but only when this rule actually fires.
				p.addDependency(e.On, e.Target, e.Reason, id)
				p.DependencyRules[e.Target] = append(p.DependencyRules[e.Target], id)
				targets[e.On] = true
				targets[e.Target] = true
			}
		}
		p.RuleTargets[id] = sortedSet(targets)
	}
	for k := range p.ApprovalRules {
		sort.Strings(p.ApprovalRules[k])
		p.ApprovalRules[k] = dedupe(p.ApprovalRules[k])
	}
}

// indexDependencies records the dependency edges declared on approvals.
func (p *Program) indexDependencies() {
	g := p.Graph
	for _, id := range g.ApprovalIDs() {
		a := g.Approvals[id]
		for _, d := range a.DependsOn {
			p.addDependency(id, d.Approval, d.Reason, "")
		}
	}
	for k := range p.DependencyRules {
		sort.Strings(p.DependencyRules[k])
		p.DependencyRules[k] = dedupe(p.DependencyRules[k])
	}
	for k := range p.ApprovalPrereqs {
		sort.Strings(p.ApprovalPrereqs[k])
		p.ApprovalPrereqs[k] = dedupe(p.ApprovalPrereqs[k])
	}
	for k := range p.ApprovalDependents {
		sort.Strings(p.ApprovalDependents[k])
		p.ApprovalDependents[k] = dedupe(p.ApprovalDependents[k])
	}
}

func (p *Program) addDependency(dependent, prerequisite string, reason srl.LocalisedText,
	ruleID string) {

	if dependent == "" || prerequisite == "" || dependent == prerequisite {
		return
	}
	p.ApprovalPrereqs[dependent] = append(p.ApprovalPrereqs[dependent], prerequisite)
	p.ApprovalDependents[prerequisite] = append(p.ApprovalDependents[prerequisite], dependent)
	key := dependent + "->" + prerequisite
	if len(reason) > 0 {
		p.DependencyReason[key] = reason
	}
	e := DepEdge{Dependent: dependent, Prereq: prerequisite, RuleID: ruleID, Reason: reason}
	p.Edges = append(p.Edges, e)
	p.EdgesByPrereq[prerequisite] = append(p.EdgesByPrereq[prerequisite], e)
	p.EdgesByDependent[dependent] = append(p.EdgesByDependent[dependent], e)
}

// DependencyReasonFor returns the applicant-facing reason for a dependency edge.
func (p *Program) DependencyReasonFor(dependent, prerequisite string) srl.LocalisedText {
	return p.DependencyReason[dependent+"->"+prerequisite]
}

// topoSort produces the evaluation order.
//
// Edges point from a node that must be processed first to one that must follow:
//
//	approval A -> rule R      when R's condition calls required(A)
//	rule R     -> approval X  when R can require or exempt X
//	approval D -> approval P  when D depends on P, since requiring D requires P
//
// Ties are broken by node key so that the order is identical on every run and
// on every machine.
func (p *Program) topoSort() ([]Node, []srl.Diagnostic) {
	g := p.Graph
	nodes := map[string]Node{}
	for _, id := range g.RuleIDs() {
		nodes["R:"+id] = Node{Kind: NodeRule, ID: id}
	}
	for _, id := range g.ApprovalIDs() {
		nodes["A:"+id] = Node{Kind: NodeApproval, ID: id}
	}

	adj := map[string][]string{}
	indeg := map[string]int{}
	for k := range nodes {
		indeg[k] = 0
	}
	addEdge := func(from, to string) {
		if _, ok := nodes[from]; !ok {
			return
		}
		if _, ok := nodes[to]; !ok {
			return
		}
		adj[from] = append(adj[from], to)
		indeg[to]++
	}

	for _, id := range g.RuleIDs() {
		for _, a := range p.RuleReadsApprovals[id] {
			addEdge("A:"+a, "R:"+id)
		}
	}
	for _, aid := range g.ApprovalIDs() {
		for _, rid := range p.ApprovalRules[aid] {
			addEdge("R:"+rid, "A:"+aid)
		}
		for _, prereq := range p.ApprovalPrereqs[aid] {
			addEdge("A:"+aid, "A:"+prereq)
		}
		// A rule that may create an edge into this approval must be evaluated
		// before the approval is settled, or its edge would arrive too late.
		for _, rid := range p.DependencyRules[aid] {
			addEdge("R:"+rid, "A:"+aid)
		}
	}

	// Kahn's algorithm over a deterministically ordered ready set.
	var ready []string
	for k := range indeg {
		if indeg[k] == 0 {
			ready = append(ready, k)
		}
	}
	sort.Strings(ready)

	var order []Node
	for len(ready) > 0 {
		k := ready[0]
		ready = ready[1:]
		order = append(order, nodes[k])
		next := append([]string(nil), adj[k]...)
		sort.Strings(next)
		var newlyReady []string
		for _, m := range next {
			indeg[m]--
			if indeg[m] == 0 {
				newlyReady = append(newlyReady, m)
			}
		}
		ready = append(ready, newlyReady...)
		sort.Strings(ready)
	}

	if len(order) == len(nodes) {
		return order, nil
	}

	// Everything left has a non-zero in-degree and is therefore in, or
	// downstream of, a cycle. Report the cycles themselves, not the fallout.
	remaining := map[string]bool{}
	for k := range nodes {
		remaining[k] = true
	}
	for _, n := range order {
		delete(remaining, n.Key())
	}
	return nil, p.reportCycles(nodes, adj, remaining)
}

// reportCycles finds concrete cycles among the unsorted nodes so the author is
// shown the actual loop rather than a list of everything it touches.
func (p *Program) reportCycles(nodes map[string]Node, adj map[string][]string,
	remaining map[string]bool) []srl.Diagnostic {

	var diags []srl.Diagnostic
	colour := map[string]int{} // 0 unvisited, 1 on stack, 2 done
	var stack []string
	reported := map[string]bool{}

	var dfs func(k string)
	dfs = func(k string) {
		colour[k] = 1
		stack = append(stack, k)
		next := append([]string(nil), adj[k]...)
		sort.Strings(next)
		for _, m := range next {
			if !remaining[m] {
				continue
			}
			switch colour[m] {
			case 0:
				dfs(m)
			case 1:
				// Found a cycle: the stack from m onwards.
				var cycle []string
				for i := len(stack) - 1; i >= 0; i-- {
					cycle = append([]string{stack[i]}, cycle...)
					if stack[i] == m {
						break
					}
				}
				sig := cycleSignature(cycle)
				if reported[sig] {
					continue
				}
				reported[sig] = true
				var names []string
				for _, c := range cycle {
					names = append(names, nodes[c].String())
				}
				names = append(names, nodes[cycle[0]].String())
				diags = append(diags, srl.Diagnostic{
					Severity: srl.SevError, Code: "CMP0001",
					Pos: p.posOf(nodes[cycle[0]]),
					Msg: "circular dependency: " + strings.Join(names, " → "),
					Hint: "break the loop; encoded law with a cycle has no " +
						"determinate answer, because the outcome would depend on evaluation order",
				})
			}
		}
		stack = stack[:len(stack)-1]
		colour[k] = 2
	}

	var keys []string
	for k := range remaining {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if colour[k] == 0 {
			dfs(k)
		}
	}
	if len(diags) == 0 {
		diags = append(diags, srl.Diagnostic{
			Severity: srl.SevError, Code: "CMP0001",
			Msg: fmt.Sprintf("circular dependency involving %d rules or approvals", len(remaining)),
		})
	}
	return diags
}

// cycleSignature normalises a cycle to its lexicographically smallest rotation
// so the same loop is not reported once per member.
func cycleSignature(cycle []string) string {
	if len(cycle) == 0 {
		return ""
	}
	best := 0
	for i := range cycle {
		if cycle[i] < cycle[best] {
			best = i
		}
	}
	rot := append(append([]string{}, cycle[best:]...), cycle[:best]...)
	return strings.Join(rot, "|")
}

func (p *Program) posOf(n Node) srl.Pos {
	if n.Kind == NodeRule {
		if r, ok := p.Graph.Rules[n.ID]; ok {
			return r.Pos
		}
	} else if a, ok := p.Graph.Approvals[n.ID]; ok {
		return a.Pos
	}
	return srl.Pos{}
}

func sortEdges(e []DepEdge) {
	sort.SliceStable(e, func(i, j int) bool {
		if e[i].Prereq != e[j].Prereq {
			return e[i].Prereq < e[j].Prereq
		}
		if e[i].Dependent != e[j].Dependent {
			return e[i].Dependent < e[j].Dependent
		}
		return e[i].RuleID < e[j].RuleID
	})
}

func sortedSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		if k != "" {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func dedupe(s []string) []string {
	out := s[:0]
	var last string
	for i, v := range s {
		if i > 0 && v == last {
			continue
		}
		last = v
		out = append(out, v)
	}
	return out
}
