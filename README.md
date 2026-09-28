# SETU — Regulatory Intelligence Engine

The deterministic rules engine and regulatory content toolchain for SETU: the
component that decides, from an applicant's Project Profile, exactly which
approvals the law requires, why, from whom, at what cost, in what order, and
what remains unknown.

This is **Phase 1, vertical slice**: §5.5 of the build specification, built to
depth, with a seeded Tamil Nadu food-processing ruleset so the worked example in
§21 runs end to end. It is not the whole platform. §"What this is not" below is
explicit about the boundary.

---

## Why this component first

Everything else in the specification derives from it. Orchestration needs the
dependency DAG and the statutory SLAs. Payments needs the computed fee lines and
their treasury heads. The Compliance Twin needs the obligations. Risk routing
needs the flags. The Officer Console needs the derived checklists. The AI
assistant is permitted to speak only about what this engine produced.

It is also the one part that can be genuinely complete on its own rather than a
stub waiting on twenty other services — and the specification says to build it
with more care than anything else.

---

## The console

A browser front end, served by the engine itself:

```bash
make serve            # or: go run ./cmd/rulesd
```

Then open **http://localhost:8080**. One self-contained binary — the web assets
are embedded, there is no build step, no package manager and no external CDN.

The left pane is the Project Profile. Every question on it is generated from
`/api/schema`, so the questions come from the encoded law with the wording their
author wrote — adding a rule that reads a new variable puts the question on the
form by itself. The right pane re-evaluates against the live engine on every
edit, in single-digit milliseconds.

Two things worth doing in it:

- **Clear all**, then answer questions one at a time. Approvals appear as the
  law starts to bite, and *Still to answer* lists only the questions whose
  answers could still change the outcome.
- Open any approval and press **Show the derivation** — the rule that decided
  it, its precedence, and the condition tree with each sub-expression and what
  it evaluated to.

```
--addr      127.0.0.1:8080          address to listen on
--content   content/jurisdictions/IN-TN
--scenarios content/goldens         example profiles offered in "Start from"
--as-of     YYYY-MM-DD              pin every evaluation to one date
--timezone  Asia/Kolkata            whose civil date counts as "today"
```

The engine never reads a clock. The service decides what "today" means at the
request boundary, in the jurisdiction's timezone, and passes the date in
explicitly — so a determination stays reproducible. `--as-of` pins it, which is
what a demo wants.

### HTTP API

| Endpoint | Purpose |
|---|---|
| `GET /api/ruleset` | package, counts, governance posture, deemed-approval provisions |
| `GET /api/schema` | the Project Profile schema — what drives the form |
| `GET /api/scenarios` | example profiles |
| `POST /api/evaluate` | `{profile, asOf, language, trace}` → the full Determination |
| `POST /api/whatif` | `{scenarios:[{name, profile}], asOf}` → side-by-side comparison |

---

## Quick start

```bash
go build ./...
go test ./...

# Check the encoded law: parse, type-check, lint, compile.
go run ./cmd/setuctl check   --as-of 2026-09-04 content/jurisdictions/IN-TN

# Run the authored rule tests and the property suite.
go run ./cmd/setuctl test    --as-of 2026-09-04 content/jurisdictions/IN-TN

# Search for unreachable rules and contradictory rule pairs.
go run ./cmd/setuctl analyze --as-of 2026-09-04 content/jurisdictions/IN-TN

# The worked example: Priya's food-processing unit.
go run ./cmd/setuctl eval    --as-of 2026-09-04 \
    --profile content/goldens/priya-coimbatore.json content/jurisdictions/IN-TN

# Why is this approval required, and which rule decided it?
go run ./cmd/setuctl explain --as-of 2026-09-04 \
    --profile content/goldens/priya-coimbatore.json \
    --approval TN.LOCALBODY.TRADE_LICENCE content/jurisdictions/IN-TN

# Site selection: compare three districts.
go run ./cmd/setuctl whatif  --as-of 2026-09-04 \
    --profile content/goldens/priya-coimbatore.json \
    --profile content/goldens/priya-chennai.json \
    --profile content/goldens/priya-ariyalur.json content/jurisdictions/IN-TN
```

Every command that depends on a date requires `--as-of`. There is no default of
"now" anywhere in the toolchain — see ADR 0004.

---

## What runs today

Against the seeded Tamil Nadu content — 37 rules, 20 approval types, 15
departments, 20 documents, 14 fee schedules, 12 obligations, 7 incentives, 30
profile variables:

| Step of §21 | Status |
|---|---|
| 1. Discovery with plain-language reasons, statute, department, fee, SLA | **runs** |
| 1. Expandable derivation trace showing which threshold triggered it | **runs** (`setuctl explain`) |
| 2. What-if across districts with live diff of approvals, fees, timeline | **runs** (`setuctl whatif`) |
| 4. Dependency DAG, parallel dispatch waves, critical path | **runs** (computed, not orchestrated) |
| 10. Incentive matching with entitlement values and deadlines | **runs** |
| 11. Obligations materialised from each issued approval | **runs** (emitted; the Twin that tracks them is not built) |

For Priya's Coimbatore scenario the engine returns 14 approvals across 12
departments, 16 deduplicated documents, ₹2,05,500 in fees split across nine
treasury heads, 10 recurring obligations, 5 matched incentives worth ₹46 lakh,
and a **120-day critical path against 419 days sequential — a 71% reduction**,
with every line carrying its statutory citation.

### The guarantees that are actually enforced

- **Determinism.** Same profile, same ruleset version, same as-of date →
  byte-identical output including a content hash. Asserted over 50 repeat runs
  and over every generated profile in the property suite.
- **Never guesses.** An unanswered question yields UNKNOWN, propagated by Kleene
  logic, surfaced as an unresolved determination carrying the exact question to
  ask — and only the questions whose answers could change the outcome.
- **Never silently resolves a contradiction.** Two provisions of equal authority
  saying opposite things produce an explicit conflict and route to a human.
- **Unit-safe.** Comparing rupees to kilolitres, or tonnes-per-day to
  tonnes-per-annum, is a compile error.
- **Exact arithmetic.** `math/big.Rat` throughout; no `float64` on any
  evaluation path.
- **Cited.** Every required approval carries its statute and section; a
  determination without one fails the property suite.
- **Effective-dated.** A case pins a ruleset version and is judged by the law in
  force when it was submitted.
- **Tamper-evident.** A published version embeds its own source, is content
  hashed and Ed25519 signed, and verification checks the recomputed hash.

---

## Publication gates

`setuctl publish` refuses to emit a version unless all five gates pass:

| Gate | Requirement |
|---|---|
| 1 | Zero errors from parsing, type checking, linting and compilation |
| 2 | Every authored rule test passes |
| 3 | Every rule has both a positive and a negative test case |
| 4 | The property suite is clean over generated profiles |
| 5 | The version exceeds the last published one |

`--production` additionally requires every rule, approval, fee and obligation to
be `review_status verified` with a named owner. The seeded content is
`unverified` throughout, so a production publish of it is correctly refused —
that is the gate working, not a gap.

---

## Repository layout

```
pkg/unit        dimensional analysis; exact rational conversion factors
pkg/value       runtime values, Kleene three-valued logic, civil dates, geometry
pkg/srl         the rule language: lexer, AST, parser, formatter
pkg/sema        name resolution, type and dimension checking, content lints
pkg/kg          the knowledge graph: merged, indexed, deterministically ordered
pkg/compile     evaluation order, dependency edges, cycle detection
pkg/eval        the deterministic engine and the Determination it produces
pkg/testkit     authored rule tests and the property-based suite
pkg/analysis    reachability, contradiction witnesses, orphan detection
pkg/discovery   scenario comparison for site selection
pkg/ruleset     immutable signed published versions
cmd/setuctl     the toolchain: check, test, analyze, eval, explain, whatif,
                publish, verify, keygen
content/jurisdictions/IN-TN   the seeded ruleset
content/goldens               golden scenarios and their expected outputs
test/golden                   the release-gating regression suite
docs/rule-language.md         the language specification
adr/                          architecture decision records
```

---

## The seeded content is not law

Every declaration in `content/jurisdictions/IN-TN` carries
`review_status unverified`. It was written from public sources by an engineer,
not a lawyer. Several thresholds are known simplifications and are flagged
individually with `REVIEW:` notes in the source. The boundary polygons in
`layers.json` are illustrative rectangles, not surveyed boundaries.

The toolchain enforces this rather than relying on a warning: the production
publish gate refuses unverified content, and a non-production snapshot records
in its manifest that it must not be served to applicants.

Before any of it becomes law-in-software it must go through the four-eyes
workflow of §5.5.5 — author, legal reviewer, department sign-off.

---

## What this is not

This slice is §5.5. It is not the other twenty-one services, and none of the
following is built:

- Case management, the state machine, SLA clocks, escalation or deemed approval
  (§5.6, §5.7). The engine emits the DAG, the statutory SLAs and the
  deemed-approval provisions the orchestrator would need; it does not run them.
- Identity, the Business Passport, the Verified Data Vault, or consent
  enforcement (§5.1–5.4). The engine names the documents and their verification
  pathways; nothing verifies or stores them.
- Payments, issuance, the Compliance Twin, grievances, notifications, search,
  analytics, the AI assistant, or the adapter fleet.
- Any client surface. `setuctl` is a toolchain, not the nine surfaces of §6.
- The Rule Studio UI. The toolchain underneath it exists — authoring, checking,
  testing, impact-relevant analysis, publication, verification — but the visual
  editor, the four-eyes workflow and the impact-analysis dashboard of §5.5.5 are
  not built.

Two things specified for this component are also not built: a canonical
formatter (`setuctl fmt`), and ruleset diffing and cross-version impact analysis
(§5.5.5). The snapshot format carries everything they need — embedded content,
an identifier index, and hashes — so both are additive.

---

## Development

```bash
make check      # build, vet, unit tests
make content    # check, test and analyse the seeded ruleset
make golden     # the release-gating regression suite
make gates      # everything CI runs
```

Regenerating golden expectations (`go test ./test/golden -update`) produces a
diff that must be reviewed by someone qualified to say the new answer is the
lawful one. Regenerating without review defeats the purpose of the suite.
