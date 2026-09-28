package eval

import (
	"strings"
	"testing"

	"github.com/setu-platform/setu/pkg/compile"
	"github.com/setu-platform/setu/pkg/kg"
	"github.com/setu-platform/setu/pkg/sema"
	"github.com/setu-platform/setu/pkg/srl"
	"github.com/setu-platform/setu/pkg/unit"
	"github.com/setu-platform/setu/pkg/value"
)

// testRules exercises every mechanism the engine has: precedence, exemption,
// dependency propagation, cross-rule reads, unit-aware thresholds, slab fees,
// obligations and incentives.
const testRules = `
package IN.TN

enum Category { GREEN ORANGE RED }

profile {
  var workers: Quantity<Count>      { question en "How many workers?" unit workers }
  var uses_power: Bool              { question en "Will you use power-driven machinery?" }
  var water_draw: Quantity<VolumeFlow> { question en "Daily water draw?" unit KLD }
  var category: Enum<Category>      { question en "Pollution category?" }
  var investment: Money             { question en "Investment in plant and machinery?" }
  var has_boiler: Bool              { question en "Will you operate a boiler?" }
}

department D.LABOUR  { name en "Labour" jurisdiction IN.TN adapter_tier api }
department D.TNPCB   { name en "Pollution Control Board" jurisdiction IN.TN adapter_tier api }
department D.FIRE    { name en "Fire and Rescue" jurisdiction IN.TN adapter_tier rpa }
department D.PLANNING { name en "Planning" jurisdiction IN.TN adapter_tier api }
department D.BOILERS { name en "Boilers" jurisdiction IN.TN adapter_tier assisted_manual }

document DOC.PLAN  { title en "Layout plan" issuer "Applicant" verification officer }
document DOC.DEED  { title en "Land deed" issuer "Registration Department" verification api "TN_REGISTRATION" }

fee FEE.FACTORY {
  title en "Factory licence fee"
  head "0230-01-800-AB"
  department D.LABOUR
  amount = slab profile.workers {
    <= 20 workers -> INR 1000
    <= 50 workers -> INR 2500
    else          -> INR 5000
  }
  legal_basis { act "Factories Act, 1948" section "6" }
}

obligation OBL.ANNUAL {
  title en "Annual return"
  cadence 1 year
  due month 1 day 31
  department D.LABOUR
  consequence en "Penalty under Section 92."
  legal_basis { act "Factories Act, 1948" section "88" }
}

incentive INC.SUBSIDY {
  title en "Capital subsidy"
  scheme "TN MSME"
  department D.LABOUR
  value = min(0.25 * profile.investment, INR 30 lakh)
  window from 2020-01-01 until 2030-01-01
}

approval A.PLANNING {
  title en "Building plan approval"
  category "permit"
  department D.PLANNING
  sla 30 days
  documents [ DOC.PLAN, DOC.DEED ]
  legal_basis { act "Town and Country Planning Act, 1971" section "49" }
}

approval A.FACTORY {
  title en "Factory licence"
  category "licence"
  department D.LABOUR
  sla 30 days
  validity 1 year
  renewal { notice 60 days cadence 1 year }
  deemed_approval permitted section "6(3)"
  documents [ DOC.PLAN ]
  fees [ FEE.FACTORY ]
  obligations [ OBL.ANNUAL ]
  legal_basis { act "Factories Act, 1948" section "6" }
}

approval A.FIRE {
  title en "Fire NOC"
  category "noc"
  department D.FIRE
  sla 45 days
  depends_on {
    A.PLANNING { reason en "The fire service assesses the approved building plan." }
  }
  legal_basis { act "TN Fire Service Act, 1985" section "13" }
}

approval A.CONSENT {
  title en "Consent to establish"
  category "consent"
  department D.TNPCB
  sla 60 days
  legal_basis { act "Water Act, 1974" section "25" }
}

approval A.BOILER {
  title en "Boiler registration"
  category "registration"
  department D.BOILERS
  sla 21 days
  legal_basis { act "Boilers Act, 1923" section "7" }
}

rule R.FACTORY {
  kind trigger
  when profile.uses_power == true and profile.workers >= 10 workers
  then { require approval A.FACTORY }
  explain en "You will employ {profile.workers} with power-driven machinery."
  legal_basis { act "Factories Act, 1948" section "2(m)" }
  test "ten with power" { given { profile.workers = 10 workers, profile.uses_power = true } expect fires }
  test "nine with power" { given { profile.workers = 9 workers, profile.uses_power = true } expect not fires }
}

rule R.FACTORY.EXEMPT {
  kind exemption
  precedence 200
  overrides [ R.FACTORY ]
  when profile.category == Category.GREEN and profile.workers < 20 workers
  then { exempt approval A.FACTORY reason en "Small green-category units are exempt." }
  explain en "Small green-category units are exempt."
  legal_basis { act "Factories Act, 1948" section "85" }
  test "green small" { given { profile.category = Category.GREEN, profile.workers = 12 workers } expect fires }
  test "red small"   { given { profile.category = Category.RED, profile.workers = 12 workers } expect not fires }
}

rule R.FIRE {
  kind trigger
  when required(A.FACTORY)
  then { require approval A.FIRE }
  explain en "A fire NOC is required for a licensed factory."
  legal_basis { act "TN Fire Service Act, 1985" section "13" }
  test "with factory" { given { profile.workers = 30 workers, profile.uses_power = true } expect fires }
  test "without factory" { given { profile.workers = 2 workers, profile.uses_power = false } expect not fires }
}

rule R.CONSENT {
  kind trigger
  when profile.water_draw > 25 KLD or profile.category == Category.RED
  then { require approval A.CONSENT }
  explain en "Your unit draws {profile.water_draw} and falls in category {profile.category}."
  legal_basis { act "Water Act, 1974" section "25" }
  test "high water" { given { profile.water_draw = 60 KLD } expect fires }
  test "low water green" { given { profile.water_draw = 5 KLD, profile.category = Category.GREEN } expect not fires }
}

rule R.BOILER {
  kind trigger
  when profile.has_boiler == true
  then { require approval A.BOILER }
  explain en "You will operate a boiler."
  legal_basis { act "Boilers Act, 1923" section "7" }
  test "boiler" { given { profile.has_boiler = true } expect fires }
  test "no boiler" { given { profile.has_boiler = false } expect not fires }
}

rule R.SUBSIDY {
  kind incentive
  when profile.investment >= INR 1 crore and required(A.FACTORY)
  then { offer incentive INC.SUBSIDY }
  explain en "Your investment of {profile.investment} qualifies for the capital subsidy."
  legal_basis { act "TN MSME Policy, 2021" section "4.2" }
  test "qualifies" { given { profile.investment = INR 4 crore, profile.workers = 25 workers, profile.uses_power = true } expect fires }
  test "too small" { given { profile.investment = INR 5 lakh } expect not fires }
}
`

func build(t *testing.T, src string) *Engine {
	t.Helper()
	g, diags := kg.LoadString("test.srl", src)
	fatalOnError(t, diags, "load")
	fatalOnError(t, sema.Check(g), "check")
	prog, diags := compile.Compile(g)
	fatalOnError(t, diags, "compile")
	return New(prog)
}

func fatalOnError(t *testing.T, diags []srl.Diagnostic, phase string) {
	t.Helper()
	var msgs []string
	for _, d := range diags {
		if d.Severity == srl.SevError {
			msgs = append(msgs, d.String())
		}
	}
	if len(msgs) > 0 {
		t.Fatalf("%s failed:\n%s", phase, strings.Join(msgs, "\n"))
	}
}

func kld(n int64) value.Value     { return value.Quantity(ratOf(n), unit.KLD) }
func workers(n int64) value.Value { return value.Quantity(ratOf(n), unit.Count) }
func ratOf(n int64) *bigRat       { return newRat(n) }

func day(y, m, d int) value.Date { return value.Date{Year: y, Month: m, Day: d} }

// fullProfile is the worked example of spec 21: a food-processing unit with
// 25 workers, power-driven machinery, 60 KLD of water, a boiler, and INR 4
// crore of investment.
func fullProfile() *Profile {
	return NewProfile("P1").
		Set("workers", workers(25)).
		Set("uses_power", value.Bool(true)).
		Set("water_draw", kld(60)).
		Set("category", value.Enum("Category", "ORANGE")).
		Set("investment", value.MoneyInt(40_000_000)).
		Set("has_boiler", value.Bool(true))
}

func TestFullDetermination(t *testing.T) {
	e := build(t, testRules)
	d := e.Evaluate(Input{Profile: fullProfile(), AsOf: day(2026, 4, 1), Language: "en"},
		Options{Trace: true, RulesetVersion: "1.0.0"})

	want := []string{"A.CONSENT", "A.FACTORY", "A.FIRE", "A.PLANNING", "A.BOILER"}
	got := map[string]bool{}
	for _, a := range d.Approvals {
		got[a.ID] = true
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("expected %s to be required; got %v", w, d.ApprovalIDs())
		}
	}
	if len(d.Approvals) != len(want) {
		t.Errorf("approvals = %v, want exactly %v", d.ApprovalIDs(), want)
	}

	// A.PLANNING is required only because A.FIRE depends on it. That inference
	// is the difference between a checklist and an orchestrator.
	st, _ := d.StatusOf("A.PLANNING")
	if st.ByDependency != "A.FIRE" {
		t.Errorf("A.PLANNING should be required by dependency on A.FIRE, got %+v", st)
	}

	if !d.IsComplete() {
		t.Errorf("determination should be complete, unresolved = %+v conflicts = %+v",
			d.Unresolved, d.Conflicts)
	}

	// Fee: 25 workers falls in the second slab.
	if len(d.Fees) != 1 {
		t.Fatalf("fees = %+v", d.Fees)
	}
	if d.Fees[0].Amount != "INR 2,500" {
		t.Errorf("fee = %q, want INR 2,500", d.Fees[0].Amount)
	}
	if d.FeeTotal != "INR 2,500" || !d.FeeComplete {
		t.Errorf("total = %q complete = %v", d.FeeTotal, d.FeeComplete)
	}

	// Documents are deduplicated across the approvals that need them.
	for _, doc := range d.Documents {
		if doc.ID == "DOC.PLAN" {
			if len(doc.RequiredFor) != 2 {
				t.Errorf("DOC.PLAN required for %v, want both A.FACTORY and A.PLANNING",
					doc.RequiredFor)
			}
		}
	}

	if len(d.Obligations) != 1 || d.Obligations[0].ID != "OBL.ANNUAL" {
		t.Errorf("obligations = %+v", d.Obligations)
	}
	if len(d.Incentives) != 1 || d.Incentives[0].Value != "INR 30,00,000" {
		t.Errorf("incentives = %+v", d.Incentives)
	}

	// The DAG must show parallelism: fire (45d) waits on planning (30d) for a
	// 75-day critical path, against 186 days if run sequentially.
	if d.DAG.TotalDays != 75 {
		t.Errorf("critical path = %d days, want 75 (planning 30 + fire 45)", d.DAG.TotalDays)
	}
	if d.DAG.SequentialDays != 186 {
		t.Errorf("sequential = %d days, want 186", d.DAG.SequentialDays)
	}
	wantPath := []string{"A.PLANNING", "A.FIRE"}
	if strings.Join(d.DAG.CriticalPath, ",") != strings.Join(wantPath, ",") {
		t.Errorf("critical path = %v, want %v", d.DAG.CriticalPath, wantPath)
	}
	if len(d.DAG.Waves) != 2 || len(d.DAG.Waves[0]) != 4 {
		t.Errorf("waves = %v, want 4 approvals dispatchable immediately", d.DAG.Waves)
	}

	// The explanation must interpolate the applicant's own values.
	for _, a := range d.Approvals {
		if a.ID != "A.FACTORY" {
			continue
		}
		if !strings.Contains(a.Reason, "25 workers") {
			t.Errorf("reason = %q, want the worker count interpolated", a.Reason)
		}
		if a.LegalBasis == "" {
			t.Errorf("A.FACTORY has no citation")
		}
		if !a.DeemedPermitted || a.DeemedSection != "6(3)" {
			t.Errorf("deemed approval not carried through: %+v", a)
		}
	}
}

// TestUnknownIsNotFalse is the central guarantee: an unanswered question
// produces an explicit unresolved determination, never a silently dropped
// approval.
func TestUnknownIsNotFalse(t *testing.T) {
	e := build(t, testRules)
	p := NewProfile("P2").
		Set("workers", workers(25)).
		Set("uses_power", value.Bool(true)).
		Set("category", value.Enum("Category", "ORANGE")).
		Set("investment", value.MoneyInt(40_000_000))
	// water_draw and has_boiler are deliberately unanswered.

	d := e.Evaluate(Input{Profile: p, AsOf: day(2026, 4, 1), Language: "en"}, Options{})

	if d.IsComplete() {
		t.Fatal("determination claims to be complete despite unanswered questions")
	}
	st, _ := d.StatusOf("A.BOILER")
	if st.Required != value.Unknown {
		t.Errorf("A.BOILER should be undetermined, got %v", st.Required)
	}
	// A.BOILER must not appear as required, and must not silently vanish either.
	for _, a := range d.Approvals {
		if a.ID == "A.BOILER" {
			t.Error("A.BOILER listed as required when it is undetermined")
		}
	}

	byVar := map[string]UnresolvedDetermination{}
	for _, u := range d.Unresolved {
		byVar[u.Var] = u
	}
	if u, ok := byVar["has_boiler"]; !ok {
		t.Error("has_boiler not reported as unresolved")
	} else {
		if u.Question != "Will you operate a boiler?" {
			t.Errorf("question = %q, want the authored wording", u.Question)
		}
		if len(u.AffectsApprovals) != 1 || u.AffectsApprovals[0] != "A.BOILER" {
			t.Errorf("affects = %v, want [A.BOILER]", u.AffectsApprovals)
		}
	}
	if _, ok := byVar["water_draw"]; !ok {
		t.Error("water_draw not reported as unresolved")
	}
}

// TestRelevanceOfUnknowns checks that the engine only asks questions whose
// answers could change the outcome. Category RED already forces the consent, so
// the unanswered water draw is irrelevant and must not be asked.
func TestRelevanceOfUnknowns(t *testing.T) {
	e := build(t, testRules)
	p := NewProfile("P3").
		Set("workers", workers(5)).
		Set("uses_power", value.Bool(false)).
		Set("category", value.Enum("Category", "RED")).
		Set("has_boiler", value.Bool(false)).
		Set("investment", value.MoneyInt(100000))

	d := e.Evaluate(Input{Profile: p, AsOf: day(2026, 4, 1), Language: "en"}, Options{})

	for _, u := range d.Unresolved {
		if u.Var == "water_draw" {
			t.Errorf("asked about water_draw although category RED already settles the consent: %+v", u)
		}
	}
	st, _ := d.StatusOf("A.CONSENT")
	if st.Required != value.True {
		t.Errorf("A.CONSENT should be required by category RED, got %v", st.Required)
	}
}

// TestExemptionOutranksTrigger checks precedence resolution.
func TestExemptionOutranksTrigger(t *testing.T) {
	e := build(t, testRules)
	p := NewProfile("P4").
		Set("workers", workers(12)).
		Set("uses_power", value.Bool(true)).
		Set("category", value.Enum("Category", "GREEN")).
		Set("water_draw", kld(2)).
		Set("has_boiler", value.Bool(false)).
		Set("investment", value.MoneyInt(100000))

	d := e.Evaluate(Input{Profile: p, AsOf: day(2026, 4, 1), Language: "en"}, Options{})
	st, _ := d.StatusOf("A.FACTORY")
	if st.Required != value.False || !st.Exempted {
		t.Fatalf("A.FACTORY should be exempt, got %+v", st)
	}
	if st.DecidedBy != "R.FACTORY.EXEMPT" {
		t.Errorf("decided by %q, want R.FACTORY.EXEMPT", st.DecidedBy)
	}
	// The trigger rule fired but was outranked; that must be visible.
	var sawOverridden bool
	for _, c := range st.Contributions {
		if c.RuleID == "R.FACTORY" && c.Fired == "true" && c.Overridden {
			sawOverridden = true
		}
	}
	if !sawOverridden {
		t.Errorf("the overridden trigger is not recorded: %+v", st.Contributions)
	}
	// The fire NOC depends on the factory licence via required(), so it falls away.
	if fs, _ := d.StatusOf("A.FIRE"); fs.Required != value.False {
		t.Errorf("A.FIRE should not be required when the factory licence is exempt")
	}
}

// TestDeterminism is the P1 guarantee expressed as a test: the same input must
// produce byte-identical output, run after run, including under Go's
// randomised map iteration.
func TestDeterminism(t *testing.T) {
	e := build(t, testRules)
	first := ""
	for i := 0; i < 50; i++ {
		d := e.Evaluate(Input{Profile: fullProfile(), AsOf: day(2026, 4, 1), Language: "en"},
			Options{Trace: true, RulesetVersion: "1.0.0"})
		b, err := d.JSON()
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = string(b)
			continue
		}
		if string(b) != first {
			t.Fatalf("determination differs on run %d", i)
		}
	}
}

// TestEffectiveDating checks that a rule not yet in force does not apply.
func TestEffectiveDating(t *testing.T) {
	src := strings.Replace(testRules,
		`rule R.BOILER {
  kind trigger`,
		`rule R.BOILER {
  kind trigger
  effective from 2027-01-01`, 1)
	e := build(t, src)
	p := fullProfile()

	before := e.Evaluate(Input{Profile: p, AsOf: day(2026, 6, 1)}, Options{})
	if st, _ := before.StatusOf("A.BOILER"); st.Required == value.True {
		t.Error("boiler registration required before the rule came into force")
	}
	after := e.Evaluate(Input{Profile: p, AsOf: day(2027, 6, 1)}, Options{})
	if st, _ := after.StatusOf("A.BOILER"); st.Required != value.True {
		t.Error("boiler registration not required after the rule came into force")
	}
}

// TestConflictIsReportedNotGuessed checks that two rules of equal precedence
// pulling in opposite directions produce an explicit conflict rather than a
// silent winner.
func TestConflictIsReportedNotGuessed(t *testing.T) {
	src := testRules + `
rule R.FACTORY.CONFLICT {
  kind exemption
  precedence 100
  when profile.uses_power == true
  then { exempt approval A.FACTORY }
  explain en "Conflicting exemption for testing."
  legal_basis { act "Test Act" section "1" }
  test "fires" { given { profile.uses_power = true } expect fires }
  test "not"   { given { profile.uses_power = false } expect not fires }
}
`
	e := build(t, src)
	d := e.Evaluate(Input{Profile: fullProfile(), AsOf: day(2026, 4, 1)}, Options{})
	if len(d.Conflicts) != 1 {
		t.Fatalf("conflicts = %+v, want exactly one", d.Conflicts)
	}
	c := d.Conflicts[0]
	if c.ApprovalID != "A.FACTORY" || len(c.RequireRules) != 1 || len(c.ExemptRules) != 1 {
		t.Errorf("conflict = %+v", c)
	}
	if st, _ := d.StatusOf("A.FACTORY"); st.Required != value.Unknown {
		t.Errorf("a conflicted approval must be undetermined, got %v", st.Required)
	}
	if d.IsComplete() {
		t.Error("a determination with a conflict must not report itself complete")
	}
}

// TestCycleIsRejected checks that circular dependencies are a compile error.
func TestCycleIsRejected(t *testing.T) {
	src := testRules + `
rule R.CYCLE {
  kind dependency
  when true
  then { depend A.PLANNING on A.FIRE }
  explain en "Creates a cycle."
  legal_basis { act "Test Act" section "1" }
  test "fires" { given { } expect fires }
  test "not" { given { } expect not fires }
}
`
	g, diags := kg.LoadString("cycle.srl", src)
	fatalOnError(t, diags, "load")
	_, cdiags := compile.Compile(g)
	var found bool
	for _, d := range cdiags {
		if d.Code == "CMP0001" && strings.Contains(d.Msg, "circular dependency") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a cycle diagnostic, got %v", cdiags)
	}
}
