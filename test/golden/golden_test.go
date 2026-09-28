// Package golden holds the platform's most important regression suite.
//
// Each case is a real applicant profile with an expert-verified expected
// output. Spec 12 makes this the suite that gates a release: any change to what
// the engine tells one of these applicants must be explicitly approved by
// someone qualified to say the new answer is the lawful one, not silently
// accepted because the code compiled.
//
// The expectations are stored as readable JSON, not as opaque fixtures,
// precisely so that a legal reviewer can read a diff and say whether the law
// now says something different. Regenerate with:
//
//	go test ./test/golden -update
//
// and then have the diff reviewed. Regenerating without review defeats the
// entire purpose of the suite.
package golden

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/setu-platform/setu/pkg/compile"
	"github.com/setu-platform/setu/pkg/eval"
	"github.com/setu-platform/setu/pkg/kg"
	"github.com/setu-platform/setu/pkg/sema"
	"github.com/setu-platform/setu/pkg/srl"
	"github.com/setu-platform/setu/pkg/value"
)

var update = flag.Bool("update", false,
	"rewrite the expected outputs; the resulting diff MUST be reviewed by counsel")

const (
	contentDir  = "../../content/jurisdictions/IN-TN"
	profilesDir = "../../content/goldens"
	expectedDir = "../../content/goldens/expected"
)

// asOf anchors every golden case. It is a constant, not the current date, so
// that the suite gives the same answer on every run and in every year: an
// effective-dated ruleset evaluated against "today" would start failing on its
// own the moment a rule came into force.
var asOf = value.Date{Year: 2026, Month: 9, Day: 4}

// Expectation is the legally meaningful part of a determination, in the form a
// reviewer reads.
type Expectation struct {
	Note string `json:"_note"`

	Hash         string   `json:"determinationHash"`
	Complete     bool     `json:"complete"`
	Approvals    []string `json:"approvalsRequired"`
	NotRequired  []string `json:"explicitlyExempt"`
	Undetermined []string `json:"undetermined"`

	Documents   []string `json:"documents"`
	FeeTotal    string   `json:"feeTotal"`
	FeeComplete bool     `json:"feeTotalIsComplete"`
	FeeLines    []string `json:"feeLines"`

	CriticalPathDays int      `json:"criticalPathDays"`
	SequentialDays   int      `json:"sequentialDays"`
	CriticalPath     []string `json:"criticalPath"`

	Obligations []string `json:"obligations"`
	Incentives  []string `json:"incentives"`
	Unresolved  []string `json:"questionsStillOpen"`
	Conflicts   []string `json:"conflicts"`
	Flags       []string `json:"riskFlags"`

	// Reasons records the citizen-facing explanation for each required
	// approval, because a change in wording is a change in what the state told
	// a person and is exactly what a reviewer needs to see.
	Reasons map[string]string `json:"reasonsGiven"`
	// Citations records the statutory basis shown for each approval.
	Citations map[string]string `json:"citations"`
}

func TestGoldenScenarios(t *testing.T) {
	prog, layers := loadContent(t)
	e := eval.New(prog)

	entries, err := os.ReadDir(profilesDir)
	if err != nil {
		t.Fatalf("cannot read %s: %v", profilesDir, err)
	}
	var names []string
	for _, en := range entries {
		if en.IsDir() || !strings.HasSuffix(en.Name(), ".json") {
			continue
		}
		names = append(names, en.Name())
	}
	sort.Strings(names)
	if len(names) == 0 {
		t.Fatal("no golden profiles found")
	}

	for _, name := range names {
		t.Run(strings.TrimSuffix(name, ".json"), func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(profilesDir, name))
			if err != nil {
				t.Fatal(err)
			}
			p, err := eval.ParseProfileJSON(prog, raw)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}

			d := e.Evaluate(
				eval.Input{Profile: p, AsOf: asOf, Language: "en", Layers: layers},
				eval.Options{RulesetVersion: "golden"},
			)
			got := summarise(d)

			path := filepath.Join(expectedDir, name)
			if *update {
				writeExpectation(t, path, got)
				t.Logf("rewrote %s — have the diff reviewed before merging", path)
				return
			}

			wantRaw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("no expected output for %s. Generate it with "+
					"`go test ./test/golden -update` and have the result reviewed: %v", name, err)
			}
			var want Expectation
			if err := json.Unmarshal(wantRaw, &want); err != nil {
				t.Fatalf("%s: %v", path, err)
			}
			compare(t, want, got)
		})
	}
}

func loadContent(t *testing.T) (*compile.Program, *value.Layers) {
	t.Helper()
	g, diags := kg.LoadDir(contentDir)
	failOnErrors(t, diags, "loading content")
	failOnErrors(t, sema.Check(g), "checking content")
	prog, diags := compile.Compile(g)
	failOnErrors(t, diags, "compiling content")
	layers, err := kg.LoadLayers(contentDir)
	if err != nil {
		t.Fatalf("loading boundary layers: %v", err)
	}
	return prog, layers
}

func failOnErrors(t *testing.T, diags []srl.Diagnostic, phase string) {
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

func summarise(d *eval.Determination) Expectation {
	exp := Expectation{
		Note: "Expert-verified expected output. A diff here is a change in what " +
			"the state tells this applicant and must be approved by counsel before release.",
		Hash:             d.Hash,
		Complete:         d.IsComplete(),
		FeeTotal:         d.FeeTotal,
		FeeComplete:      d.FeeComplete,
		CriticalPathDays: d.DAG.TotalDays,
		SequentialDays:   d.DAG.SequentialDays,
		CriticalPath:     d.DAG.CriticalPath,
		Reasons:          map[string]string{},
		Citations:        map[string]string{},
	}
	for _, a := range d.Approvals {
		exp.Approvals = append(exp.Approvals, a.ID)
		exp.Reasons[a.ID] = a.Reason
		exp.Citations[a.ID] = a.LegalBasis
	}
	for _, st := range d.Statuses() {
		switch {
		case st.Exempted:
			exp.NotRequired = append(exp.NotRequired, st.ID+" ("+st.DecidedBy+")")
		case st.Required == value.Unknown:
			exp.Undetermined = append(exp.Undetermined, st.ID)
		}
	}
	for _, doc := range d.Documents {
		exp.Documents = append(exp.Documents, doc.ID)
	}
	for _, f := range d.Fees {
		exp.FeeLines = append(exp.FeeLines, f.FeeID+" = "+f.Amount)
	}
	for _, o := range d.Obligations {
		exp.Obligations = append(exp.Obligations, o.ID)
	}
	for _, m := range d.Incentives {
		exp.Incentives = append(exp.Incentives, m.ID+" = "+m.Value)
	}
	for _, u := range d.Unresolved {
		exp.Unresolved = append(exp.Unresolved, u.Var)
	}
	for _, c := range d.Conflicts {
		exp.Conflicts = append(exp.Conflicts, c.ApprovalID)
	}
	exp.Flags = d.Flags
	return exp
}

func writeExpectation(t *testing.T, path string, exp Expectation) {
	t.Helper()
	b, err := json.MarshalIndent(exp, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

// compare reports every difference rather than stopping at the first, so a
// reviewer sees the whole change in one place.
func compare(t *testing.T, want, got Expectation) {
	t.Helper()

	slice := func(field string, w, g []string) {
		if equalStrings(w, g) {
			return
		}
		added, removed := diff(w, g)
		t.Errorf("%s changed:\n  added:   %v\n  removed: %v", field, added, removed)
	}
	slice("approvals required", want.Approvals, got.Approvals)
	slice("explicit exemptions", want.NotRequired, got.NotRequired)
	slice("undetermined approvals", want.Undetermined, got.Undetermined)
	slice("documents", want.Documents, got.Documents)
	slice("fee lines", want.FeeLines, got.FeeLines)
	slice("obligations", want.Obligations, got.Obligations)
	slice("incentives", want.Incentives, got.Incentives)
	slice("questions still open", want.Unresolved, got.Unresolved)
	slice("conflicts", want.Conflicts, got.Conflicts)
	slice("risk flags", want.Flags, got.Flags)
	slice("critical path", want.CriticalPath, got.CriticalPath)

	if want.FeeTotal != got.FeeTotal {
		t.Errorf("fee total changed: %s → %s", want.FeeTotal, got.FeeTotal)
	}
	if want.FeeComplete != got.FeeComplete {
		t.Errorf("fee completeness changed: %v → %v", want.FeeComplete, got.FeeComplete)
	}
	if want.Complete != got.Complete {
		t.Errorf("determination completeness changed: %v → %v", want.Complete, got.Complete)
	}
	if want.CriticalPathDays != got.CriticalPathDays {
		t.Errorf("critical path changed: %d → %d days", want.CriticalPathDays, got.CriticalPathDays)
	}
	if want.SequentialDays != got.SequentialDays {
		t.Errorf("sequential total changed: %d → %d days", want.SequentialDays, got.SequentialDays)
	}

	for id, w := range want.Reasons {
		g, ok := got.Reasons[id]
		if !ok {
			continue // already reported as a missing approval
		}
		if w != g {
			t.Errorf("the reason given to the applicant for %s changed:\n  was: %s\n  now: %s",
				id, w, g)
		}
	}
	for id, w := range want.Citations {
		if g, ok := got.Citations[id]; ok && w != g {
			t.Errorf("the citation shown for %s changed:\n  was: %s\n  now: %s", id, w, g)
		}
	}

	if want.Hash != got.Hash && !t.Failed() {
		t.Errorf("the determination hash changed (%s → %s) although every field "+
			"checked above is identical; something outside the summarised fields moved",
			want.Hash, got.Hash)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func diff(want, got []string) (added, removed []string) {
	w := map[string]bool{}
	for _, s := range want {
		w[s] = true
	}
	g := map[string]bool{}
	for _, s := range got {
		g[s] = true
	}
	for _, s := range got {
		if !w[s] {
			added = append(added, s)
		}
	}
	for _, s := range want {
		if !g[s] {
			removed = append(removed, s)
		}
	}
	return added, removed
}
