package main

import (
	"fmt"

	"github.com/setu-platform/setu/pkg/analysis"
)

func runAnalyze(args []string) error {
	fs := flagSet("analyze")
	asOf := fs.String("as-of", "", "date to anchor the analysis to (YYYY-MM-DD)")
	attempts := fs.Int("attempts", 2000, "candidate profiles to try per witness search")
	seed := fs.Int64("seed", 424242, "search seed; a fixed seed makes a finding reproducible")
	if err := fs.Parse(args); err != nil {
		return err
	}
	dir := contentDir(fs, "content/jurisdictions/IN-TN")

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
		return fmt.Errorf("%d error(s); fix them before analysing", n)
	}

	cfg := analysis.DefaultConfig(anchor)
	cfg.Attempts = *attempts
	cfg.Seed = *seed
	cfg.Layers = l.Layers

	rep := analysis.Run(l.Prog, cfg)
	for _, f := range rep.Findings {
		fmt.Println(f)
		fmt.Println()
	}

	proven, searched := 0, 0
	for _, f := range rep.Findings {
		if f.Proven {
			proven++
		} else {
			searched++
		}
	}
	fmt.Printf("analysis: %d finding(s) — %d proven, %d from a search that came up empty\n",
		len(rep.Findings), proven, searched)
	fmt.Printf("  cycles and orphans are decided exactly; unreachability and contradiction\n")
	fmt.Printf("  are proven only where the report says so.\n")

	if n := rep.Errors(); n > 0 {
		return fmt.Errorf("%d blocking finding(s)", n)
	}
	return nil
}
