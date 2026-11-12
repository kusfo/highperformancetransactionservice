# User deactivation

`DELETE /users/{userId}` requires the owner's bearer token and no request body.
Success returns `204 No Content` with `Cache-Control: no-store`. Another user's
ID returns `404 NOT_FOUND`; missing, expired or revoked credentials return
`401 UNAUTHORIZED`. Bodies (including `{}`) return `400 INVALID_REQUEST`.

The User binary uses `NewHandlerWithLifecycle`. Authentication runs before
ownership and body validation; only the authenticated user ID reaches
`src/profile.Service.Deactivate`.

## Atomicity and retries

A READ COMMITTED transaction updates the retained user row to inactive, then
permanently revokes every unrevoked session before committing. The user-row
lock serializes with login and password changes. A login that commits first
has its session revoked; login after deactivation cannot issue a session.
Storage failures return a fixed error and roll back both changes. Cancellation
while waiting for the user lock leaves identity and sessions unchanged.

Requests authenticated before deactivation may both complete with `204`.
Subsequent retries return `401` because the caller's session is revoked. A lost
success response therefore cannot be replayed using that token. Manual
re-enablement does not revive old sessions; the user must log in again.
See [identity administration](identity-administration.md) for operator tooling.

## Retained data and validation

Email, display name, password hash and session rows remain stored. The email
stays reserved. No account closure, withdrawal or queue cancellation occurs;
this transaction writes only identity users and sessions. Financial records
are outside this service's mutation path.

Run `DOCKER_CONTEXT=desktop-linux make check-all`. Handler and real
HTTPS/PostgreSQL tests cover owner isolation, strict empty bodies, repeated
requests (including two HTTPS DELETEs synchronized after authentication and
ownership checks, both returning empty `204` responses), blocked login/profile
updates, retained identity data, revocation
across re-enablement, unrelated sessions, secret-free logs, deferred commit
failure and observed database lock waits with cancellation. Existing login
concurrency tests cover rechecking a committed deactivation. End-to-end proof
that accepted financial operations survive deactivation remains T38, after
financial persistence and workers exist.
