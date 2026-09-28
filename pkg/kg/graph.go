// Package kg holds the regulatory knowledge graph: the merged, indexed set of
// declarations that make up one jurisdiction's law as encoded in SRL.
//
// The graph is the thing a ruleset version is a snapshot of. Everything about
// it is ordered: iteration over any collection yields a stable sequence, so
// that a determination, a diff, a hash and an impact analysis are reproducible
// byte for byte (P1, P6).
package kg

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/setu-platform/setu/pkg/srl"
	"github.com/setu-platform/setu/pkg/value"
)

// Graph is a jurisdiction's complete encoded regulatory content.
type Graph struct {
	Package string
	Meta    map[string]string

	Enums       map[string]*srl.EnumDecl
	Vars        map[string]*srl.VarDecl
	Departments map[string]*srl.DepartmentDecl
	Documents   map[string]*srl.DocumentDecl
	Approvals   map[string]*srl.ApprovalDecl
	Fees        map[string]*srl.FeeDecl
	Obligations map[string]*srl.ObligationDecl
	Incentives  map[string]*srl.IncentiveDecl
	Rules       map[string]*srl.RuleDecl

	// Files records the sources, in load order, for diffs and provenance.
	Files []*srl.File

	varOrder []string
}

// New constructs an empty graph.
func New() *Graph {
	return &Graph{
		Meta:        map[string]string{},
		Enums:       map[string]*srl.EnumDecl{},
		Vars:        map[string]*srl.VarDecl{},
		Departments: map[string]*srl.DepartmentDecl{},
		Documents:   map[string]*srl.DocumentDecl{},
		Approvals:   map[string]*srl.ApprovalDecl{},
		Fees:        map[string]*srl.FeeDecl{},
		Obligations: map[string]*srl.ObligationDecl{},
		Incentives:  map[string]*srl.IncentiveDecl{},
		Rules:       map[string]*srl.RuleDecl{},
	}
}

// LoadDir parses every .srl file under dir, recursively, and merges them.
// Files are visited in sorted path order so that a duplicate-identifier error
// always names the same file as the original and the same file as the
// duplicate, run after run.
func LoadDir(dir string) (*Graph, []srl.Diagnostic) {
	var paths []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && d.Name() != "." {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(p, ".srl") {
			paths = append(paths, p)
		}
		return nil
	})
	if err != nil {
		return nil, []srl.Diagnostic{{
			Severity: srl.SevError, Code: "KG0001",
			Msg: fmt.Sprintf("cannot read %s: %v", dir, err),
		}}
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		return nil, []srl.Diagnostic{{
			Severity: srl.SevError, Code: "KG0002",
			Pos:  srl.Pos{File: dir},
			Msg:  "no .srl files found",
			Hint: "regulatory content lives in .srl files under the jurisdiction directory",
		}}
	}

	var files []*srl.File
	var diags []srl.Diagnostic
	for _, p := range paths {
		src, err := os.ReadFile(p)
		if err != nil {
			diags = append(diags, srl.Diagnostic{
				Severity: srl.SevError, Code: "KG0001", Pos: srl.Pos{File: p},
				Msg: fmt.Sprintf("cannot read file: %v", err),
			})
			continue
		}
		f, fd := srl.Parse(p, string(src))
		diags = append(diags, fd...)
		if f != nil {
			files = append(files, f)
		}
	}
	g, bd := Build(files)
	return g, append(diags, bd...)
}

// LoadString parses a single in-memory source, for tests and Rule Studio's
// live preview.
func LoadString(name, src string) (*Graph, []srl.Diagnostic) {
	f, diags := srl.Parse(name, src)
	if f == nil {
		return nil, diags
	}
	g, bd := Build([]*srl.File{f})
	return g, append(diags, bd...)
}

// Build merges parsed files into one graph, rejecting duplicate identifiers.
//
// Duplicate identifiers are an error rather than a last-write-wins merge. Two
// rules with the same ID in different files would make the meaning of a
// determination depend on file ordering, which is exactly the class of
// non-determinism P1 forbids.
func Build(files []*srl.File) (*Graph, []srl.Diagnostic) {
	g := New()
	var diags []srl.Diagnostic
	seen := map[string]srl.Pos{}

	dup := func(kind, id string, pos srl.Pos) bool {
		key := kind + ":" + id
		if prev, ok := seen[key]; ok {
			diags = append(diags, srl.Diagnostic{
				Severity: srl.SevError, Code: "KG0010", Pos: pos,
				Msg:  fmt.Sprintf("duplicate %s %q", kind, id),
				Hint: "first declared at " + prev.String(),
			})
			return true
		}
		seen[key] = pos
		return false
	}

	for _, f := range files {
		g.Files = append(g.Files, f)
		if g.Package == "" {
			g.Package = f.Package
		} else if f.Package != g.Package {
			diags = append(diags, srl.Diagnostic{
				Severity: srl.SevError, Code: "KG0011", Pos: srl.Pos{File: f.Path},
				Msg: fmt.Sprintf("file declares package %q but the ruleset is %q",
					f.Package, g.Package),
				Hint: "one directory holds the content of exactly one jurisdiction",
			})
		}
		for k, v := range f.Meta {
			g.Meta[k] = v
		}
		for _, d := range f.Enums {
			if !dup("enum", d.Name, d.Pos) {
				g.Enums[d.Name] = d
			}
		}
		if f.Profile != nil {
			for _, v := range f.Profile.Vars {
				if !dup("profile variable", v.Name, v.Pos) {
					g.Vars[v.Name] = v
					g.varOrder = append(g.varOrder, v.Name)
				}
			}
		}
		for _, d := range f.Departments {
			if !dup("department", d.ID, d.Pos) {
				g.Departments[d.ID] = d
			}
		}
		for _, d := range f.Documents {
			if !dup("document", d.ID, d.Pos) {
				g.Documents[d.ID] = d
			}
		}
		for _, d := range f.Approvals {
			if !dup("approval", d.ID, d.Pos) {
				g.Approvals[d.ID] = d
			}
		}
		for _, d := range f.Fees {
			if !dup("fee", d.ID, d.Pos) {
				g.Fees[d.ID] = d
			}
		}
		for _, d := range f.Obligations {
			if !dup("obligation", d.ID, d.Pos) {
				g.Obligations[d.ID] = d
			}
		}
		for _, d := range f.Incentives {
			if !dup("incentive", d.ID, d.Pos) {
				g.Incentives[d.ID] = d
			}
		}
		for _, d := range f.Rules {
			if !dup("rule", d.ID, d.Pos) {
				g.Rules[d.ID] = d
			}
		}
	}
	return g, diags
}

// ---------- ordered accessors ----------
//
// Every traversal in the engine goes through one of these, never through a
// bare map range, so that output ordering never depends on Go's randomised map
// iteration.

// RuleIDs lists rule identifiers in sorted order.
func (g *Graph) RuleIDs() []string { return sortedKeys(g.Rules) }

// ApprovalIDs lists approval identifiers in sorted order.
func (g *Graph) ApprovalIDs() []string { return sortedKeys(g.Approvals) }

// DocumentIDs lists document identifiers in sorted order.
func (g *Graph) DocumentIDs() []string { return sortedKeys(g.Documents) }

// FeeIDs lists fee identifiers in sorted order.
func (g *Graph) FeeIDs() []string { return sortedKeys(g.Fees) }

// ObligationIDs lists obligation identifiers in sorted order.
func (g *Graph) ObligationIDs() []string { return sortedKeys(g.Obligations) }

// IncentiveIDs lists incentive identifiers in sorted order.
func (g *Graph) IncentiveIDs() []string { return sortedKeys(g.Incentives) }

// DepartmentIDs lists department identifiers in sorted order.
func (g *Graph) DepartmentIDs() []string { return sortedKeys(g.Departments) }

// EnumNames lists enum type names in sorted order.
func (g *Graph) EnumNames() []string { return sortedKeys(g.Enums) }

// VarNames lists profile variables in declaration order, which is the order
// the applicant is asked them in.
func (g *Graph) VarNames() []string {
	out := make([]string, len(g.varOrder))
	copy(out, g.varOrder)
	return out
}

// VarNamesSorted lists profile variables alphabetically, for diagnostics.
func (g *Graph) VarNamesSorted() []string { return sortedKeys(g.Vars) }

func sortedKeys[T any](m map[string]T) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// EnumMember looks up a member of an enum type.
func (g *Graph) EnumMember(enumName, member string) (*srl.EnumMember, bool) {
	e, ok := g.Enums[enumName]
	if !ok {
		return nil, false
	}
	for i := range e.Members {
		if e.Members[i].Name == member {
			return &e.Members[i], true
		}
	}
	return nil, false
}

// ActiveRules returns the rules in force on the given date, sorted by ID.
//
// Effective dating is applied here and nowhere else, so there is exactly one
// place in the engine that decides whether a rule was law on the day a case was
// submitted (P6).
func (g *Graph) ActiveRules(on value.Date) []*srl.RuleDecl {
	var out []*srl.RuleDecl
	for _, id := range g.RuleIDs() {
		r := g.Rules[id]
		if r.Valid.Covers(on) {
			out = append(out, r)
		}
	}
	return out
}

// ActiveApprovals returns the approval types in force on the given date.
func (g *Graph) ActiveApprovals(on value.Date) []*srl.ApprovalDecl {
	var out []*srl.ApprovalDecl
	for _, id := range g.ApprovalIDs() {
		a := g.Approvals[id]
		if a.Valid.Covers(on) {
			out = append(out, a)
		}
	}
	return out
}

// Counts summarises the graph for the CLI and for Rule Studio's header.
func (g *Graph) Counts() map[string]int {
	return map[string]int{
		"rules":       len(g.Rules),
		"approvals":   len(g.Approvals),
		"documents":   len(g.Documents),
		"fees":        len(g.Fees),
		"obligations": len(g.Obligations),
		"incentives":  len(g.Incentives),
		"departments": len(g.Departments),
		"enums":       len(g.Enums),
		"profileVars": len(g.Vars),
		"files":       len(g.Files),
	}
}

// CountsString renders the counts in a fixed order.
func (g *Graph) CountsString() string {
	c := g.Counts()
	keys := sortedKeys(c)
	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", k, c[k]))
	}
	return strings.Join(parts, " ")
}
