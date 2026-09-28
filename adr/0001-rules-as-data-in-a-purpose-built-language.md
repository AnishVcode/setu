# ADR 0001 — Rules are data in a purpose-built language, not a BRMS or code

**Status:** accepted
**Date:** 2026-09-04

## Context

Principle P1 requires that every legal requirement, eligibility test, fee,
timeline and dependency be computed by a deterministic engine over versioned
data. The obvious alternatives are an off-the-shelf business rules engine
(Drools, ODM) or expressing rules directly in the host language.

## Decision

Build a purpose-built typed language, SRL, with its own lexer, parser, type
checker, linter and evaluator.

## Rationale

An off-the-shelf BRMS gives us execution. It does not give us the four things
that actually decide whether this platform is trustworthy:

1. **Dimensional typing.** No BRMS will reject a comparison between rupees and
   kilolitres, or silently converting tonnes-per-annum to tonnes-per-day. In a
   regulatory engine that class of error produces a confident, wrong, legally
   consequential answer. We need it to be a compile error.
2. **Three-valued semantics as a first-class outcome.** Every BRMS we examined
   treats a missing fact as false. That silently omits approvals an applicant
   legally needs. We need "I do not know, and here is the question" to be a
   normal result, with the relevant questions computed by relevance.
3. **Governance in the source.** Statutory citation, named owner, review
   cadence, review status and test cases have to live in the same block as the
   logic, and the toolchain has to refuse to publish without them. Metadata
   held elsewhere drifts from the rule it describes.
4. **Legibility to a lawyer.** The person who signs off a rule is not an
   engineer. Decision tables and Rete networks are not something a legal
   reviewer can read and attest to. SRL source is.

Expressing rules in Go was rejected outright: it makes law a deployment, puts
the change outside the reach of the regulatory content team, and provides no
way to pin, hash, sign and reconstruct the exact logic applied to a case.

## Consequences

- We own a language: grammar, diagnostics, editor support and documentation are
  ongoing costs. `docs/rule-language.md` is the specification.
- The function set is closed. A rule cannot call out to arbitrary code, which is
  a constraint on expressiveness we accept in exchange for totality and
  determinism.
- Compound dimensions are deliberately unsupported. If a statutory threshold
  ever needs one, this decision is revisited rather than worked around.
