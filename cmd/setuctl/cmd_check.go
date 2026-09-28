package main

import (
	"fmt"
	"strings"

	"github.com/setu-platform/setu/pkg/sema"
	"github.com/setu-platform/setu/pkg/srl"
	"github.com/setu-platform/setu/pkg/value"
)

func runCheck(args []string) error {
	fs := flagSet("check")
	asOf := fs.String("as-of", "", "date to evaluate staleness against (YYYY-MM-DD)")
	langs := fs.String("languages", "en", "comma-separated languages that must be complete")
	strict := fs.Bool("strict", false, "treat governance findings as errors, as the production publish gate does")
	quiet := fs.Bool("quiet", false, "print only the summary")
	showInfo := fs.Bool("info", false, "include informational notes")
	if err := fs.Parse(args); err != nil {
		return err
	}
	dir := contentDir(fs, "content/jurisdictions/IN-TN")

	today, err := parseAsOf(*asOf)
	if err != nil {
		return err
	}

	l, err := load(dir, loadOptions{
		Lint: true,
		LintCfg: sema.LintConfig{
			Languages:       splitCSV(*langs),
			RequireVerified: *strict,
			Today:           today,
		},
	})
	if err != nil {
		return err
	}

	if !*quiet {
		printDiagnostics(l, *showInfo)
	}

	fmt.Printf("\n%s: %s\n", l.Graph.Package, l.Graph.CountsString())
	if l.Prog != nil && len(l.Prog.Order) > 0 {
		fmt.Printf("evaluation order: %d nodes, acyclic\n", len(l.Prog.Order))
	}
	if l.Layers != nil {
		fmt.Printf("%s\n", l.Layers.Describe())
	} else {
		fmt.Println("boundary layers: none loaded (geospatial predicates will return unknown)")
	}
	// The governance posture is what a department signing off actually reads.
	fmt.Printf("governance: %s\n", summariseGovernance(l, today))

	if n := l.Errors(); n > 0 {
		return fmt.Errorf("%d error(s); the ruleset cannot be compiled or published", n)
	}
	fmt.Println("\ncheck passed")
	return nil
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// summariseGovernance reports the content-governance posture of a ruleset,
// which is what a department signing off needs to see at a glance.
func summariseGovernance(l *loaded, today value.Date) string {
	var b strings.Builder
	counts := map[srl.ReviewStatus]int{}
	stale := 0
	for _, id := range l.Graph.RuleIDs() {
		g := l.Graph.Rules[id].Governance
		counts[g.ReviewStatus]++
		if !g.LastVerified.IsZero() && !g.ReviewCadence.IsZero() && !today.IsZero() {
			if today.After(g.LastVerified.AddPeriod(g.ReviewCadence)) {
				stale++
			}
		}
	}
	b.WriteString(fmt.Sprintf("rules: %d verified, %d in review, %d unverified, %d deprecated",
		counts[srl.StatusVerified], counts[srl.StatusInReview],
		counts[srl.StatusUnverified], counts[srl.StatusDeprecated]))
	if stale > 0 {
		b.WriteString(fmt.Sprintf("; %d past their review date", stale))
	}
	return b.String()
}
