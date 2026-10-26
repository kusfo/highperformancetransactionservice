# T02 User and Session Contract Review

Status: T02 behavior confirmed. `SPEC.md` remains authoritative. Runtime behavior will be implemented in subsequent tasks.

## Confirmed Requirements

Unique emails remain reserved for inactive users. Registration assigns UUID user IDs. HTTPS-only credentials, server-salted password hashing, no credential/token logging, and bearer authentication are required. Tokens last ten minutes and can be manually revoked. Deactivation retains user/accounts/history; manual re-enablement has no superuser API. Accepted work survives expiry, revocation and deactivation. No recovery or verification endpoints.

## Confirmed Contract Rules

- **Confirmed:** trim surrounding ASCII whitespace and lowercase ASCII email addresses before registration/login lookup. Reject internationalized email for the initial version. Email remains immutable.
- **Confirmed:** displayName is optional at registration, defaulting to an empty string when omitted. Supplied names and owner-only updates contain 1–100 Unicode characters excluding NUL, which PostgreSQL text cannot store. Names need not be unique and do not affect login; user and account names are separate. Reject unrecognized fields.
- Change passwords using authenticated PUT /users/{userId}/password and the current password. New passwords are 12–128 Unicode characters; never trim/normalize them or require character categories.
- **Confirmed:** use opaque random tokens backed by stored token hashes and revocation state; exact token generation/hashing choices belong to implementation tasks. Never store raw tokens.
- **Confirmed:** no refresh endpoint: log in again after expiry. DELETE /sessions/current revokes the current session. Password changes and deactivation revoke all existing sessions permanently; re-enablement cannot revive them.
- Revoked, expired or inactive-user credentials fail entry-point checks, including retries; queued operations remain independent of session state.

## Manual Administration Contract

T16 will provide operator-only procedures for revoking a session or re-enabling a user without exposing credentials. Revocation is durable and affects subsequent requests; it does not undo accepted work. No superuser routes are introduced.

## Contract Artifact and Validation

`api/user-session.openapi.json` documents request/response schemas and examples. All example credentials are fictitious. Validate the OpenAPI document, embedded request/response examples, and negative boundary cases with the command below. Email canonicalization, cross-field expiry consistency, authentication state changes, and ownership need behavioral tests in implementation tasks; schema validation alone cannot prove them.

Run reproducible checks in a disposable Python environment: `python -m pip install -r tests/contracts/requirements.txt`, then `python tests/contracts/validate_user_session.py`.

Email schema validation distinguishes raw, normalizable ASCII request input from canonical lowercase response output. `tests/contracts/email_cases.json` is shared with Go repository tests to cover Unicode rejection, whitespace/case handling and normalized-length boundaries.
