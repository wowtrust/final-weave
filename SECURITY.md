# Security Policy

FinalWeave is currently a design and protocol specification project. There are no supported production releases, binaries, containers, network deployments, or compatibility guarantees yet. Security reports may still identify specification flaws that could affect a future implementation.

## Report privately

Do not open a public issue for a suspected vulnerability. Use GitHub's private vulnerability reporting page:

https://github.com/wowtrust/final-weave/security/advisories/new

Include, when available:

- the affected document, section, object, state transition, or proposed implementation boundary;
- the attacker model and required capabilities;
- the safety, liveness, availability, confidentiality, integrity, finality, or resource impact;
- a minimal trace, conflicting states, proof, test vector, or reproduction;
- whether the issue crosses ledgers, epochs, validator sets, recovery states, or trust boundaries;
- any suggested mitigation and its compatibility cost.

Never include real production credentials, customer data, private keys, or unrelated personal information.

## Security-sensitive areas

Reports are especially useful for:

- quorum intersection, equivocation, slot decisions, stable-prefix derivation, and epoch transitions;
- BatchAC reconstruction, erasure coding, data-availability acknowledgement, and retention rules;
- canonical encoding, domain separation, signatures, validator-set chains, object IDs, and state commitments;
- deterministic execution, access declarations, MVCC validation, serial fallback, gas, and resource accounting;
- ExecutionAttestation, FinalityCertificate, FinalityProof, checkpoint trust, and proof-carrying queries;
- WAL, atomic publication, snapshots, pruning, synchronization, replay, and crash recovery;
- peer authentication, admission control, malformed or Byzantine inputs, and amplification limits;
- cross-ledger message uniqueness, source proof validation, relaying, and trust policy.

## Response and disclosure

Maintainers will acknowledge a complete report when possible, assess severity and affected design surfaces, and coordinate remediation and disclosure through the private advisory. A specification fix may require an ADR, versioned schema or algorithm, new negative tests, and an explicit compatibility or epoch-activation plan.

No response-time or remediation SLA is promised while the project remains pre-implementation, but good-faith reports will be handled with care and credited when the reporter wishes and coordinated disclosure permits.

## Public hardening work

General threat-model discussion, test improvements, bounded-resource design, and non-exploitable hardening proposals may use normal issues. When unsure whether details are safe to disclose, report privately first.
