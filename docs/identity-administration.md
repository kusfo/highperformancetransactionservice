# Manual identity administration

Build with `make bin/identity-admin`. This is an operator CLI with direct
PostgreSQL access, not an HTTP endpoint. It requires `DATABASE_URL` supplied by
an authorized operator's secret-management environment. Never distribute its
database credentials to application users or the frontend. Run against the
intended database after applying migrations; the CLI never runs migrations.

## Commands

```sh
./bin/identity-admin --help
./bin/identity-admin revoke-session < /secure/path/session-token
./bin/identity-admin revoke-user-sessions 01234567-89ab-cdef-0123-456789abcdef
./bin/identity-admin enable-user 01234567-89ab-cdef-0123-456789abcdef
```

Use the user's canonical lowercase UUID. Single-session revocation reads the
43-character access token from standard input, optionally followed by LF or
CRLF. Use an existing protected file or a trusted secret-manager pipe; do not
put tokens in arguments, shell history, environment variables or command
substitutions. Do not type them into an echoing terminal or enable shell tracing.
The database stores only token digests, so an original token cannot be recovered.
If the original token is unavailable, use `revoke-user-sessions`.

The CLI prints only a fixed success or failure message. Exit status is 0 on
success, 1 on failure. Invalid arguments, unknown users, unavailable databases
and transaction failures have the same redacted diagnostic. Database operations
have a 30-second deadline. Standard-input collection precedes that deadline;
provide a finite stream ending at EOF. No passwords, token digests or raw tokens
are printed, including in driver-error cases.

## Effects and retries

- `revoke-session` permanently revokes that token, including an expired session.
  Repeated revocation preserves the original timestamp. A well-formed unknown
  token is a successful no-op; other sessions remain valid.
- `revoke-user-sessions` locks the user row using login's lock order, then revokes
  all existing sessions in one transaction. It works for active or inactive
  users. Login committed before the lock is included; later logins may succeed.
  Repeating this command also revokes sessions created since the previous call.
- `enable-user` locks the user row. For an inactive user it atomically revokes any
  remaining unrevoked sessions and marks the user active. They can then log in
  using their unchanged credentials. Old sessions cannot revive. For an already
  active user this is a no-op, preserving current sessions. It does not undo a
  later deactivation automatically; operators must check intent before retrying
  after an uncertain result or intervening lifecycle action.

All commands retain identity and session rows, email reservation and credentials.
They do not modify financial records or cancel accepted operations. No account
reactivation, closure or withdrawal is implied. New HTTP requests still undergo
normal authentication. To prevent new login, use the existing user-deactivation
flow rather than repeatedly revoking sessions. No operator deactivation route,
superuser interface or password recovery is introduced here.

## Access and validation

Restrict execution and database network access to trusted operators. The runtime
connection needs schema usage and the relevant identity columns: user ID/active
reads and active updates (including row locking), session token hash/user ID/
revocation reads and revocation updates. It does not need password-hash reads,
financial-table privileges, DDL, or DELETE rights. Use a dedicated operator role
and your deployment's secret delivery; do not reuse the migration owner in a
production procedure. Operator attribution belongs in the existing infrastructure
audit system, without command input or credential logging.

`make check-all` builds this CLI and runs its input tests and real PostgreSQL
subprocess tests. They verify durable single/all-session revocation, no-op and
missing-user outcomes, re-enabled login, old-token rejection, unaffected other
users, rollback on deferred commit failure and secret-free success/error output.
Observed lock-wait tests verify revocation after concurrent issuance and safe
cancellation. Financial queued-work survival is covered by future T38.
