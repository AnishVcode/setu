package main

import (
	"fmt"
	"strings"

	"github.com/setu-platform/setu/pkg/testkit"
)

func runTest(args []string) error {
	fs := flagSet("test")
	asOf := fs.String("as-of", "", "date to anchor evaluations to (YYYY-MM-DD)")
	cases := fs.Int("property-cases", 500, "number of generated profiles for the property suite")
	seed := fs.Int64("seed", 20260101, "generator seed; a fixed seed makes a failure reproducible")
	skipProps := fs.Bool("no-properties", false, "run only the authored rule tests")
	verbose := fs.Bool("v", false, "list every test, not only failures")
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
		return fmt.Errorf("%d error(s); fix them before running tests", n)
	}

	failed := false

	// 1. Authored cases. Spec 5.5.5 makes these a precondition of publication.
	rep := testkit.RunRuleTests(l.Prog, anchor, l.Layers)
	for _, r := range rep.Results {
		if r.Passed && !*verbose {
			continue
		}
		status := "PASS"
		if !r.Passed {
			status = "FAIL"
		}
		fmt.Printf("%s  %s / %s\n", status, r.RuleID, r.TestName)
		if !r.Passed {
			fmt.Printf("      at %s\n", r.Pos)
			fmt.Printf("      expected: %s\n      actual:   %s\n", r.Expected, r.Actual)
			if r.Detail != "" {
				for _, line := range strings.Split(r.Detail, "\n") {
					fmt.Printf("      %s\n", line)
				}
			}
		}
	}
	fmt.Printf("\nauthored rule tests: %d passed, %d failed\n", rep.Passed, rep.Failed)
	if rep.Failed > 0 {
		failed = true
	}

	// 2. Coverage. A rule without both a positive and a negative case is not
	// tested, however many cases it has.
	if gaps := testkit.CoverageGaps(l.Prog); len(gaps) > 0 {
		fmt.Printf("\ntest coverage gaps (%d):\n", len(gaps))
		for _, g := range gaps {
			fmt.Println("  " + g)
		}
		failed = true
	} else {
		fmt.Println("test coverage: every rule has a positive and a negative case")
	}

	// 3. Properties that must hold whatever the encoded law says.
	if !*skipProps {
		cfg := testkit.DefaultPropertyConfig(anchor)
		cfg.Cases = *cases
		cfg.Seed = *seed
		cfg.Layers = l.Layers
		prep := testkit.RunProperties(l.Prog, cfg)
		fmt.Printf("\nproperty suite: %d generated profiles, %d violation(s)\n",
			prep.Cases, len(prep.Violations))
		for _, v := range prep.Violations {
			fmt.Printf("\n  VIOLATION [%s] %s\n", v.Property, v.Detail)
			for _, line := range strings.Split(v.Profile, "\n") {
				fmt.Printf("    %s\n", line)
			}
			failed = true
		}
		if len(prep.SkippedMonotonicity) > 0 {
			fmt.Printf("\n  note: the monotonicity property was not checked because these rules "+
				"call defined(), which is not monotone in information by design:\n    %s\n",
				strings.Join(prep.SkippedMonotonicity, ", "))
		}
	}

	if failed {
		return fmt.Errorf("test suite failed")
	}
	fmt.Println("\nall tests passed")
	return nil
}
