# ADR 0003 — Unknown is a truth value, not a missing input

**Status:** accepted
**Date:** 2026-09-04

## Context

An applicant completes a profile incrementally. At any moment most variables are
unanswered. The engine has to say something useful about a partial profile
without misleading anyone.

## Decision

Conditions evaluate under Kleene three-valued logic. An unanswered variable
yields UNKNOWN, which propagates. An approval whose outcome depends on an
unknown is reported as undetermined, with the exact questions that would settle
it.

## Rationale

The alternative — coercing a missing answer to false — silently omits approvals
the applicant legally needs. That is the single most damaging failure this
platform could have: it is invisible, it looks like success, and the applicant
discovers it when an inspector arrives.

Making unknown a value rather than an error also gives the platform its most
useful behaviour: a determination over a half-finished profile is still correct
about everything it is definite about, and precise about what it needs next.

## Soundness

The design rests on **monotonicity**: answering a further question may turn
UNKNOWN into TRUE or FALSE, but must never turn TRUE into FALSE. That is what
lets us show a partial determination without misleading anyone. It is asserted
as a property over generated profiles, and it caught a real bug during
development — precedence resolution was reporting "definitely required" while a
higher-precedence exemption was still undetermined.

`defined()` is deliberately non-monotone and is the documented exception. The
property suite detects any rule using it and reports that the check was skipped,
naming the rules, rather than weakening the guarantee.

## Consequences

- Relevance must be computed, not approximated: an unknown result reports only
  the variables that actually determined it. Asking an applicant a question
  whose answer cannot change anything is a defect.
- Every profile variable must carry citizen-facing question text in every
  supported language, because that text is what an unresolved determination
  shows. The linter enforces it.
