# Profile and password updates

The User service implements these authenticated, owner-only endpoints:

- `PATCH /users/{userId}` accepts exactly `{"displayName":"New name"}` and returns the public user profile with status `200`. Names contain 1–100 Unicode characters, exclude NUL, and are preserved without trimming or normalization. Empty updates, nulls, duplicate/unknown fields and changes to email, identity, active state or credentials return `400 INVALID_REQUEST`.
- `PUT /users/{userId}/password` accepts exactly `currentPassword` and `newPassword`. Current password input contains 1–128 Unicode characters; an incorrect current password returns `403 INVALID_CURRENT_PASSWORD`. New passwords contain 12–128 Unicode characters, without trimming, normalization or character-category rules. Success returns an empty `204` and permanently revokes all existing sessions, including the caller's. Log in again to obtain a token.

Both routes use `src/authn` before decoding their bodies. Missing or invalid credentials return `401 UNAUTHORIZED`; another user's ID or a missing user returns `404 NOT_FOUND`. Only the validated identity is passed to the update service. Profile changes do not revoke sessions or change credentials. Email is immutable, so duplicate-email handling is not applicable to updates.

## Transactions and concurrency

`src/profile` updates display names with a single SQL statement. Password changes use a READ COMMITTED transaction and the same user-row lock as login. Current-password verification, server-side hashing, hash replacement and all-session revocation complete before commit. A failed transaction publishes no successful response and rolls back both changes. Concurrent password changes recheck the committed credential under the lock. Login cannot publish a session using stale credentials after the change commits.

Password changes share the two active-request admission slots used by registration and login, bounding Argon2 memory per process. Cancellation is checked before and after hashing; database calls use the request context. Updates reject users deactivated since the entry check. Authentication remains an entry check, not continuous authorization. Queued financial operations are unaffected.

A lost successful password-change response leaves the new password active and the old sessions revoked; the client must log in with the new password. No replacement token is returned by the password endpoint. Storage failures return fixed errors, and passwords, hashes and tokens must never be logged.

## Validation

Run `DOCKER_CONTEXT=desktop-linux make check-all`. Unit tests cover routing, ownership, input validation and error mapping. Real HTTPS/PostgreSQL tests check persisted profile values, immutable fields, credential isolation, session revocation, login with the new password and secret-free logs. Database tests inject a deferred commit failure and run concurrent password changes to verify rollback and serialization.

User deactivation, logout and operator administration remain separate work.
