# Account persistence

T17 adds `financial.accounts` through the append-only migration
`000003_accounts.sql` and `src/financial.Accounts`. Apply migrations with
`make migrate`; no Account HTTP routes are implemented by this task.

## Identity and creation

`Create(ctx, trustedUserID, Metadata)` generates an internal UUID and a separate
128-bit random account number encoded as 32 lowercase hexadecimal characters.
Numbers are opaque strings, not amounts or bank identifiers. Preserve case and
leading zeros; do not convert them to integers. The database uses bytewise `C`
collation for number equality and ordering, including future pagination ties.
The storage limit is 128 bytes; generated numbers use 32.

Both IDs are unique. Creation retries generated-ID collisions at most three
times using `ON CONFLICT DO NOTHING`, so a collision does not abort a caller-owned
transaction. Other errors return a fixed storage error; cancellation is preserved.
Creation has no request idempotency: retrying after an ambiguous commit can create
another account. The repository can use a connection, pool or caller-owned
transaction; a transaction caller must commit before reporting success.

An account starts active with balance zero, generation 1, head/next sequence 1,
and no retry deadline. Database defaults and an INSERT trigger enforce this
state even when an SQL caller supplies financial fields. Owners must be trusted
UUIDs from authenticated entry points. There is deliberately no identity foreign
key or activity lookup: financial storage is independent of identity lifecycle
writes. T18 must authenticate before invoking creation. Multiple accounts per
owner are supported.

## Metadata and retained state

Metadata defaults to empty strings. Display names allow 0–100 Unicode code points;
descriptions allow 0–1,000. Validation rejects malformed UTF-8 and NUL, preserves
whitespace/normalization and counts code points, not bytes. PostgreSQL also limits
character counts and rejects NUL. HTTP-specific validation of omitted/null,
duplicate and unknown fields belongs to T18/T20.

`ByNumber(ctx, ownerID, number)` returns the same not-found outcome for missing
and other-owner accounts. Disabled accounts remain readable. Public JSON fields
match the account contract; internal ID, generation, sequence and retry state are
excluded. T19 supplies pagination and HTTP routes.

Database checks enforce balances 0–2,147,483,647 cents, active/disabled status,
zero balance while disabled, positive generations and
`1 <= head_sequence <= next_sequence`. Sequence/generation columns are bigint;
Go state and balances use int64. Updates cannot change identity, number, owner or
creation time, or decrease counters. DELETE is rejected: rows and numbers are
retained. Metadata remains mutable on disabled accounts. Application roles must
also lack DELETE/TRUNCATE/DDL privileges; T17-A defines role enforcement.

These constraints do not implement balance processing or prove multirow financial
consistency. T21 adds operations, history, outbox, closures and the latest-closure
reference with its foreign key. Later transactional paths own acceptance,
processing, disable/reactivate transitions and overflow handling. No generic
balance/status mutation method is exposed by this repository.

## Indexes and validation

Indexes cover unique account-number lookup, owner/newest-first pagination and
active accounts with pending work, ordered by retry deadline then internal ID.
Typed UUID predicates avoid casts on indexed columns.

Run `DOCKER_CONTEXT=desktop-linux make check-all`. Unit tests cover metadata,
identifier collisions/retry bounds, safe errors, cancellation and JSON isolation.
Real PostgreSQL fixtures cover migration reruns, initial state, metadata retention,
owner isolation, opaque numbers, multiple/concurrent creations, rollback,
uniqueness, immutable keys, balance/counter constraints and disabled reads.
Populated 10,000-account fixtures verify normal planner index use for the actual
repository lookup, owner ordering and ready-account discovery without disabling
sequential scans. Endpoints and financial lifecycle behavior remain later tasks.
