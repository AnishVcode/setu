# ADR 0006 — A required approval's prerequisites are themselves required

**Status:** accepted
**Date:** 2026-09-04

## Context

`depends_on` states that one approval cannot be granted before another. It is
not obvious whether that should also make the prerequisite required.

## Decision

It does. If an approval is required, every prerequisite reachable from it is
required, transitively. The propagation carries the lowest authority in the
system (precedence 0 in effect), so an explicit statutory exemption on the
prerequisite always outranks it.

Dependencies are **conjunctive**: two prerequisites mean both. Where an approval
waits on whichever of several alternative authorities has jurisdiction, the edge
is created conditionally by a rule and exists only when that rule fires.

## Rationale

There is no way to obtain a fire NOC that depends on a building plan approval
without obtaining the building plan approval. Modelling the edge without the
implication would leave the applicant with a case stuck in
`WAITING_ON_PREREQUISITE` for an approval nobody told them to apply for.

The conjunctive reading was forced by a real bug found while running the worked
example: the fire NOC declared both the CMDA and the DTCP planning permissions
as prerequisites, intending "whichever applies", and the engine correctly
required both. Conditional edges express the alternative properly, and they are
keyed on the same fact as the trigger.

## Consequences

- A rule that feeds a dependency edge into an approval must not also read that
  approval's outcome. The compiler rejects it as a cycle — the graph would have
  no fixed point — and this is how the mistake was caught.
- The compiler orders rules that may create an edge into an approval before that
  approval is settled.
- Cycle detection is over *potential* edges, including conditional ones. A cycle
  among edges that might exist is a modelling error even if no profile activates
  them all at once.
