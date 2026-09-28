# ADR 0007 — Static analysis distinguishes what it proved from what it searched for

**Status:** accepted
**Date:** 2026-09-04

## Context

Spec 5.5.4 requires the compiler to detect contradictory rules, unreachable
rules, dependency cycles and orphaned approvals, and to block publication. Two
of those are decidable by graph traversal. The other two are satisfiability
problems over a rich type system, and a complete decision procedure would mean
embedding an SMT solver.

## Decision

Implement each check at the strength it can honestly claim, and label every
finding with whether it was **proven** or merely **searched for**:

- **Cycles, orphans, missing test coverage, type errors** — decided exactly.
- **Unreachable rules** — proven for contradictory bounds, equalities and
  boolean/enum constraints on a single variable, via interval and domain
  analysis. Anything else is a witness search, reported as "no satisfying
  profile found in N attempts".
- **Contradictions** — detected by finding an actual profile that makes two
  opposing rules of equal precedence both fire. A hit is proof and the witness
  is printed as a pasteable `given` block. A miss is reported as a warning that
  says explicitly it is not proof of absence.

The witness search draws candidate values from the literals the rules themselves
compare against, plus the values immediately either side, because a condition
changes exactly at its thresholds and almost nowhere else.

## Rationale

A checker that overstated its certainty would be worse than none. An author told
"this rule is unreachable" will delete it; if the analysis merely failed to
understand the condition, a legal requirement disappears. Conversely a
contradiction found with a concrete witness is not a heuristic — it is a profile
that provably breaks, and it goes straight into the test suite.

## Consequences

- The analyser must not cry wolf. When it reported a perfectly reachable
  geospatial rule as possibly unreachable — because the probe grid was coarser
  than the polygon — that was fixed by probing feature centroids, not by
  lowering the finding's severity.
- A rule proven unreachable suppresses contradiction findings that involve it:
  one defect, one finding.
- Adopting an SMT solver later is a strict improvement and does not change the
  reporting contract.
