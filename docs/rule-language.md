# The SETU Rule Language (SRL)

**Status:** implemented. This document specifies the language as built; the
parser, type checker and evaluator in `pkg/srl`, `pkg/sema` and `pkg/eval` are
the normative implementation and the test suites are the conformance suite.

---

## 1. Why a language at all

Legal requirements are computed only by a deterministic engine over versioned
data (principle P1). That data has to be written by someone with legal training
and no programming background, read by a departmental reviewer signing it off,
and defended in a tribunal years later. Three consequences follow, and they are
what shaped every decision below:

- **Rules are data, not code.** There is no escape hatch to a general-purpose
  language. The set of functions is closed (§7), so a rule cannot make a network
  call, read a clock, or do anything whose result is not a pure function of the
  applicant's profile and the pinned ruleset.
- **The source is the specification.** A rule states its statute, its section,
  its owner, its review date and its test cases in the same block as its logic.
  Metadata that lives somewhere else drifts.
- **The type system is a safety device.** Comparing rupees to kilolitres, or
  tonnes-per-day to tonnes-per-annum, produces a confident, wrong, legally
  consequential answer. Every such comparison is a compile error (§5).

---

## 2. Lexical structure

```
comment     := "//" … end-of-line | "/*" … "*/"
identifier  := letter (letter | digit | "_")*
number      := digit (digit | "_")* ["." digit+]
date        := 4digit "-" 2digit "-" 2digit          // 2019-06-12
string      := '"' character* '"'                     // \n \t \" \\ \{ \}
```

`2019-06-12` lexes as a single date token. `2019 - 06` is subtraction; the
lexer distinguishes them by requiring the exact `YYYY-MM-DD` shape with no
trailing digit.

**Reserved words** — these may not be used as identifiers:

```
and  or  not  in  true  false  if  else  unknown
```

`then` is deliberately *not* reserved. It separates the branches of an `if`
expression and also names a rule's effect block; reserving it would stop
`then { … }` from being recognised as a field.

---

## 3. Grammar

Written in EBNF. `{ x }` is zero or more, `[ x ]` is optional.

```ebnf
file          := "package" qualified declaration*

declaration   := meta | enum | profile | department | document
               | approval | fee | obligation | incentive | rule

qualified     := identifier { "." identifier }

meta          := "meta" "{" { identifier string } "}"

enum          := "enum" identifier "{" { enum_member } "}"
enum_member   := identifier [ "{" "title" lang string "}" ]

profile       := "profile" "{" { var_decl } "}"
var_decl      := "var" identifier ":" type [ "{" { var_field } "}" ]
var_field     := "question"  lang string
               | "hint"      lang string
               | "unit"      identifier
               | "example"   string
               | "sensitive" ("true" | "false")

type          := "Bool" | "Text" | "Date" | "Period" | "Geo" | "Money" | "Number"
               | "Quantity" "<" dimension ">"
               | "Enum" "<" identifier ">"
               | "Set" "<" type ">"

department    := "department" qualified "{" { dept_field | gov_field } "}"
dept_field    := "name" lang string
               | "jurisdiction" qualified
               | "adapter_tier" ("api" | "file" | "rpa" | "assisted_manual")
               | "contact_url" string

document      := "document" qualified "{" { doc_field | gov_field } "}"
doc_field     := "title" lang string
               | "issuer" string
               | "verification" ("api" [string] | "digilocker" | "officer" | "self_declared")
               | "max_age" period
               | "credential" string
               | "attributes" "[" { string [","] } "]"

approval      := "approval" qualified "{" { appr_field | gov_field | validity } "}"
appr_field    := "title" lang string
               | "summary" lang string
               | "category" string
               | "department" qualified
               | "jurisdiction" qualified
               | "sla" period
               | "validity" period
               | "renewal" "{" [ "notice" period ] [ "cadence" period ] "}"
               | "deemed_approval" ( "permitted" [ "section" string ] | "prohibited" )
               | "documents"   id_list
               | "fees"        id_list
               | "obligations" id_list
               | "depends_on"  ( id_list | dep_block )
               | "appeal" "{" [ "authority" lang string ] [ "within" period ] "}"
               | legal_basis

dep_block     := "{" { qualified [ "{" "reason" lang string "}" ] } "}"

fee           := "fee" qualified "{" { fee_field | gov_field | validity } "}"
fee_field     := "title" lang string
               | "head" string                       // treasury accounting head
               | "department" qualified
               | "amount" "=" expr
               | "rounding" ( "none" | ("nearest"|"up"|"down") money_literal )
               | legal_basis

obligation    := "obligation" qualified "{" { obl_field | gov_field | validity } "}"
obl_field     := "title" lang string
               | "cadence" period
               | "due" "month" number "day" number
               | "due_within" period
               | "evidence" id_list
               | "consequence" lang string
               | "department" qualified
               | legal_basis

incentive     := "incentive" qualified "{" { inc_field | gov_field } "}"
inc_field     := "title" lang string
               | "scheme" string
               | "department" qualified
               | "value" "=" expr
               | "window" "from" date [ "until" date ]
               | "evidence" id_list
               | "apply_url" string
               | legal_basis

rule          := "rule" qualified "{" { rule_field | gov_field | validity } "}"
rule_field    := "title" lang string
               | "kind" rule_kind
               | "precedence" number
               | "overrides" id_list
               | "tags" "[" { string [","] } "]"
               | "when" expr
               | "then" "{" { effect } "}"
               | "explain" lang string
               | test
               | legal_basis

rule_kind     := "trigger" | "exemption" | "document" | "fee"
               | "obligation" | "incentive" | "dependency"

effect        := "require" "approval" qualified [ reason ]
               | "exempt"  "approval" qualified [ reason ]
               | "require" "document" qualified "for" qualified [ reason ]
               | "levy"    "fee" qualified "on" qualified [ reason ]
               | "waive"   "fee" qualified "on" qualified [ reason ]
               | "create"  "obligation" qualified "from" qualified [ reason ]
               | "offer"   "incentive" qualified [ reason ]
               | "depend"  qualified "on" qualified [ reason ]
               | "flag"    string
reason        := "reason" lang string

test          := "test" string "{" [ "given" "{" { assignment } "}" ]
                                   "expect" ( "fires" | "not" "fires" | "unknown" ) "}"
assignment    := "profile" "." identifier "=" expr

legal_basis   := "legal_basis" "{" { lb_field } "}"
lb_field      := "act" string | "section" string | "rule" string
               | "notification" string | "dated" date | "url" string

gov_field     := "owner" string
               | "review_status" ("unverified"|"in_review"|"verified"|"deprecated")
               | "last_verified" date
               | "review_cadence" period

validity      := "effective" "from" date [ "until" date ]

period        := { number ("day"|"days"|"week"|"weeks"|"month"|"months"|"year"|"years") }
id_list       := "[" { qualified [","] } "]"
lang          := identifier                            // BCP 47 tag, e.g. en, ta
```

### Expressions

Loosest binding first:

```ebnf
expr        := or
or          := and { "or" and }
and         := not { "and" not }
not         := "not" not | comparison
comparison  := additive [ ("=="|"!="|"<"|"<="|">"|">=") additive | "in" additive ]
additive    := multiplicative { ("+"|"-") multiplicative }
multiplicative := unary { ("*"|"/") unary }
unary       := "-" unary | primary
primary     := number | quantity | money | period | string | date
             | "true" | "false" | "unknown"
             | "[" { expr [","] } "]"                  // set literal
             | "profile" "." identifier                // profile variable
             | qualified                               // declared object or enum member
             | identifier "(" { expr [","] } ")"        // builtin call
             | "if" expr "then" expr "else" expr
             | slab
             | "(" expr ")"

slab        := "slab" additive "{" { slab_arm } "else" "->" additive "}"
slab_arm    := ("<="|"<"|">="|">"|"==") additive "->" additive

quantity    := number identifier                        // 60 KLD, 25 workers, 500 m
money       := "INR" number [ "thousand" | "lakh" | "crore" ]
```

**Comparison is non-associative.** `a < b < c` is rejected with a message telling
the author to write two comparisons joined by `and`. In a legal condition it is
always a mistake, never a chain.

**A slab must have an `else` arm.** A fee schedule with a gap is undefined
outside its bounds, and the compiler refuses it rather than letting an applicant
discover the gap.

---

## 4. Profile variables

A profile variable is written `profile.name`, always with the prefix. Declared
objects are written by their dotted identifier. There is no context in which the
two could be confused, and a legal reader never has to work out which is which.

A variable's `question` is not documentation. When a rule cannot be decided
because the variable is unanswered, the engine returns that exact wording as an
unresolved determination (§6). It is a citizen-facing legal artefact and the
linter requires it in every supported language.

A variable no rule, fee or incentive reads is a **lint error** (`LINT0070`):
collecting an answer for no purpose is a data-minimisation defect, not an
oversight.

---

## 5. Types, dimensions and units

| Type | Written | Notes |
|---|---|---|
| Boolean | `Bool` | |
| Number | `Number` | dimensionless |
| Amount | `Money` | always rupees; `INR 4 crore` |
| Quantity | `Quantity<Dim>` | dimensioned, see below |
| Text | `Text` | |
| Date | `Date` | civil date, no time, no zone |
| Period | `Period` | calendar duration; never converts to a number |
| Enum | `Enum<Name>` | closed vocabulary |
| Set | `Set<T>` | |
| Location | `Geo` | resolved against versioned boundary layers |

**Dimensions:** `Scalar, Money, Count, Length, Area, Volume, VolumeFlow, Power,
Mass, MassFlowDay, MassFlowYear, Energy, Ratio, Nominal, Temperature`.

**Rules of the type system:**

- Ordering and equality require the same dimension. `profile.water_draw > 25` is
  an error; `profile.water_draw > 25 KLD` is correct. The error message names
  the unit to add.
- `MassFlowDay` and `MassFlowYear` are separate dimensions and **do not
  convert**. A threshold stated per annum is encoded per annum. There is no
  ratio between them that is right in every month.
- Nominal scales — `dB`, `pH` — convert only to themselves.
- Multiplication requires at least one dimensionless operand. Compound
  dimensions are outside the language: no statutory threshold in scope needs
  one, and a dimension algebra is not auditable by a legal reader.
- Division by a dimensionless number keeps the dimension; division by the same
  dimension yields a plain number. Anything else is an error.
- `Period` never becomes a number. "Valid for 1 year" and "valid for 365 days"
  differ across a leap year.

**Arithmetic is exact.** Every number is a `math/big.Rat`. No evaluation in the
engine touches a float: a fee computed as a third of a slab must produce the
same paise on every machine, forever, and be reproducible in a tribunal. Unit
conversion factors are exact rationals for the same reason — `0.3048` is
`3048/10000`, not the nearest double.

---

## 6. Three-valued semantics

Every condition evaluates to **true**, **false** or **unknown**. Unknown is what
an unanswered profile variable produces, and it propagates by Kleene's rules:

| | | `and` | `or` |
|---|---|---|---|
| unknown | true | unknown | **true** |
| unknown | false | **false** | unknown |
| unknown | unknown | unknown | unknown |

`not unknown` is `unknown`.

This is the mechanism behind "the engine must not guess". A conventional engine
coerces unknown to false and quietly omits an approval the applicant legally
needs. SRL propagates it to the determination as an **unresolved determination**
carrying the question to ask.

### Relevance

An unknown result reports *which* unanswered variables were responsible, and
only those. In `A and B` where `A` is definitely false, the unanswered variables
in `B` are irrelevant — the answer is false either way — and the applicant is
not asked about them. The evaluator computes this operand by operand, so the
question list is minimal rather than merely correct.

### `defined()` is the exception

`defined(profile.x)` reports whether an answer exists and never returns unknown.
It is therefore **not monotone in information**: answering a question changes it
from false to true. The property suite detects any rule using it and reports
that the monotonicity property was not checked, naming the rules, rather than
silently weakening the guarantee.

---

## 7. Builtins

The set is closed. Every one is a pure function of its arguments and the pinned
boundary layers.

| Function | Signature | Notes |
|---|---|---|
| `min(x, …)` | numeric, same dimension → same | |
| `max(x, …)` | numeric, same dimension → same | |
| `abs(x)` | numeric → same | |
| `round(x)`, `floor(x)`, `ceil(x)` | numeric → same | half away from zero |
| `defined(x)` | any → `Bool` | never unknown; see §6 |
| `point(lat, lon)` | `Number, Number` → `Geo` | |
| `within(g, layer)` | `Geo, Text` → `Bool` | unknown when the layer is absent |
| `distance_to(g, layer)` | `Geo, Text` → `Quantity<Length>` | rounded to whole metres |
| `zone_of(g, layer)` | `Geo, Text` → `Text` | names the matching feature |
| `starts_with(a, b)`, `contains_text(a, b)` | `Text, Text` → `Bool` | |
| `required(A)` | approval → `Bool` | reads another approval's settled outcome |
| `exempted(A)` | approval → `Bool` | |
| `years_between(a, b)`, `days_between(a, b)` | `Date, Date` → `Number` | |

An absent boundary layer makes `within` return **unknown**, not false. Not
having the map is not evidence of being outside the zone.

---

## 8. Effects and precedence

A rule's `then` block produces effects. Only `require approval` and
`exempt approval` change whether an approval is required; the rest attach
documents, fees, obligations, incentives, dependency edges and risk flags.

**Resolution.** For each approval:

1. Take the rules that fired **true**. Let `p*` be their highest precedence.
2. At `p*`: exemptions only → not required. Requirements only → required.
   Both → **conflict** (below).
3. A rule that is still **unknown** can overturn this only if its precedence is
   at least `p*` *and* it pulls the other way. If any such rule exists, the
   approval is **undetermined**, and the questions that would settle it are
   reported. This is the difference between "you need a trade licence" and "you
   need a trade licence unless you are in an industrial estate, and you have not
   told us".
4. If no rule fired definitely, pending *exemptions* alone change nothing —
   with nothing requiring it, the approval is not required either way.

Exemptions default to precedence 200, triggers to 100, so an exemption outranks
what it displaces without the author having to think about it. Declaring
`overrides [R.X]` with a precedence that does not exceed `R.X` is a lint error:
the exemption would never take effect.

**Conflicts are never resolved silently.** Two provisions of equal authority
saying opposite things about one applicant produce an explicit `Conflict` on the
determination, the approval is marked undetermined, and the case routes to a
human. Guessing either way would be a legal error (P12).

---

## 9. Dependencies

`depends_on` means *cannot be granted before*. It is a legal ordering
constraint, and it is **conjunctive**: two prerequisites mean both.

**A required approval's prerequisites are themselves required.** There is no way
to obtain a fire NOC that depends on a building plan approval without obtaining
the building plan approval. The propagation carries the lowest authority in the
system, so an explicit statutory exemption always outranks it.

Where an approval waits on *whichever of several authorities* has jurisdiction,
declare the edge conditionally from a rule:

```srl
rule R.DEP.FIRE_ON_CMDA {
  kind dependency
  when within(profile.premise, "chennai_metropolitan_area")
  then { depend TN.FIRE.NOC on TN.PLANNING.BUILDING_CMDA
           reason en "The fire service assesses the approved building plan." }
  …
}
```

A conditional edge exists only when its rule fires. Declaring both authorities
as static prerequisites would require both.

**A rule that feeds a dependency edge into an approval must not also read that
approval's outcome.** The compiler rejects it as a cycle: the graph would have
no fixed point and the answer would depend on evaluation order.

---

## 10. Effective dating and versioning

Every rule, approval, fee and obligation carries `effective from D [until D]`,
a half-open interval. Half-open is deliberate: a rule superseded on 1 April and
its successor commencing on 1 April must not both apply that day.

Evaluation takes an explicit **as-of date**. Nothing in the toolchain reads the
clock — every command that depends on a date requires one on the command line —
so a determination made today can be reproduced byte for byte in ten years.

A **published ruleset version** embeds every source file and the boundary
layers, hashes them, and is signed. A case pins the version at submission and is
evaluated against it for its whole life. Versions are immutable: republishing an
existing version fails rather than overwriting.

---

## 11. Determinism

The same profile, the same ruleset version and the same as-of date always
produce byte-identical output, including the content hash. This is asserted by
`TestDeterminism` and by the `determinism` property over generated profiles.

It is achieved by construction, not by care:

- exact rational arithmetic, never floating point;
- no clock, no randomness, no network in evaluation;
- every traversal goes through a sorted accessor, never a bare `map` range;
- every output collection is sorted before it is emitted;
- ties in precedence, in critical-path selection and in rule ordering break on
  identifier.

---

## 12. What the toolchain guarantees, and what it does not

| Check | Guarantee |
|---|---|
| Type and dimension errors | **Decided.** A dimension mismatch cannot compile. |
| Dependency cycles | **Decided.** Exact graph traversal; publication is blocked. |
| Orphaned approvals | **Decided.** An approval nothing can require is an error. |
| Missing test coverage | **Decided.** Every rule needs a positive and a negative case. |
| Unreachable rules | **Proven** for contradictory bounds on one variable. Otherwise a witness search, reported as "no satisfying profile found in N attempts" — never as "unreachable". |
| Contradictory rules | **Proven** when a witness profile is found, and the witness is printed as a pasteable `given` block. A miss is not proof of absence, and the report says so. |

The distinction is deliberate. A checker that overstated its certainty would be
worse than none: an author would delete a rule the analysis merely failed to
understand.

---

## 13. Worked example

```srl
rule R.FACTORY.LICENCE.THRESHOLD {
  title en "When premises become a factory"
  kind trigger

  // Section 2(m): ten or more workers with power, twenty or more without.
  when (profile.uses_power == true and profile.workers_total >= 10 workers)
       or profile.workers_total >= 20 workers

  then {
    require approval TN.DISH.FACTORY_LICENCE
    require approval TN.DISH.FACTORY_PLAN
  }

  explain en "With {profile.workers_total} on the largest shift and power-driven
              machinery, the premises are a factory under Section 2(m) of the
              Factories Act, 1948, and need a licence."

  legal_basis {
    act "Factories Act, 1948"
    section "2(m)"
    rule "Tamil Nadu Factories Rules, 1950, Rule 4"
  }

  owner "Joint Director (Licensing)"
  review_status unverified
  review_cadence 1 year

  test "ten workers with power"      { given { profile.workers_total = 10 workers,
                                               profile.uses_power = true }  expect fires }
  test "twenty workers without power"{ given { profile.workers_total = 20 workers,
                                               profile.uses_power = false } expect fires }
  test "nine workers with power"     { given { profile.workers_total = 9 workers,
                                               profile.uses_power = true }  expect not fires }
  test "nineteen without power"      { given { profile.workers_total = 19 workers,
                                               profile.uses_power = false } expect not fires }
}
```

Placeholders in `explain` are `{profile.<var>}`, `{approval.title}`,
`{legal_basis}` and `{rule.id}`. They are validated at compile time; a
placeholder naming a variable that does not exist is an error, because it would
reach a citizen as raw template text. A quantity renders in the unit the profile
declares, so the explanation and the threshold the rule tested are stated in the
same terms.

---

## 14. Diagnostic codes

| Range | Phase |
|---|---|
| `SRL0001`–`SRL0037` | Lexing and parsing |
| `SEM0001`–`SEM0056` | Name resolution and type checking |
| `LINT0001`–`LINT0070` | Regulatory content governance |
| `CMP0001` | Compilation — cycles |
| `KG0001`–`KG0020` | Content loading and boundary layers |

Every diagnostic carries a position, a stable code and, where there is an
obvious fix, a hint.
