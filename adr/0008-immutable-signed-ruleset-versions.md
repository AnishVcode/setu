# ADR 0008 — A published ruleset embeds its own content, and is hashed and signed

**Status:** accepted
**Date:** 2026-09-04

## Context

P6 requires that it be possible to reconstruct, years later, exactly what the
system told an applicant and why. A case pins a ruleset version at submission
and is evaluated against it for its entire life.

## Decision

A published version is a single self-sufficient artefact containing every source
file with its digest, the boundary layers and their digest, the identifier
index, the governance posture, and an Ed25519 signature over a content hash of
the whole manifest. Versions are immutable: republishing an existing version
fails rather than overwriting, and the file is written mode `0444`.

Signature verification checks the **recomputed** hash, not the hash recorded in
the file.

## Rationale

A reference to a git tag is not enough. Repositories are rewritten, branches
move, and a tribunal reading the file five years from now should not have to
trust that a build system resolved a reference correctly. Embedding the content
means the snapshot alone answers the question — which is asserted by a test that
deletes the source directory and rebuilds the graph from the snapshot.

Verifying against the recorded hash rather than the recomputed one was an actual
bug during development: a tampered snapshot reported "signature valid" because
the attacker had left the hash field untouched. A signature must attest to the
bytes present, not to a claim about them.

Deemed-approval provisions are listed on every snapshot. Spec 15 treats their
misuse as a headline risk, so which approvals permit deemed approval — and under
which cited section — is a published fact about a version, not a setting buried
in configuration.

## Consequences

- Snapshots are large; the content is embedded rather than referenced. At the
  size of a jurisdiction's ruleset this is measured in megabytes and is the
  right trade.
- The development signing key written by `setuctl keygen` is explicitly not a
  production key. Spec 7 requires HSM-backed, non-exportable keys under a
  documented ceremony; a key readable off a filesystem is not one.
- The production publish gate refuses any content that is not `verified` with a
  named owner, so an unreviewed ruleset cannot reach an applicant by accident.
