package analysis

import (
	"strings"
	"testing"

	"github.com/setu-platform/setu/pkg/compile"
	"github.com/setu-platform/setu/pkg/kg"
	"github.com/setu-platform/setu/pkg/sema"
	"github.com/setu-platform/setu/pkg/srl"
	"github.com/setu-platform/setu/pkg/value"
)

const broken = `
package IN.TEST

enum Cat { A B C }

profile {
  var workers: Quantity<Count> { question en "How many workers?" unit workers }
  var powered: Bool            { question en "Power-driven machinery?" }
  var cat: Enum<Cat>           { question en "Category?" }
}

department D.X { name en "Department" jurisdiction IN.TEST adapter_tier api }

approval A.GOOD {
  title en "A required approval"
  category "licence"
  department D.X
  sla 30 days
  legal_basis { act "Test Act" section "1" }
}

approval A.ORPHAN {
  title en "An approval nothing can require"
  category "licence"
  department D.X
  sla 30 days
  legal_basis { act "Test Act" section "2" }
}

// Contradictory bounds on one variable: no profile can satisfy this.
rule R.UNREACHABLE {
  kind trigger
  when profile.workers > 100 workers and profile.workers < 50 workers
  then { require approval A.GOOD }
  explain en "Never fires."
  legal_basis { act "Test Act" section "3" }
  test "p" { given { profile.workers = 200 workers } expect not fires }
  test "n" { given { profile.workers = 10 workers } expect not fires }
}

// Two rules of equal precedence pulling in opposite directions on A.GOOD.
rule R.REQUIRE {
  kind trigger
  precedence 100
  when profile.powered == true
  then { require approval A.GOOD }
  explain en "Requires it."
  legal_basis { act "Test Act" section "4" }
  test "p" { given { profile.powered = true } expect fires }
  test "n" { given { profile.powered = false } expect not fires }
}

rule R.EXEMPT {
  kind exemption
  precedence 100
  when profile.cat == Cat.A
  then { exempt approval A.GOOD }
  explain en "Exempts it."
  legal_basis { act "Test Act" section "5" }
  test "p" { given { profile.cat = Cat.A } expect fires }
  test "n" { given { profile.cat = Cat.B } expect not fires }
}
`

func buildProgram(t *testing.T, src string) *compile.Program {
	t.Helper()
	g, diags := kg.LoadString("broken.srl", src)
	assertNoErrors(t, diags)
	assertNoErrors(t, sema.Check(g))
	prog, diags := compile.Compile(g)
	assertNoErrors(t, diags)
	return prog
}

func assertNoErrors(t *testing.T, diags []srl.Diagnostic) {
	t.Helper()
	for _, d := range diags {
		if d.Severity == srl.SevError {
			t.Fatalf("unexpected error: %s", d)
		}
	}
}

func TestAnalysisFindsRealDefects(t *testing.T) {
	prog := buildProgram(t, broken)
	cfg := DefaultConfig(value.Date{Year: 2026, Month: 9, Day: 4})
	cfg.Attempts = 500
	rep := Run(prog, cfg)

	var unreachable, contradiction, orphan *Finding
	for i := range rep.Findings {
		f := &rep.Findings[i]
		switch {
		case f.Kind == "unreachable" && f.Subject == "R.UNREACHABLE":
			unreachable = f
		case f.Kind == "contradiction" && f.Subject == "A.GOOD" && f.Proven:
			contradiction = f
		case f.Kind == "orphan" && f.Subject == "A.ORPHAN":
			orphan = f
		}
	}

	if unreachable == nil {
		t.Fatalf("the unreachable rule was not detected; findings: %+v", rep.Findings)
	}
	if !unreachable.Proven {
		t.Error("contradictory numeric bounds should be proven unsatisfiable, not merely searched for")
	}
	if !strings.Contains(unreachable.Msg, "can never fire") {
		t.Errorf("unexpected message: %s", unreachable.Msg)
	}

	if contradiction == nil {
		t.Fatalf("the equal-precedence contradiction was not detected; findings: %+v", rep.Findings)
	}
	// A rule proven unreachable must not also be reported as contradicting
	// something: one defect, one finding.
	for _, f := range rep.Findings {
		if f.Kind == "contradiction" && strings.Contains(f.Msg, "R.UNREACHABLE") {
			t.Errorf("a proven-unreachable rule was also reported as a contradiction: %s", f.Msg)
		}
	}
	if !contradiction.Proven || contradiction.Witness == "" {
		t.Errorf("a contradiction should be reported with a concrete witness profile: %+v", contradiction)
	}
	if !strings.Contains(contradiction.Witness, "profile.powered = true") ||
		!strings.Contains(contradiction.Witness, "profile.cat = Cat.A") {
		t.Errorf("the witness does not actually trigger both rules:\n%s", contradiction.Witness)
	}

	if orphan == nil {
		t.Fatalf("the orphaned approval was not detected")
	}
	if !orphan.Proven {
		t.Error("orphan detection is exact and should be reported as proven")
	}
}

// TestCleanRulesetHasNoFindings guards against the analysis crying wolf, which
// is the failure mode that makes an author stop reading it.
func TestCleanRulesetHasNoFindings(t *testing.T) {
	const clean = `
package IN.TEST
profile {
  var workers: Quantity<Count> { question en "How many workers?" unit workers }
}
department D.X { name en "Department" jurisdiction IN.TEST adapter_tier api }
approval A.X {
  title en "Licence"
  category "licence"
  department D.X
  sla 30 days
  legal_basis { act "Test Act" section "1" }
}
rule R.X {
  kind trigger
  when profile.workers >= 10 workers
  then { require approval A.X }
  explain en "Ten or more workers."
  legal_basis { act "Test Act" section "2" }
  test "p" { given { profile.workers = 10 workers } expect fires }
  test "n" { given { profile.workers = 9 workers } expect not fires }
}
`
	prog := buildProgram(t, clean)
	cfg := DefaultConfig(value.Date{Year: 2026, Month: 9, Day: 4})
	cfg.Attempts = 300
	rep := Run(prog, cfg)
	if len(rep.Findings) != 0 {
		t.Errorf("clean ruleset produced findings: %+v", rep.Findings)
	}
}

func TestProveUnsatisfiable(t *testing.T) {
	cases := []struct {
		name   string
		cond   string
		proven bool
	}{
		{"contradictory bounds", "profile.workers > 100 workers and profile.workers < 50 workers", true},
		{"exclusive equal bounds", "profile.workers > 10 workers and profile.workers < 10 workers", true},
		{"equality against a bound", "profile.workers == 5 workers and profile.workers > 10 workers", true},
		{"two different equalities", "profile.cat == Cat.A and profile.cat == Cat.B", true},
		{"equal and not equal", "profile.cat == Cat.A and profile.cat != Cat.A", true},
		{"both boolean values", "profile.powered == true and profile.powered == false", true},
		{"literal false", "false", true},
		{"satisfiable range", "profile.workers > 10 workers and profile.workers < 50 workers", false},
		{"satisfiable disjunction", "profile.workers > 100 workers or profile.workers < 50 workers", false},
		{"single bound", "profile.workers >= 20 workers", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := strings.Replace(broken,
				"when profile.workers > 100 workers and profile.workers < 50 workers",
				"when "+tc.cond, 1)
			g, diags := kg.LoadString("t.srl", src)
			assertNoErrors(t, diags)
			got, proven := proveUnsatisfiable(g.Rules["R.UNREACHABLE"].When)
			if proven != tc.proven {
				t.Errorf("proveUnsatisfiable(%q) = (%q, %v), want proven=%v",
					tc.cond, got, proven, tc.proven)
			}
		})
	}
}
