package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/setu-platform/setu/pkg/eval"
)

func runEval(args []string) error {
	fs := flagSet("eval")
	profilePath := fs.String("profile", "", "path to a profile JSON file (required)")
	asOf := fs.String("as-of", "", "date the determination is anchored to (YYYY-MM-DD)")
	lang := fs.String("lang", "en", "language for citizen-facing text")
	asJSON := fs.Bool("json", false, "emit the determination as JSON")
	trace := fs.Bool("trace", false, "include the full derivation trace")
	if err := fs.Parse(args); err != nil {
		return err
	}
	dir := contentDir(fs, "content/jurisdictions/IN-TN")

	if *profilePath == "" {
		return fmt.Errorf("--profile is required")
	}
	anchor, err := parseAsOf(*asOf)
	if err != nil {
		return err
	}

	l, err := load(dir, loadOptions{})
	if err != nil {
		return err
	}
	if n := l.Errors(); n > 0 {
		printDiagnostics(l, false)
		return fmt.Errorf("%d error(s); the ruleset cannot be evaluated", n)
	}

	raw, err := os.ReadFile(*profilePath)
	if err != nil {
		return err
	}
	p, err := eval.ParseProfileJSON(l.Prog, raw)
	if err != nil {
		return fmt.Errorf("%s: %w", *profilePath, err)
	}

	e := eval.New(l.Prog)
	d := e.Evaluate(
		eval.Input{Profile: p, AsOf: anchor, Language: *lang, Layers: l.Layers},
		eval.Options{Trace: *trace, RulesetVersion: "working-copy"},
	)

	if *asJSON {
		b, err := d.JSON()
		if err != nil {
			return err
		}
		fmt.Println(string(b))
		return nil
	}
	printDetermination(d)
	return nil
}

// printDetermination renders a determination the way the applicant console
// shows it: what you need, why, from whom, by when, and what is still unknown.
func printDetermination(d *eval.Determination) {
	rule := strings.Repeat("─", 78)

	fmt.Println(rule)
	fmt.Printf("DETERMINATION  %s   as of %s   ruleset %s\n", d.Package, d.AsOf, d.RulesetVersion)
	fmt.Printf("hash %s\n", d.Hash)
	fmt.Println(rule)

	if len(d.Approvals) == 0 {
		fmt.Println("\nNo approvals are required on the information given.")
	} else {
		fmt.Printf("\nAPPROVALS REQUIRED (%d)\n\n", len(d.Approvals))
		for i, a := range d.Approvals {
			fmt.Printf("%2d. %s\n", i+1, a.Title)
			fmt.Printf("    id          %s\n", a.ID)
			dept := a.DepartmentName
			if dept == "" {
				dept = a.Department
			}
			fmt.Printf("    department  %s\n", dept)
			fmt.Printf("    timeline    %s (statutory)\n", a.SLA)
			if a.Validity != "" {
				fmt.Printf("    validity    %s\n", a.Validity)
			}
			fmt.Printf("    legal basis %s\n", a.LegalBasis)
			if a.Reason != "" {
				fmt.Printf("    why         %s\n", wrap(a.Reason, 62, "                "))
			}
			if a.DeemedPermitted {
				fmt.Printf("    deemed      permitted under %s if the department does not act in time\n",
					a.DeemedSection)
			}
			for _, dep := range a.DependsOn {
				fmt.Printf("    waits for   %s", dep.Title)
				if dep.Reason != "" {
					fmt.Printf(" — %s", dep.Reason)
				}
				fmt.Println()
			}
			if a.AppealAuthority != "" {
				fmt.Printf("    appeal      %s, within %s\n", a.AppealAuthority, a.AppealWithin)
			}
			fmt.Println()
		}
	}

	if len(d.DAG.Nodes) > 0 {
		fmt.Println(rule)
		fmt.Printf("TIMELINE\n\n")
		fmt.Printf("  In parallel:  %d working days along the critical path\n", d.DAG.TotalDays)
		fmt.Printf("  One by one:   %d days\n", d.DAG.SequentialDays)
		if d.DAG.SequentialDays > 0 {
			saved := d.DAG.SequentialDays - d.DAG.TotalDays
			fmt.Printf("  Saved:        %d days (%d%%)\n", saved,
				saved*100/d.DAG.SequentialDays)
		}
		fmt.Printf("\n  Critical path: %s\n", strings.Join(d.DAG.CriticalPath, " → "))
		for i, wave := range d.DAG.Waves {
			fmt.Printf("  Wave %d (dispatch together): %s\n", i+1, strings.Join(wave, ", "))
		}
		fmt.Println()
	}

	if len(d.Documents) > 0 {
		fmt.Println(rule)
		fmt.Printf("DOCUMENTS (%d, collected once and reused)\n\n", len(d.Documents))
		for _, doc := range d.Documents {
			fmt.Printf("  %-28s %s\n", doc.ID, doc.Title)
			fmt.Printf("  %-28s verification: %s", "", doc.Verification)
			if doc.SourceSystem != "" {
				fmt.Printf(" via %s", doc.SourceSystem)
			}
			fmt.Println()
			fmt.Printf("  %-28s used by: %s\n", "", strings.Join(doc.RequiredFor, ", "))
		}
		fmt.Println()
	}

	if len(d.Fees) > 0 {
		fmt.Println(rule)
		fmt.Printf("FEES\n\n")
		for _, f := range d.Fees {
			fmt.Printf("  %-46s %14s", f.Title, f.Amount)
			if !f.Computed {
				fmt.Printf("  (needs: %s)", strings.Join(f.Unresolved, ", "))
			}
			fmt.Println()
			fmt.Printf("  %-46s head %s\n", "", f.Head)
		}
		fmt.Printf("\n  %-46s %14s", "TOTAL", d.FeeTotal)
		if !d.FeeComplete {
			fmt.Print("  (incomplete)")
		}
		fmt.Println()
		fmt.Println()
	}

	if len(d.Obligations) > 0 {
		fmt.Println(rule)
		fmt.Printf("ONGOING OBLIGATIONS (%d)\n\n", len(d.Obligations))
		for _, o := range d.Obligations {
			sched := o.Cadence
			if o.DueMonth > 0 {
				sched += fmt.Sprintf(", due %02d-%02d", o.DueDay, o.DueMonth)
			} else if o.DueWithin != "" {
				sched += ", within " + o.DueWithin
			}
			fmt.Printf("  %-52s %s\n", o.Title, sched)
		}
		fmt.Println()
	}

	if len(d.Incentives) > 0 {
		fmt.Println(rule)
		fmt.Printf("INCENTIVES YOU APPEAR ELIGIBLE FOR (%d)\n\n", len(d.Incentives))
		for _, m := range d.Incentives {
			fmt.Printf("  %-52s %14s\n", m.Title, m.Value)
			if m.Reason != "" {
				fmt.Printf("    %s\n", wrap(m.Reason, 70, "    "))
			}
			if m.WindowTo != "" {
				fmt.Printf("    apply before %s\n", m.WindowTo)
			}
		}
		fmt.Println()
	}

	if len(d.Unresolved) > 0 {
		fmt.Println(rule)
		fmt.Printf("STILL TO ANSWER (%d)\n\n", len(d.Unresolved))
		fmt.Print("  These are not optional details. Each one changes what you need.\n\n")
		for _, u := range d.Unresolved {
			fmt.Printf("  • %s\n", u.Question)
			if u.Hint != "" {
				fmt.Printf("    %s\n", u.Hint)
			}
			var affects []string
			affects = append(affects, u.AffectsApprovals...)
			affects = append(affects, u.AffectsFees...)
			affects = append(affects, u.AffectsIncentives...)
			sort.Strings(affects)
			if len(affects) > 0 {
				fmt.Printf("    determines: %s\n", strings.Join(affects, ", "))
			}
			fmt.Println()
		}
	}

	if len(d.Conflicts) > 0 {
		fmt.Println(rule)
		fmt.Printf("CONFLICTS IN THE ENCODED LAW (%d) — referred to a human\n\n", len(d.Conflicts))
		for _, c := range d.Conflicts {
			fmt.Printf("  %s at precedence %d\n", c.ApprovalID, c.Precedence)
			fmt.Printf("    requires: %s\n", strings.Join(c.RequireRules, ", "))
			fmt.Printf("    exempts:  %s\n", strings.Join(c.ExemptRules, ", "))
			fmt.Printf("    %s\n\n", c.Resolution)
		}
	}

	if len(d.Flags) > 0 {
		fmt.Println(rule)
		fmt.Printf("RISK SIGNALS: %s\n\n", strings.Join(d.Flags, ", "))
	}

	fmt.Println(rule)
	if d.IsComplete() {
		fmt.Println("This determination is complete on the information given.")
	} else {
		fmt.Println("This determination is INCOMPLETE. The questions above must be answered")
		fmt.Println("before the requirement set can be relied on.")
	}
	fmt.Println(rule)
}

// wrap re-flows text to a width with a hanging indent.
func wrap(s string, width int, indent string) string {
	words := strings.Fields(s)
	if len(words) == 0 {
		return ""
	}
	var lines []string
	cur := words[0]
	for _, w := range words[1:] {
		if len(cur)+1+len(w) > width {
			lines = append(lines, cur)
			cur = w
			continue
		}
		cur += " " + w
	}
	lines = append(lines, cur)
	return strings.Join(lines, "\n"+indent)
}
