# T03 Account and Lifecycle Contract

Status: confirmed. Metadata, list and lifecycle choices were accepted during
T03. `api/account.openapi.json` defines the contract; endpoints remain future
implementation work.

## Metadata and account responses

**Confirmed:** create accepts an object with optional `displayName` (0–100 Unicode
characters) and `description` (0–1,000), both defaulting to empty. PATCH requires
at least one of those fields. Omitted fields stay unchanged; empty strings clear
values. Reject null, NUL, unknown/duplicate fields, malformed Unicode and invalid
types. Preserve whitespace and Unicode without normalization. Metadata may be
edited while disabled. Identity, owner, balance, currency and status cannot be
set through metadata requests.

Responses include opaque `accountNumber`, UUID `userId`, both metadata strings,
integer-cent `balance`, `status` (`active`/`disabled`) and UTC `createdAt`.
Account numbers are never recycled; preserve leading zeros and URL-encode them
as path segments. Creation returns 201 and Location; reads/updates return 200.
Disabled accounts always have zero balance.

**Confirmed:** creation has no idempotency guarantee in this version. Reject an
Idempotency-Key header on create instead of silently ignoring it. Clients must
not automatically retry an ambiguous creation response: a second accepted POST
creates a separate account. Confirmed lifecycle retry guarantees do not apply to
creation or metadata updates. Metadata updates use last committed write per field.

## Lists and transaction history

**Confirmed:** opaque keyset cursors, `limit` default 50 and range 1–100, and
`{items, nextCursor}` responses. Null nextCursor ends the list. List all owned
accounts, active and disabled, ordered by `(createdAt, accountNumber)` descending.
History orders by `(createdAt, transactionId)` descending and includes successful
user balance changes and closure withdrawals; pending operations and business
declines belong to the T04 operation API. No status filters or individual
transaction endpoint are introduced here.

A cursor is bound to the authenticated owner, collection and account where
applicable, and must resist tampering. Invalid, expired or mismatched cursors
return 400 INVALID_CURSOR without exposing another owner's data. Cursor encoding
and signing are implementation details. Page-size changes are allowed between
pages. Reject duplicate/unknown query parameters and nondecimal limits. Lists are
not a snapshot across requests: concurrent inserts may require restarting from
the first page; mutable metadata/status/balance reflects each read. Stable sort
keys do not change. An empty owned collection returns 200 with an empty list.

## Lifecycle identity and replay

**Confirmed:** DELETE and reactivation require exactly one UUID `Idempotency-Key`
header and no body. The header value is returned as `lifecycleRequestId`.
Lifecycle IDs are globally unique across users, accounts and actions, in a
namespace separate from balance operationIds and transactionIds. Normalize valid
UUID spelling to its UUID value before comparing keys.

Every successful lifecycle request, including a no-op, atomically records its
result snapshot indefinitely. Reusing a key for another account/action returns
409 LIFECYCLE_ID_CONFLICT. Reuse by another user never exposes the stored result.
Malformed, unauthorized, inaccessible and conflicting attempts reserve no new ID.
A timeout or 5xx is ambiguous; retry the same key and action.

Authenticate first, verify ownership of the target account, then check the global
key. Missing and other-owner accounts share 404 NOT_FOUND. A key collision with
another user on an otherwise owned target returns generic 409, with no original
account, owner or outcome. Known outcomes replay before current lifecycle-state
checks. Historical snapshots are not current-state reads; use GET to refresh.

Responses contain `lifecycleRequestId`, `action`, an `account` snapshot and
`closureWithdrawal` (null or original operationId, transactionId, negative amount,
and balanceAfter zero). A zero-balance closure has no operation or transaction.
A fresh close of an already disabled account returns the existing closure IDs
and no new withdrawal; its own lifecycle key is still recorded. Metadata edits
while disabled do not rewrite older snapshots. A fresh close after reactivation
is a new closure cycle, and must never reuse a prior cycle's withdrawal.

Reactivation returns closureWithdrawal null. Disabled-to-active preserves zero;
an already-active no-op preserves its current balance, which may be positive.
An old reactivation retry after closure returns its recorded active snapshot and
does not reactivate the account again. An old closure retry after reactivation
returns its recorded disabled snapshot and does not close the account again.
Concurrent lifecycle requests linearize relative to each other and processing.
Closure's withdrawal/history/disable/result commit together. Pending operations
at that boundary decline ACCOUNT_DISABLED even after reactivation; T04/T05 define
their representations and persistence design.

## Validation and implementation boundaries

The OpenAPI artifact includes positive-balance and zero-balance closure,
concurrent/repeated closure results, reactivation no-ops and historical replay
examples. Schema checks validate shape and cross-field invariants; example
relationships check documented replay consistency. These checks do not prove
runtime atomicity, ordering or authorization. T05/T33–T38 own those proofs.
Account schema/repositories/endpoints remain future tasks. T03 does not implement
financial operations, broker behavior or overload policy.
