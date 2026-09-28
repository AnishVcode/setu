package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/setu-platform/setu/pkg/compile"
	"github.com/setu-platform/setu/pkg/kg"
	"github.com/setu-platform/setu/pkg/sema"
	"github.com/setu-platform/setu/pkg/srl"
	"github.com/setu-platform/setu/pkg/value"
)

// loaded is a fully processed jurisdiction: parsed, checked, linted, compiled.
type loaded struct {
	Graph  *kg.Graph
	Prog   *compile.Program
	Layers *value.Layers
	Diags  []srl.Diagnostic
	Dir    string
}

// Errors reports whether any diagnostic blocks use of the ruleset.
func (l *loaded) Errors() int {
	n := 0
	for _, d := range l.Diags {
		if d.Severity == srl.SevError {
			n++
		}
	}
	return n
}

// Warnings counts non-blocking findings.
func (l *loaded) Warnings() int {
	n := 0
	for _, d := range l.Diags {
		if d.Severity == srl.SevWarning {
			n++
		}
	}
	return n
}

// loadOptions controls how strict the load is.
type loadOptions struct {
	Lint      bool
	LintCfg   sema.LintConfig
	Languages []string
}

// load runs the whole front end over a content directory.
//
// It never stops at the first failing phase when the failure is recoverable:
// an author fixing content wants every diagnostic in one pass. It does stop
// before compiling if name resolution failed, because compiling an unresolved
// graph would produce cascading nonsense.
func load(dir string, opt loadOptions) (*loaded, error) {
	if _, err := os.Stat(dir); err != nil {
		return nil, fmt.Errorf("content directory %s: %w", dir, err)
	}
	out := &loaded{Dir: dir}

	g, diags := kg.LoadDir(dir)
	out.Diags = append(out.Diags, diags...)
	if g == nil {
		return out, fmt.Errorf("no content could be loaded from %s", dir)
	}
	out.Graph = g

	out.Diags = append(out.Diags, sema.Check(g)...)

	if opt.Lint {
		cfg := opt.LintCfg
		if len(cfg.Languages) == 0 {
			cfg.Languages = []string{"en"}
		}
		out.Diags = append(out.Diags, sema.Lint(g, cfg)...)
	}

	layers, err := kg.LoadLayers(dir)
	if err != nil {
		out.Diags = append(out.Diags, srl.Diagnostic{
			Severity: srl.SevError, Code: "KG0020",
			Pos: srl.Pos{File: dir}, Msg: err.Error(),
		})
	}
	out.Layers = layers

	// Compilation is attempted even with errors present, because a cycle is
	// often the thing the author most needs to see.
	prog, cdiags := compile.Compile(g)
	out.Diags = append(out.Diags, cdiags...)
	out.Prog = prog

	return out, nil
}

// printDiagnostics writes findings in severity order with a summary line.
func printDiagnostics(l *loaded, showInfo bool) {
	bySev := map[srl.Severity][]srl.Diagnostic{}
	for _, d := range l.Diags {
		bySev[d.Severity] = append(bySev[d.Severity], d)
	}
	for _, sev := range []srl.Severity{srl.SevError, srl.SevWarning, srl.SevInfo} {
		if sev == srl.SevInfo && !showInfo {
			continue
		}
		for _, d := range bySev[sev] {
			fmt.Println(d)
		}
	}
	var parts []string
	if n := len(bySev[srl.SevError]); n > 0 {
		parts = append(parts, fmt.Sprintf("%d error(s)", n))
	}
	if n := len(bySev[srl.SevWarning]); n > 0 {
		parts = append(parts, fmt.Sprintf("%d warning(s)", n))
	}
	if n := len(bySev[srl.SevInfo]); n > 0 {
		parts = append(parts, fmt.Sprintf("%d note(s)", n))
	}
	if len(parts) == 0 {
		parts = append(parts, "no findings")
	}
	fmt.Println()
	fmt.Println(strings.Join(parts, ", "))
}

// today parses an --as-of flag, defaulting to a caller-supplied date.
//
// There is no default of "now" anywhere in the toolchain. Every command that
// depends on a date takes one explicitly, so that a run is reproducible and a
// CI failure can be replayed exactly (P1, P6).
func parseAsOf(s string) (value.Date, error) {
	if s == "" {
		return value.Date{}, fmt.Errorf(
			"--as-of is required: every evaluation is anchored to an explicit date " +
				"so that it can be reproduced later")
	}
	return value.ParseDate(s)
}

// parseDateFlag reads a required date-valued flag.
func parseDateFlag(name, s string) (value.Date, error) {
	if s == "" {
		return value.Date{}, fmt.Errorf("%s is required (YYYY-MM-DD)", name)
	}
	return value.ParseDate(s)
}
