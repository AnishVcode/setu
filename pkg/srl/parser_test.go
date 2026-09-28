package srl

import (
	"strings"
	"testing"
)

const sample = `
package IN.TN

meta {
  title "Tamil Nadu — smoke test content"
}

enum PollutionCategory {
  RED    { title en "Red" }
  ORANGE { title en "Orange" }
  GREEN  { title en "Green" }
}

profile {
  var workers: Quantity<Count> {
    question en "How many workers will you employ, including contract workers?"
    hint en "Count everyone present on the largest shift."
    unit workers
  }
  var uses_power: Bool {
    question en "Will you use power-driven machinery?"
  }
  var water_draw: Quantity<VolumeFlow> {
    question en "How much water will the unit draw each day?"
    unit KLD
  }
  var investment: Money {
    question en "What is the total investment in plant and machinery?"
  }
  var category: Enum<PollutionCategory> {
    question en "Which pollution category applies?"
  }
  var premise: Geo {
    question en "Where is the premise?"
  }
}

department TN.DISH {
  name en "Directorate of Industrial Safety and Health"
  jurisdiction IN.TN
  adapter_tier api
  owner "Joint Director (Licensing)"
  review_status unverified
}

document D.SITE_PLAN {
  title en "Site and machinery layout plan"
  issuer "Applicant"
  verification officer
  attributes [ "built_up_area_m2", "machinery_layout" ]
}

fee F.FACT.LICENCE {
  title en "Factory licence fee"
  head "0230-01-800-AB"
  department TN.DISH
  amount = slab profile.workers {
    <= 20 workers  -> INR 1000
    <= 50 workers  -> INR 2500
    else           -> INR 5000
  }
  rounding nearest INR 1
  legal_basis { act "Factories Act, 1948" section "6" }
}

obligation O.FACT.ANNUAL_RETURN {
  title en "Annual return in Form 22"
  cadence 1 year
  due month 1 day 31
  department TN.DISH
  consequence en "Failure to file attracts penalty under Section 92."
  legal_basis { act "Factories Act, 1948" section "88" }
}

approval TN.FACT.LICENCE {
  title en "Factory licence"
  category "licence"
  department TN.DISH
  jurisdiction IN.TN
  sla 30 days
  validity 1 year
  renewal { notice 60 days cadence 1 year }
  deemed_approval permitted section "6(3)"
  documents [ D.SITE_PLAN ]
  fees [ F.FACT.LICENCE ]
  obligations [ O.FACT.ANNUAL_RETURN ]
  appeal { authority en "Chief Inspector of Factories" within 30 days }
  legal_basis { act "Factories Act, 1948" section "6" dated 1948-09-23 }
  effective from 1948-04-01
  owner "Joint Director (Licensing)"
  review_status unverified
}

incentive I.TN.CAPITAL_SUBSIDY {
  title en "Capital subsidy"
  scheme "TN MSME Policy"
  department TN.DISH
  value = min(0.25 * profile.investment, INR 30 lakh)
  window from 2021-01-01 until 2026-03-31
}

rule R.FACT.LICENCE.TRIGGER {
  title en "Factory licence threshold"
  kind trigger
  when profile.uses_power == true and profile.workers >= 10 workers
       or profile.workers >= 20 workers
  then {
    require approval TN.FACT.LICENCE
  }
  explain en "You need a factory licence because you will employ {profile.workers} with power-driven machinery."
  legal_basis { act "Factories Act, 1948" section "2(m)" }
  test "ten workers with power" {
    given { profile.workers = 10 workers, profile.uses_power = true }
    expect fires
  }
  test "nine workers without power" {
    given { profile.workers = 9 workers, profile.uses_power = false }
    expect not fires
  }
}

rule R.FACT.LICENCE.EXEMPT_SEASONAL {
  kind exemption
  overrides [ R.FACT.LICENCE.TRIGGER ]
  when profile.category == PollutionCategory.GREEN and profile.water_draw < 10 KLD
  then {
    exempt approval TN.FACT.LICENCE reason en "Small green-category units are exempt."
  }
  legal_basis { act "Factories Act, 1948" section "85" }
  test "green and low water" {
    given { profile.category = PollutionCategory.GREEN, profile.water_draw = 5 KLD }
    expect fires
  }
  test "red category" {
    given { profile.category = PollutionCategory.RED, profile.water_draw = 5 KLD }
    expect not fires
  }
}

rule R.FIRE.NOC.GEO {
  kind trigger
  when within(profile.premise, "coastal_regulation_zone")
       or distance_to(profile.premise, "coastline") < 500 m
  then { require approval TN.FACT.LICENCE }
  explain en "Coastal siting."
  legal_basis { act "CRZ Notification, 2019" section "3" }
  test "inside crz" { given { } expect unknown }
  test "no premise" { given { } expect unknown }
}
`

func TestParseSample(t *testing.T) {
	f, diags := Parse("sample.srl", sample)
	for _, d := range diags {
		if d.Severity == SevError {
			t.Errorf("unexpected diagnostic: %s", d)
		}
	}
	if f.Package != "IN.TN" {
		t.Fatalf("package = %q, want IN.TN", f.Package)
	}
	if got := len(f.Enums); got != 1 {
		t.Errorf("enums = %d, want 1", got)
	}
	if f.Profile == nil || len(f.Profile.Vars) != 6 {
		t.Fatalf("profile vars = %v, want 6", f.Profile)
	}
	if got := len(f.Approvals); got != 1 {
		t.Fatalf("approvals = %d, want 1", got)
	}
	a := f.Approvals[0]
	if a.SLA.Days != 30 {
		t.Errorf("sla = %v, want 30 days", a.SLA)
	}
	if !a.Deemed.Permitted || a.Deemed.Section != "6(3)" {
		t.Errorf("deemed = %+v", a.Deemed)
	}
	if a.Appeal == nil || a.Appeal.Within.Days != 30 {
		t.Errorf("appeal = %+v", a.Appeal)
	}
	if got := len(f.Rules); got != 3 {
		t.Fatalf("rules = %d, want 3", got)
	}
	r := f.Rules[0]
	if len(r.Tests) != 2 {
		t.Errorf("tests = %d, want 2", len(r.Tests))
	}
	if len(r.Then) != 1 || r.Then[0].Kind != EffectRequireApproval {
		t.Errorf("effects = %+v", r.Then)
	}
	// An exemption must outrank the rule it displaces by default.
	ex := f.Rules[1]
	if ex.Precedence <= r.Precedence {
		t.Errorf("exemption precedence %d should exceed trigger precedence %d",
			ex.Precedence, r.Precedence)
	}
	if len(f.Fees) != 1 || f.Fees[0].Amount == nil {
		t.Fatalf("fee not parsed")
	}
	if _, ok := f.Fees[0].Amount.(*SlabExpr); !ok {
		t.Errorf("fee amount is %T, want *SlabExpr", f.Fees[0].Amount)
	}
}

func TestParseErrorsAreReported(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"chained comparison", `package P
rule R { when 1 < 2 < 3 then { flag "x" } }`, "cannot be chained"},
		{"slab without else", `package P
fee F { amount = slab profile.x { <= 10 -> INR 5 } }`, "no `else` arm"},
		{"unknown function", `package P
rule R { when frobnicate(1) then { flag "x" } }`, "unknown function"},
		{"unknown unit", `package P
profile { var x: Money { unit furlong } }`, "unknown unit"},
		{"test without expectation", `package P
rule R { when true then { flag "x" } test "t" { given { } } }`, "states no expectation"},
		{"two when clauses", `package P
rule R { when true when false then { flag "x" } }`, "more than one `when`"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, diags := Parse("t.srl", tc.src)
			var all []string
			for _, d := range diags {
				all = append(all, d.Msg)
			}
			joined := strings.Join(all, " | ")
			if !strings.Contains(joined, tc.want) {
				t.Errorf("diagnostics %q do not mention %q", joined, tc.want)
			}
		})
	}
}

func TestLexerDates(t *testing.T) {
	toks := NewLexer("t", `2019-06-12 2019 - 06 x2019-06-12`).Tokens()
	if toks[0].Kind != DATE || toks[0].Text != "2019-06-12" {
		t.Errorf("first token = %v, want DATE", toks[0])
	}
	if toks[1].Kind != NUMBER || toks[2].Kind != MINUS {
		t.Errorf("subtraction misparsed: %v %v", toks[1], toks[2])
	}
}
