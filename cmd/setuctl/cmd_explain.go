package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/setu-platform/setu/pkg/eval"
	"github.com/setu-platform/setu/pkg/srl"
	"github.com/setu-platform/setu/pkg/value"
)

func runExplain(args []string) error {
	fs := flagSet("explain")
	profilePath := fs.String("profile", "", "path to a profile JSON file (required)")
	approval := fs.String("approval", "", "the approval to explain (required)")
	asOf := fs.String("as-of", "", "date the determination is anchored to (YYYY-MM-DD)")
	lang := fs.String("lang", "en", "language for citizen-facing text")
	full := fs.Bool("full", false, "show the whole condition tree, not just the top level")
	if err := fs.Parse(args); err != nil {
		return err
	}
	dir := contentDir(fs, "content/jurisdictions/IN-TN")

	if *profilePath == "" || *approval == "" {
		return fmt.Errorf("--profile and --approval are both required")
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
	if _, ok := l.Graph.Approvals[*approval]; !ok {
		return fmt.Errorf("%q is not an approval in this jurisdiction; try one of:\n  %s",
			*approval, strings.Join(l.Graph.ApprovalIDs(), "\n  "))
	}

	raw, err := os.ReadFile(*profilePath)
	if err != nil {
		return err
	}
	p, err := eval.ParseProfileJSON(l.Prog, raw)
	if err != nil {
		return err
	}

	d := eval.New(l.Prog).Evaluate(
		eval.Input{Profile: p, AsOf: anchor, Language: *lang, Layers: l.Layers},
		eval.Options{Trace: true},
	)

	st, _ := d.StatusOf(*approval)
	a := l.Graph.Approvals[*approval]
	line := strings.Repeat("─", 78)

	fmt.Println(line)
	fmt.Printf("%s\n%s\n", a.Title.Get(*lang), *approval)
	fmt.Println(line)

	// The headline answer, in plain language.
	switch st.Required {
	case value.True:
		fmt.Println("\nYOU NEED THIS APPROVAL.")
	case value.False:
		if st.Exempted {
			fmt.Println("\nYOU DO NOT NEED THIS APPROVAL — you are exempt.")
		} else {
			fmt.Println("\nYou do not need this approval.")
		}
	default:
		fmt.Println("\nWE CANNOT TELL YET whether you need this approval.")
	}

	if st.ByDependency != "" {
		fmt.Printf("\nIt is required because %s depends on it.\n", st.ByDependency)
	}
	for _, ap := range d.Approvals {
		if ap.ID == *approval && ap.Reason != "" {
			fmt.Printf("\nReason given to you:\n  %s\n", wrap(ap.Reason, 74, "  "))
		}
	}
	fmt.Printf("\nStatutory basis: %s\n", a.LegalBasis.Cite())

	// Which rules acted, at what authority, and which lost.
	fmt.Printf("\n%s\nHOW THIS WAS DECIDED\n%s\n", line, line)
	if len(st.Contributions) == 0 {
		fmt.Println("\nNo rule in this ruleset acts on this approval.")
	}
	for _, c := range st.Contributions {
		marker := "  "
		switch {
		case c.RuleID == st.DecidedBy:
			marker = "→ "
		case c.Overridden:
			marker = "× "
		}
		fmt.Printf("\n%s%s\n", marker, c.RuleID)
		fmt.Printf("    effect      %s\n", c.Effect)
		fmt.Printf("    precedence  %d\n", c.Precedence)
		fmt.Printf("    fired       %s\n", c.Fired)
		if c.Overridden {
			fmt.Printf("    outranked by a rule at precedence %d\n", st.Precedence)
		}
		if r, ok := l.Graph.Rules[c.RuleID]; ok {
			fmt.Printf("    basis       %s\n", r.LegalBasis.Cite())
			fmt.Printf("    condition   %s\n", wrapExpr(r.When, 60, "                "))
		}
	}

	if len(st.UnresolvedVars) > 0 {
		fmt.Printf("\n%s\nWHAT WOULD SETTLE IT\n%s\n\n", line, line)
		for _, v := range st.UnresolvedVars {
			if decl, ok := l.Graph.Vars[v]; ok {
				fmt.Printf("  • %s\n", decl.Question.Get(*lang))
				if h := decl.Hint.Get(*lang); h != "" {
					fmt.Printf("    %s\n", h)
				}
			} else {
				fmt.Printf("  • %s\n", v)
			}
		}
	}

	// The derivation trace: every sub-expression and what it evaluated to.
	fmt.Printf("\n%s\nDERIVATION TRACE\n%s\n", line, line)
	for _, t := range d.Trace {
		relevant := false
		for _, c := range st.Contributions {
			if c.RuleID == t.RuleID {
				relevant = true
			}
		}
		if !relevant {
			continue
		}
		fmt.Printf("\n%s → %s\n", t.RuleID, t.Fired)
		if !t.InForce {
			fmt.Println("    not in force on the anchor date")
			continue
		}
		depth := 1
		if *full {
			depth = 99
		}
		printTrace(t.Detail, "    ", depth)
	}
	fmt.Println()
	return nil
}

// printTrace renders the condition tree. The top level is enough for most
// questions ("which clause failed?"); --full shows every step.
func printTrace(nodes []eval.TraceNode, indent string, depth int) {
	if depth <= 0 {
		return
	}
	for _, n := range nodes {
		fmt.Printf("%s%s\n", indent, n.Expr)
		detail := n.Result
		if n.Detail != "" {
			detail += " — " + n.Detail
		}
		fmt.Printf("%s  = %s\n", indent, detail)
		printTrace(n.Children, indent+"    ", depth-1)
	}
}

// wrapExpr renders a rule condition as source and re-flows it to a width.
func wrapExpr(e srl.Expr, width int, indent string) string {
	if e == nil {
		return "(none)"
	}
	return wrap(srl.FormatExpr(e), width, indent)
}
