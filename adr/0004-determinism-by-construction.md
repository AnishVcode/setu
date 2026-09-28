# ADR 0004 — Determinism is structural, not a matter of care

**Status:** accepted
**Date:** 2026-09-04

## Context

P1 and P6 require that the same profile and ruleset version always produce
identical output, and that a determination be reproducible years later, in a
tribunal, from stored artefacts alone.

## Decision

Determinism is enforced by construction:

- Exact rational arithmetic (ADR 0002).
- No clock. Every command that depends on a date takes `--as-of` explicitly;
  there is no default of "now" anywhere in the toolchain.
- No randomness or network access in evaluation.
- No bare `map` iteration on any path that affects output. Every traversal goes
  through a sorted accessor on `kg.Graph`.
- Every output collection is sorted before emission; ties break on identifier.
- A published ruleset embeds its source and boundary layers and is content
  hashed and signed.
- The determination carries a content hash of itself.

## Rationale

Go randomises map iteration deliberately, so a single `range` over a map on an
output path is enough to make two runs differ. That would not fail a unit test
reliably — it would fail occasionally, in production, in a way nobody could
reproduce. Structural rules plus a determinism property over generated profiles
catch it; discipline alone does not.

Refusing a default date is the part most likely to be questioned. It makes the
CLI more verbose. It also means a CI failure can be replayed exactly, an
effective-dated ruleset does not start failing its own tests when a rule comes
into force, and a golden scenario means the same thing next year.

## Consequences

- Callers must supply the anchor date, the language and the boundary layers.
  This is more work at every call site and it is the point.
- `TestDeterminism` runs 50 evaluations and compares bytes; the property suite
  re-runs every generated profile and compares hashes.
