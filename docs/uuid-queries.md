# UUID query predicates

Identity lookups and mutations compare UUID columns directly to typed UUID
parameters. Casts to text remain only in returned values. This allows PostgreSQL
to use the existing users primary key and sessions user-ID index; no migration
or new index is needed.

`postgres.UUIDKey` accepts canonical lowercase UUIDs. Other representations become
SQL NULL parameters, so equality matches no rows. This preserves prior behavior:
user/profile lookups report not found, session issuance rejects the user, and
all-session revocation is a no-op. Invalid inputs cannot cause a UUID cast error
or abort a caller-owned transaction. Cancellation and storage failures continue
through the existing database error handling.

`TestUUIDPredicatesUseIndexes` captures actual repository/service SQL, including
password-change transaction queries, and runs EXPLAIN against fixtures with
10,000 unrelated users and sessions. It checks indexed UUID conditions without
forcing planner settings. `TestInvalidUUIDPredicatesPreserveOutcomes` checks
invalid and unknown IDs, transaction usability and session preservation.

Validation on 2026-11-03: `DOCKER_CONTEXT=desktop-linux make check-all` passed
contracts, orchestration checks, formatting/vet, all five builds, unit/race tests
and PostgreSQL integration. All eight captured queries used indexed UUID
conditions. Invalid-ID tests preserved prior outcomes and transaction usability.
No fixture databases remained; resource cleanup exited successfully.
Query plans establish index use for these fixtures, not a throughput guarantee.
