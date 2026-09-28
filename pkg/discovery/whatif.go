// Package discovery builds the applicant-facing views over a determination:
// scenario comparison for site selection, and the diff between two
// determinations.
//
// The what-if simulator of spec 6.1 is a site-selection tool, not a novelty.
// An entrepreneur choosing between two districts wants to know, before
// committing to land: which approvals change, what the fees come to, and how
// much longer the slower option takes. All three come out of the same
// deterministic engine, so the comparison is as defensible as the determination.
package discovery

import (
	"sort"

	"github.com/setu-platform/setu/pkg/eval"
	"github.com/setu-platform/setu/pkg/value"
)

// Scenario is one named alternative under comparison.
type Scenario struct {
	Name          string
	Profile       *eval.Profile
	Determination *eval.Determination
}

// Comparison is a side-by-side view of several scenarios.
type Comparison struct {
	Scenarios []Scenario
	// Rows are the approvals appearing in any scenario, with the status of each.
	Rows []ApprovalRow
	// Differences names the approvals whose status is not the same everywhere.
	// It is the answer to "what actually changes if I move?".
	Differences []string
	// Summary carries the headline figures per scenario.
	Summary []ScenarioSummary
}

// ApprovalRow is one approval across every scenario.
type ApprovalRow struct {
	ID       string
	Title    string
	Statuses []Status
	Differs  bool
}

// Status is one approval's outcome in one scenario.
type Status struct {
	Required value.Tri
	Exempted bool
	// Why is the short reason, so a difference can be explained on the spot.
	Why string
}

// Text renders the status for display.
func (s Status) Text() string {
	switch s.Required {
	case value.True:
		return "required"
	case value.False:
		if s.Exempted {
			return "exempt"
		}
		return "not required"
	}
	return "undetermined"
}

// ScenarioSummary is the headline comparison for one scenario.
type ScenarioSummary struct {
	Name            string
	ApprovalCount   int
	FeeTotal        string
	FeeComplete     bool
	CriticalDays    int
	SequentialDays  int
	DocumentCount   int
	IncentiveCount  int
	UnresolvedCount int
	ConflictCount   int
}

// NamedProfile pairs a scenario name with the profile to evaluate.
type NamedProfile struct {
	Name    string
	Profile *eval.Profile
}

// Compare evaluates every scenario against the same engine, anchor date and
// boundary layers, so the only thing that varies is the profile.
func Compare(e *eval.Engine, base eval.Input, opts eval.Options, profiles []NamedProfile) *Comparison {
	c := &Comparison{}
	seen := map[string]bool{}

	for _, np := range profiles {
		in := base
		in.Profile = np.Profile
		d := e.Evaluate(in, opts)
		c.Scenarios = append(c.Scenarios, Scenario{
			Name: np.Name, Profile: np.Profile, Determination: d,
		})
		c.Summary = append(c.Summary, ScenarioSummary{
			Name:            np.Name,
			ApprovalCount:   len(d.Approvals),
			FeeTotal:        d.FeeTotal,
			FeeComplete:     d.FeeComplete,
			CriticalDays:    d.DAG.TotalDays,
			SequentialDays:  d.DAG.SequentialDays,
			DocumentCount:   len(d.Documents),
			IncentiveCount:  len(d.Incentives),
			UnresolvedCount: len(d.Unresolved),
			ConflictCount:   len(d.Conflicts),
		})
		for _, st := range d.Statuses() {
			seen[st.ID] = true
		}
	}

	var ids []string
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		row := ApprovalRow{ID: id}
		var first Status
		relevant := false
		for i, sc := range c.Scenarios {
			st, _ := sc.Determination.StatusOf(id)
			s := Status{}
			if st != nil {
				s.Required = st.Required
				s.Exempted = st.Exempted
				s.Why = st.DecidedBy
				if st.ByDependency != "" {
					s.Why = "required by " + st.ByDependency
				}
			}
			if s.Required != value.False || s.Exempted {
				relevant = true
			}
			if i == 0 {
				first = s
			} else if s.Required != first.Required || s.Exempted != first.Exempted {
				row.Differs = true
			}
			row.Statuses = append(row.Statuses, s)
			if row.Title == "" {
				for _, a := range sc.Determination.Approvals {
					if a.ID == id {
						row.Title = a.Title
					}
				}
			}
		}
		// An approval that is irrelevant in every scenario is noise in a
		// comparison meant to inform a decision.
		if !relevant && !row.Differs {
			continue
		}
		c.Rows = append(c.Rows, row)
		if row.Differs {
			c.Differences = append(c.Differences, id)
		}
	}
	return c
}

// Cheapest returns the index of the scenario with the lowest complete fee
// total, and whether a comparison was possible at all.
func (c *Comparison) Cheapest() (int, bool) {
	best, bestVal := -1, ""
	for i, s := range c.Summary {
		if !s.FeeComplete {
			continue
		}
		if best == -1 || lessMoney(s.FeeTotal, bestVal) {
			best, bestVal = i, s.FeeTotal
		}
	}
	return best, best >= 0
}

// Fastest returns the index of the scenario with the shortest critical path.
func (c *Comparison) Fastest() (int, bool) {
	best := -1
	for i, s := range c.Summary {
		if best == -1 || s.CriticalDays < c.Summary[best].CriticalDays {
			best = i
		}
	}
	return best, best >= 0
}

// Simplest returns the index of the scenario with the fewest approvals.
func (c *Comparison) Simplest() (int, bool) {
	best := -1
	for i, s := range c.Summary {
		if best == -1 || s.ApprovalCount < c.Summary[best].ApprovalCount {
			best = i
		}
	}
	return best, best >= 0
}

// lessMoney compares two rendered amounts by length then lexically, which
// orders correctly for the "INR n,nn,nnn" form the engine emits.
func lessMoney(a, b string) bool {
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	return a < b
}
