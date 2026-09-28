# ADR 0009 — Go for the engine, with a strict package dependency order

**Status:** accepted
**Date:** 2026-09-04

## Context

The spec's baseline offers Java 21 or Go for core services. This slice is the
rules engine and its toolchain.

## Decision

Go 1.22. Packages depend strictly downward, and nothing depends upward:

```
unit  →  value  →  srl  →  kg  →  sema  →  compile  →  eval
                                                        ↓
                                     testkit  ·  analysis  ·  discovery  ·  ruleset
                                                        ↓
                                                    cmd/setuctl
```

## Rationale

Go was chosen by the project owner from the spec's baseline. It suits this
component specifically: the engine is CPU-bound tree walking over immutable
data, `math/big` is in the standard library, the single static binary makes the
toolchain trivial to run in CI and on an officer's machine, and the absence of
runtime reflection in the hot path keeps evaluation predictable.

The layering is enforced by the absence of import cycles, which Go checks. It
matters because the direction encodes the design: units know nothing about
values, values know nothing about the language, the language knows nothing about
the graph, and the evaluator knows nothing about the CLI.

## Consequences

- `pkg/eval` imports `pkg/compile` and `pkg/sema`, so profile JSON parsing lives
  in `eval` where the schema is available, rather than in a separate package
  that would have to import both.
- Go's randomised map iteration is a live hazard for determinism; ADR 0004
  addresses it structurally.
- The module declares `go 1.22` although the local toolchain is newer, to keep
  the baseline the spec states.
