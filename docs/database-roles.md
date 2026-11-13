# Database roles and service credentials

T17-A separates capability roles from deployment-specific LOGIN credentials and
from the owner that applies migrations. No passwords are stored in this repository.
Provisioning is explicit: migrations never create cluster-wide roles.

## Provisioning order

1. Create a dedicated database and migration owner through your cloud/database
   administrator. Run `make migrate` with the owner's `DATABASE_URL`.
2. Run the following using a protected libpq service configuration for the same
   database. The provisioning session must own the schemas/tables and have
   CREATEROLE, or be an administrator. When using a separate administrator,
   configure the migration owner's default function privileges separately too.

   ```sh
   PGSERVICE=hpts_provisioner psql -X -v ON_ERROR_STOP=1 -f scripts/database-roles.sql
   ```

3. Create a separate nonprivileged LOGIN for each process and grant exactly one
   matching capability role. For example, from the provisioning session:

   ```sql
   CREATE ROLE app_user LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE
       NOREPLICATION NOBYPASSRLS;
   GRANT hpts_identity TO app_user;
   ```

   Repeat with different login names for Account, Transaction, worker and future
   relay, using the table below. Provision authentication through your platform's
   secret manager or psql `\password`; never embed a password in shell/SQL history.
   Use verified TLS for remote database connections and restrict network access.
4. Supply each process its own `DATABASE_URL` from secret delivery. Keep owner and
   provisioning credentials out of service containers. No fallback to a migration
   credential is supported. Run deployment checks before routing traffic.

Capability names are reserved, cluster-wide NOLOGIN roles. Provisioning checks
unsafe attributes and inherited memberships instead of silently reusing elevated
roles. The script is transactional, serialized and repeatable; it removes PUBLIC
and capability table/column grants in the two schemas before applying the explicit
matrix. Direct login grants or other memberships are not managed by this script;
use dedicated logins with exactly one capability membership. Separate unrelated
applications into different databases/clusters and restrict CONNECT separately.

## Current permission matrix

| Capability | Used by | Allowed access |
| --- | --- | --- |
| `hpts_identity` | User | Read/insert users and sessions; update user display name/password hash/active flag and session revocation. No financial schema access. |
| `hpts_financial_http` | Account, Transaction (distinct logins) | Identity users: SELECT user_id, active only. Sessions: SELECT token_hash, user_id, issued_at, expires_at, revoked_at only. Read accounts, insert creation fields, update metadata and financial state for future shared transaction paths. |
| `hpts_worker` | Transaction worker | Read and lock accounts; update balance, head_sequence and retry_after. No metadata, acceptance-sequence or identity access. |
| `hpts_relay` | Future outbox relay | Financial schema USAGE only until T21 adds the outbox. No account or identity access. |

No capability owns objects or has DELETE, TRUNCATE, DDL, grant options or migration
history access. Account identity columns remain immutable through both grants and
schema guards. Financial HTTP has column grants, not table-wide identity SELECT.
Ownership filtering remains an application responsibility; these roles separate
services, not individual users (there is no account row-level security policy).

New tables get no automatic runtime grants. T21 must extend the matrix and tests
for operations, history, closures and outbox. T29 must use the relay capability and
shared pool guard. Role revocation/grants alone do not implement queue processing;
current worker tests prove account locking/state writes without identity access.
Actual balance/history/outcome processing and its role tests follow in T26/T30.

## Runtime enforcement

`postgres.OpenRuntimePool` checks every new connection. User requires the identity
capability, Account/Transaction require financial HTTP, and the worker scaffold
requires worker credentials before exiting with its scaffold message. Thus all
four processes now require a migrated, provisioned database even before financial
HTTP endpoints/worker loops are implemented. Relay startup must use this same
function once its executable exists.

The guard rejects superusers, BYPASSRLS, role/database creators, replication roles,
current database/schema/table owners, extra role memberships, schema creation
rights, role-switched sessions and selected forbidden cross-domain privileges.
Missing capabilities or an unavailable database fail closed with fixed diagnostics.
It checks new connections, not a continuous administrator audit: do not change
roles/grants underneath running applications; recycle pools after provisioning.
The migration CLI and operator CLI intentionally use separate privileged paths;
see [identity administration](identity-administration.md) for narrower operator
permissions. Operators are not application credentials.

## Validation

`DOCKER_CONTEXT=desktop-linux make check-all` uses a disposable PostgreSQL server
with test-only trust authentication. Role tests create actual non-superuser LOGIN
connections, not just SET ROLE as a superuser. They exercise identity registration/
login, financial HTTP bearer authentication, account creation/read, and a worker
account lock/write without identity access. Negative SQL tests cover credential
reads, identity writes, ownership changes, destructive/DDL operations and relay
access. Provisioning reruns remove accidental column/PUBLIC grants. Startup tests
reject superuser credentials and run compiled HTTP binaries with matching logins.

The test server must allow database and role creation/deletion; never use a shared
or production server. Role fixtures are sequential and remove their grants and
logins during cleanup. Existing fixtures without runtime roles remain isolated.

PostgreSQL references: [GRANT and additive role/column permissions](https://www.postgresql.org/docs/17/sql-grant.html),
[privilege inquiry functions](https://www.postgresql.org/docs/17/functions-info.html).
