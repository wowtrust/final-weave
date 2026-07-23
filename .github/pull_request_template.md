## Linked Issue

Fixes #

## Summary

-

## Required Format

- Branch name uses an approved prefix: `feat/`, `fix/`, `docs/`, `test/`, `refactor/`, `perf/`, `security/`, `chore/`, `ci/`, `build/`, `release/`, or `revert/`.
- PR title uses Conventional Commit style, for example `fix(consensus): reject equivocated proposer slots`.
- Commit messages use Conventional Commits.

## Scope And Project Status

- [ ] This PR solves one issue or one clearly scoped maintenance task.
- [ ] This PR excludes unrelated formatting, experiments, generated files, local data, credentials, logs, captures, and build artifacts.
- [ ] User-facing material distinguishes implemented behavior, target design, and roadmap work.
- [ ] Any performance claim identifies its source, hardware, configuration, measurement method, and whether it is a FinalWeave result.

## Protocol, Safety, And Compatibility

- [ ] This PR does not change quorum, Byzantine fault, proposer-slot, stable-prefix, or epoch-transition rules.
- [ ] BatchAC still proves recoverable data availability only; it is not treated as transaction validity, ordering, or execution finality.
- [ ] Canonical ordering and speculative parallel execution remain deterministic and equivalent to canonical serial `Apply`.
- [ ] External finality remains bound to an authenticated Header, exact FinalityCertificate, locally trusted validator/config chain, and query-specific proofs.
- [ ] This PR does not change canonical encoding, domain separation, signatures, IDs, state commitments, or wire schemas.
- [ ] WAL, atomic publication, snapshots, pruning, synchronization, replay, and recovery preserve their durable safety boundaries.
- [ ] This PR does not change networking, admission, cross-ledger proof validation, or resource-accounting boundaries.
- [ ] No production path introduces unbounded scan, sort, load, verification, retry, amplification, or recomputation behavior.

For every unchecked box or affected boundary, explain the ADR, proof obligation, schema or algorithm version, epoch activation, migration, recovery, compatibility, validation, and rollback impact. A boundary is not exempt merely because it received review:

-

## Validation

- [ ] `git diff --check`
- [ ] `python3 scripts/check_docs.py`
- [ ] Relevant unit, race, property, fuzz, vector, model, Byzantine, partition, crash-recovery, snapshot, chaos, and performance checks
- [ ] Negative tests cover malformed, conflicting, stale, replayed, oversized, and partially durable inputs where applicable

Checks not run and residual risk:

-

## Risk And Rollback

-
