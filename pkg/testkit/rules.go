// Package testkit runs the two test suites a ruleset must pass before it can be
// published: the cases its authors wrote for each rule, and the property-based
// suite that holds for every rule regardless of what it says.
//
// Spec 5.5.5 makes authored positive and negative cases a precondition of
// publication, and spec 12 adds property tests over generated profiles. Both
// run in CI on every change to regulatory content.
package testkit

import (
	"fmt"
	"sort"
	"strings"

	"github.com/setu-platform/setu/pkg/compile"
	"github.com/setu-platform/setu/pkg/eval"
	"github.com/setu-platform/setu/pkg/srl"
	"github.com/setu-platform/setu/pkg/value"
)

// RuleTestResult is the outcome of one authored test case.
type RuleTestResult struct {
	RuleID   string
	TestName string
	Pos      srl.Pos
	Expected string
	Actual   string
	Passed   bool
	// Detail explains a failure in the author's own terms.
	Detail string
}

// RuleTestReport summarises an authored-test run.
type RuleTestReport struct {
	Results []RuleTestResult
	Passed  int
	Failed  int
}

// Failures returns only the failing results.
func (r RuleTestReport) Failures() []RuleTestResult {
	var out []RuleTestResult
	for _, res := range r.Results {
		if !res.Passed {
			out = append(out, res)
		}
	}
	return out
}

// RunRuleTests evaluates every authored test case against the full engine.
//
// A rule's test runs through the whole ruleset rather than against the rule's
// condition in isolation. That is deliberate: a rule whose condition reads
// required(X) has no meaning on its own, and a test that passed in isolation
// while failing in context would be worse than no test at all.
func RunRuleTests(prog *compile.Program, asOf value.Date, layers *value.Layers) RuleTestReport {
	e := eval.New(prog)
	var rep RuleTestReport

	for _, rid := range prog.Graph.RuleIDs() {
		rule := prog.Graph.Rules[rid]
		for _, tc := range rule.Tests {
			res := RuleTestResult{
				RuleID: rid, TestName: tc.Name, Pos: tc.Pos,
				Expected: expectationText(tc.Expect.Fires),
			}
			p, err := profileFromGiven(prog, tc.Given)
			if err != nil {
				res.Actual = "error"
				res.Detail = err.Error()
				rep.Results = append(rep.Results, res)
				rep.Failed++
				continue
			}

			// A rule's own effective dating must not silently fail its tests.
			// Anchor the evaluation inside the rule's validity window.
			anchor := asOf
			if !rule.Valid.Covers(anchor) {
				anchor = anchorInside(rule.Valid, asOf)
			}

			d := e.Evaluate(eval.Input{Profile: p, AsOf: anchor, Language: "en", Layers: layers},
				eval.Options{Trace: true})

			fired, found := firedStatus(d, rid)
			if !found {
				res.Actual = "not evaluated"
				res.Detail = "the rule did not appear in the derivation trace"
				rep.Results = append(rep.Results, res)
				rep.Failed++
				continue
			}
			res.Actual = fired
			res.Passed = fired == res.Expected
			if !res.Passed {
				res.Detail = explainMismatch(d, rid, p, prog)
			}
			rep.Results = append(rep.Results, res)
			if res.Passed {
				rep.Passed++
			} else {
				rep.Failed++
			}
		}
	}
	sort.SliceStable(rep.Results, func(i, j int) bool {
		if rep.Results[i].RuleID != rep.Results[j].RuleID {
			return rep.Results[i].RuleID < rep.Results[j].RuleID
		}
		return rep.Results[i].TestName < rep.Results[j].TestName
	})
	return rep
}

func expectationText(t value.Tri) string {
	switch t {
	case value.True:
		return "fires"
	case value.False:
		return "does not fire"
	}
	return "unknown"
}

func firedStatus(d *eval.Determination, ruleID string) (string, bool) {
	for _, t := range d.Trace {
		if t.RuleID != ruleID {
			continue
		}
		switch t.Fired {
		case "true":
			return "fires", true
		case "false":
			return "does not fire", true
		}
		return "unknown", true
	}
	return "", false
}

// anchorInside picks a date inside a rule's validity window so that an
// effective-dated rule can still be tested.
func anchorInside(v srl.Validity, fallback value.Date) value.Date {
	if !v.From.IsZero() && fallback.Before(v.From) {
		return v.From
	}
	if !v.Until.IsZero() && !fallback.Before(v.Until) {
		return v.Until.AddDays(-1)
	}
	return fallback
}

// profileFromGiven turns a test's `given` block into a profile.
func profileFromGiven(prog *compile.Program, given []srl.Assignment) (*eval.Profile, error) {
	p := eval.NewProfile("test")
	for _, a := range given {
		v, err := literalValue(a.Val)
		if err != nil {
			return nil, fmt.Errorf("%s: %v", a.Var, err)
		}
		p.Set(a.Var, v)
	}
	return p, nil
}

// explainMismatch renders why a test failed, in the terms the author wrote it.
func explainMismatch(d *eval.Determination, ruleID string, p *eval.Profile,
	prog *compile.Program) string {

	var b strings.Builder
	for _, t := range d.Trace {
		if t.RuleID != ruleID {
			continue
		}
		b.WriteString("condition: " + t.Condition)
		if len(t.Unresolved) > 0 {
			b.WriteString("\n  undetermined because these are unanswered: " +
				strings.Join(t.Unresolved, ", "))
		}
		for _, name := range prog.RuleReadsVars[ruleID] {
			v := p.Get(name)
			b.WriteString(fmt.Sprintf("\n  profile.%s = %s", name, v.String()))
		}
	}
	return b.String()
}

// CoverageGaps reports rules that lack the positive and negative cases spec
// 5.5.5 requires. It is separate from the linter so that `setuctl test` can
// report gaps alongside failures.
func CoverageGaps(prog *compile.Program) []string {
	var out []string
	for _, rid := range prog.Graph.RuleIDs() {
		r := prog.Graph.Rules[rid]
		var pos, neg int
		for _, t := range r.Tests {
			switch t.Expect.Fires {
			case value.True:
				pos++
			case value.False:
				neg++
			}
		}
		switch {
		case pos == 0 && neg == 0:
			out = append(out, rid+": no test cases at all")
		case pos == 0:
			out = append(out, rid+": no positive case (expect fires)")
		case neg == 0:
			out = append(out, rid+": no negative case (expect not fires)")
		}
	}
	return out
}
