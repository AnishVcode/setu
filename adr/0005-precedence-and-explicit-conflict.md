# ADR 0005 — Precedence resolves exemptions; equal-authority conflicts are surfaced, never resolved

**Status:** accepted
**Date:** 2026-09-04

## Context

A general rule and a sector exemption routinely apply to the same applicant.
Something has to decide which governs, and P1 forbids letting file ordering or
evaluation order decide it.

## Decision

Every rule carries a numeric precedence — 100 for triggers, 200 for exemptions
by default. For each approval, the outcome is decided by the highest precedence
among rules that definitely fired. Where requirements and exemptions meet at the
same precedence, the engine emits an explicit `Conflict`, marks the approval
undetermined, and routes the case to a human.

An undetermined rule can overturn a decided outcome only if its precedence is at
least the deciding precedence *and* it pulls the other way; in that case the
approval is undetermined and the questions that would settle it are reported.

## Rationale

Silently picking a winner between two provisions of equal authority is a legal
error dressed as a feature. It would be invisible, it would be wrong for some
applicants, and nobody would find out. Surfacing it costs one case going to a
human and gains a defect report against the regulatory content team, which is
exactly where the problem should land (spec 13's discrepancy loop).

The rule about undetermined higher-precedence rules is not an edge case. It is
the difference between telling an applicant "you need a trade licence" and "you
need a trade licence unless you are in an industrial estate, and you have not
told us which". The first is wrong for a large class of real applicants.

## Consequences

- `overrides [R.X]` with a precedence not exceeding `R.X` is a lint error: the
  exemption could never take effect.
- The analyser searches for profiles that trigger both sides of an
  equal-precedence pair and prints the witness as a pasteable test case.
- Determinations can be undetermined for reasons other than a missing answer,
  and the UI must distinguish "we need more from you" from "the law as encoded
  is contradictory here".
