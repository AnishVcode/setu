package testkit

import (
	"strings"
	"testing"

	"github.com/setu-platform/setu/pkg/compile"
	"github.com/setu-platform/setu/pkg/kg"
	"github.com/setu-platform/setu/pkg/sema"
	"github.com/setu-platform/setu/pkg/srl"
	"github.com/setu-platform/setu/pkg/value"
)

const roundTripSrc = `
package IN.TEST

enum Cat { A B C }

profile {
  var workers: Quantity<Count>      { question en "Workers?" unit workers }
  var powered: Bool                 { question en "Power?" }
  var cat: Enum<Cat>                { question en "Category?" }
  var investment: Money             { question en "Investment?" }
  var water: Quantity<VolumeFlow>   { question en "Water?" unit KLD }
  var start: Date                   { question en "Start date?" }
  var site: Geo                     { question en "Where?" }
  var note: Text                    { question en "Anything else?" }
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
  when profile.workers >= 10 workers and profile.powered == true
       and profile.cat == Cat.A and profile.investment > INR 1 lakh
       and profile.water > 5 KLD and profile.start > 2020-01-01
       and profile.note == "x" and within(profile.site, "nowhere")
  then { require approval A.X }
  explain en "Everything at once."
  legal_basis { act "Test Act" section "2" }
  test "p" { given { profile.workers = 10 workers } expect unknown }
  test "n" { given { profile.workers = 1 workers } expect not fires }
}
`

// TestRenderedProfileRoundTrips is the guarantee behind every counterexample
// the property suite and the analyser print: an author must be able to paste
// one into a rule file unedited and have it parse. A renderer that emitted
// "yes" for a boolean, or a bare fraction for a number, would produce advice
// that does not compile.
func TestRenderedProfileRoundTrips(t *testing.T) {
	g, diags := kg.LoadString("rt.srl", roundTripSrc)
	assertNoErrors(t, diags)
	assertNoErrors(t, sema.Check(g))
	prog, diags := compile.Compile(g)
	assertNoErrors(t, diags)

	gen := newGenerator(prog, 99)
	for i := 0; i < 200; i++ {
		p := gen.profile(0.2)
		block := RenderProfile(prog, p)

		// Wrap the rendered block in a rule so the parser sees it in context.
		src := roundTripSrc + `
rule R.ROUNDTRIP {
  kind trigger
  when true
  then { require approval A.X }
  explain en "Round trip."
  legal_basis { act "Test Act" section "3" }
  test "generated" {
    ` + block + `
    expect fires
  }
  test "n" { given { } expect not fires }
}
`
		g2, diags2 := kg.LoadString("rt2.srl", src)
		for _, d := range diags2 {
			if d.Severity == srl.SevError {
				t.Fatalf("rendered profile does not parse: %s\n\n%s", d, block)
			}
		}
		for _, d := range sema.Check(g2) {
			if d.Severity == srl.SevError {
				t.Fatalf("rendered profile does not type-check: %s\n\n%s", d, block)
			}
		}

		// And the values must survive the round trip unchanged.
		rt := g2.Rules["R.ROUNDTRIP"]
		if len(rt.Tests) == 0 {
			t.Fatalf("the round-trip test case was not parsed")
		}
		restored, err := profileFromGiven(prog, rt.Tests[0].Given)
		if err != nil {
			t.Fatalf("cannot rebuild the profile: %v\n\n%s", err, block)
		}
		for _, name := range p.Names() {
			orig, got := p.Get(name), restored.Get(name)
			if orig.IsUnknown() {
				continue
			}
			if eq := orig.Equal(got); eq != value.True {
				t.Fatalf("%s changed across the round trip: %s became %s\n\n%s",
					name, orig, got, block)
			}
		}
	}
}

func assertNoErrors(t *testing.T, diags []srl.Diagnostic) {
	t.Helper()
	var msgs []string
	for _, d := range diags {
		if d.Severity == srl.SevError {
			msgs = append(msgs, d.String())
		}
	}
	if len(msgs) > 0 {
		t.Fatalf("unexpected errors:\n%s", strings.Join(msgs, "\n"))
	}
}
