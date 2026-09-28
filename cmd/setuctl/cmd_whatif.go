package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/setu-platform/setu/pkg/discovery"
	"github.com/setu-platform/setu/pkg/eval"
)

func runWhatIf(args []string) error {
	fs := flagSet("whatif")
	var profiles multiFlag
	fs.Var(&profiles, "profile", "a scenario profile JSON file; repeat the flag to compare several")
	asOf := fs.String("as-of", "", "date the determinations are anchored to (YYYY-MM-DD)")
	lang := fs.String("lang", "en", "language for citizen-facing text")
	if err := fs.Parse(args); err != nil {
		return err
	}
	dir := contentDir(fs, "content/jurisdictions/IN-TN")

	if len(profiles) < 2 {
		return fmt.Errorf("give at least two --profile files to compare")
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

	var named []discovery.NamedProfile
	for _, path := range profiles {
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		p, err := eval.ParseProfileJSON(l.Prog, raw)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		named = append(named, discovery.NamedProfile{Name: name, Profile: p})
	}

	cmp := discovery.Compare(
		eval.New(l.Prog),
		eval.Input{AsOf: anchor, Language: *lang, Layers: l.Layers},
		eval.Options{},
		named,
	)

	printComparison(cmp)
	return nil
}

func printComparison(c *discovery.Comparison) {
	names := make([]string, len(c.Summary))
	w := 12
	for i, s := range c.Summary {
		names[i] = s.Name
		if len(s.Name) > w {
			w = len(s.Name)
		}
	}
	line := strings.Repeat("─", 34+(w+3)*len(names))

	fmt.Println(line)
	fmt.Printf("%-32s", "WHAT-IF COMPARISON")
	for _, n := range names {
		fmt.Printf("  %*s", w, n)
	}
	fmt.Println()
	fmt.Println(line)

	row := func(label string, vals []string) {
		fmt.Printf("%-32s", label)
		for _, v := range vals {
			fmt.Printf("  %*s", w, v)
		}
		fmt.Println()
	}

	var approvals, fees, critical, sequential, docs, incentives, unresolved []string
	for _, s := range c.Summary {
		approvals = append(approvals, fmt.Sprintf("%d", s.ApprovalCount))
		f := s.FeeTotal
		if !s.FeeComplete {
			f += "*"
		}
		fees = append(fees, f)
		critical = append(critical, fmt.Sprintf("%d d", s.CriticalDays))
		sequential = append(sequential, fmt.Sprintf("%d d", s.SequentialDays))
		docs = append(docs, fmt.Sprintf("%d", s.DocumentCount))
		incentives = append(incentives, fmt.Sprintf("%d", s.IncentiveCount))
		unresolved = append(unresolved, fmt.Sprintf("%d", s.UnresolvedCount))
	}
	row("approvals required", approvals)
	row("total fees", fees)
	row("time, dispatched in parallel", critical)
	row("time, one after another", sequential)
	row("documents needed", docs)
	row("incentives matched", incentives)
	row("questions still open", unresolved)

	fmt.Println(line)
	if len(c.Differences) == 0 {
		fmt.Println("\nThe approval set is identical across these scenarios.")
	} else {
		fmt.Printf("\nWHAT CHANGES (%d approval(s))\n\n", len(c.Differences))
		for _, r := range c.Rows {
			if !r.Differs {
				continue
			}
			label := r.Title
			if label == "" {
				label = r.ID
			}
			if len(label) > 30 {
				label = label[:29] + "…"
			}
			var vals []string
			for _, s := range r.Statuses {
				vals = append(vals, s.Text())
			}
			row("  "+label, vals)
		}
	}

	fmt.Println()
	if i, ok := c.Cheapest(); ok {
		fmt.Printf("  lowest fees:      %s\n", c.Summary[i].Name)
	}
	if i, ok := c.Fastest(); ok {
		fmt.Printf("  fastest to clear: %s (%d days)\n", c.Summary[i].Name, c.Summary[i].CriticalDays)
	}
	if i, ok := c.Simplest(); ok {
		fmt.Printf("  fewest approvals: %s (%d)\n", c.Summary[i].Name, c.Summary[i].ApprovalCount)
	}
	fmt.Println()
	fmt.Println("  * an incomplete total: some fees cannot be computed until the open")
	fmt.Println("    questions above are answered.")
}

// multiFlag collects a repeated string flag.
type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ", ") }

func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}
