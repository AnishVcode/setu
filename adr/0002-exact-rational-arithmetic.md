# ADR 0002 — All arithmetic is exact rational, never floating point

**Status:** accepted
**Date:** 2026-09-04

## Context

The engine computes fees, thresholds and entitlement values. P1 requires
byte-identical output for the same inputs, and P6 requires a determination to be
reconstructible years later.

## Decision

Every number in the language and the engine is a `math/big.Rat`. Unit conversion
factors are exact rationals. No evaluation path touches `float64`.

## Rationale

Binary floating point cannot represent `0.1`, and the error it introduces is not
stable across architectures, compiler versions or optimisation levels. A fee
schedule that says "one quarter of the investment" means exactly that. A
threshold at `22.75 litres` must compare the same way every time, on every
machine, for the life of the platform.

`0.3048` as a conversion factor is `3048/10000`, not the nearest double.
Otherwise `500 ft` compared against a `150 m` threshold could land either side
of the line depending on where it was evaluated.

## Consequences

- Arithmetic is slower. Measured against a 5,000-rule target this is far inside
  the p95 budget, and the correctness is not negotiable.
- Rendering needs care: an exact rational may not have a terminating decimal.
  `Value.String` renders a terminating decimal where one exists and a fraction
  otherwise, and `ratLiteral` renders nine places when emitting SRL source.
- Rounding is explicit per fee (`rounding nearest INR 1`), never implicit.
- Geodesy is the one exception: there is no exact rational haversine. Distances
  are computed in `float64` and **rounded to whole metres before re-entering rule
  evaluation**, so the values the rules see are exact. Statutory buffers are
  stated in whole metres, so no legal precision is lost.
