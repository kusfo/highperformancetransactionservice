# User login

T12 implements unauthenticated `POST /sessions` over the User service's HTTPS
runtime. Send an uncompressed JSON object containing only `email` and `password`.
Email uses the existing ASCII trimming/lowercasing policy. Login passwords contain
1–128 Unicode characters and are neither trimmed nor normalized; passwords shorter
than registration's 12-character minimum are accepted as login attempts but fail
with the generic credential response.

A successful response has exactly `accessToken`, `tokenType: "Bearer"`,
`expiresIn: 600` and an RFC 3339 `expiresAt`. All responses use `Cache-Control:
no-store`. The token is returned only after its PostgreSQL transaction commits.
Every successful login issues a fresh token; no refresh behavior is added.

## Failures and resource limits

Unknown emails, wrong passwords, inactive users and malformed persisted hashes
return identical `401 INVALID_CREDENTIALS` JSON responses. Unknown users verify
against a fixed public dummy hash with the same Argon2 profile. A malformed hash
also incurs dummy verification work. Dummy matches cannot authorize a session.
These measures avoid a missing-hash shortcut; they do not promise identical total
latency, since database and lock waits vary.

Malformed input, unknown/duplicate fields, null/wrong types, invalid email and
empty/overlong passwords return `400 INVALID_REQUEST`. Storage/commit failures
return fixed `500 INTERNAL_ERROR` responses without tokens or raw error details.
The shared runtime still handles body limits, content type, timeouts and redacted
logging. Registration, login and password changes share two active slots per process, bounding
Argon2 memory to approximately 128 MiB plus overhead. Waiting requests respect
cancellation. Network/user-based rate limiting remains future work.

## Transaction and lifecycle ordering

`src/login.Service` begins a READ COMMITTED transaction, selects the canonical
email's user row `FOR UPDATE`, verifies the current hash/active state, issues a
session using the same transaction, and commits. Cancellation is checked before
and after password verification and before commit; rollback uses a separate
bounded cleanup context. Argon2 itself is not interruptible.

Password-change/deactivation code must lock the same user row before changing
credentials/state and revoking sessions in its transaction. A login waiting on
that row observes the committed update before deciding whether to issue a token.
This follows [PostgreSQL READ COMMITTED row-lock behavior](https://www.postgresql.org/docs/16/transaction-iso.html#XACT-READ-COMMITTED).
Password changes are implemented; user deactivation remains a future task. A successful commit followed by a lost HTTP
response may leave an unused session; retrying login creates another session.
Protected-route bearer middleware is available through `src/authn`. Logout and
user deactivation endpoints remain later tasks. See [profile updates](profile.md).

## Running and testing

Use the same TLS and `DATABASE_URL` configuration as registration and apply both
migrations before starting `./bin/user`. `make check-all` runs unit tests,
clock-controlled real HTTPS/PostgreSQL login checks and compiled-binary login.
Integration cases check exact response fields/expiry, canonical email, distinct
tokens visible from another connection, generic failure bodies, log redaction,
storage errors, forced deferred-constraint commit failure and observed row-lock
waits during password/deactivation changes.

Targeted commands (the integration command needs a disposable PostgreSQL server):

```sh
go test -race ./src/login ./src/userapi
go test -race -tags=integration -run '^TestLogin' ./tests/integration
```
