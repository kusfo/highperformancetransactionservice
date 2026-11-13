# Account creation

The Account service implements authenticated `POST /accounts` (T18).
Run it with the TLS configuration described in [HTTP runtime](http-runtime.md)
and a dedicated financial HTTP login from [database roles](database-roles.md).

Send `Authorization: Bearer <accessToken>` and `Content-Type: application/json`.
The required body is an object with optional `displayName` and `description`
strings, defaulting to empty strings. `{}` creates an account without metadata.
Limits are 100 and 1,000 Unicode characters respectively. Whitespace and Unicode
are preserved. Null, NUL, malformed Unicode, duplicate/unknown fields and
non-string values are rejected. Owner, number, balance and status cannot be supplied.

The authenticated identity supplies the owner. The server assigns an opaque
unique account number and returns `201 Created`, a relative
`Location: /accounts/{accountNumber}`, and the public account representation:
`accountNumber`, `userId`, `displayName`, `description`, `balance`, `status`,
and UTC `createdAt`. The initial balance is zero and status is active.
Responses use `Cache-Control: no-store`.

Creation is **not idempotent**: repeated accepted requests create separate
accounts. Any `Idempotency-Key` header, including an empty value, is rejected
with `400 INVALID_REQUEST`. Do not automatically retry an ambiguous timeout
or server error. The Location identifies the account, but GET is a later task.

Missing/invalid authentication returns 401; invalid metadata returns 400,
unsupported media types return 415, and storage failures return a generic 500.
The shared runtime bounds request bodies and deadlines. No database details,
submitted metadata or bearer tokens are included in error diagnostics.

## Tests

`make check-all` runs strict-input and response unit tests, plus real PostgreSQL
tests using the financial HTTP login over HTTPS. They verify multiple accounts
per user, distinct server-assigned numbers, ownership, zero balances and persisted
Unicode metadata. Existing repository tests cover concurrency and database guards.
