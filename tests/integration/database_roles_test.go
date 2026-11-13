//go:build integration

package integration_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/kusfo/highperformancetransactionservice/src/financial"
	"github.com/kusfo/highperformancetransactionservice/src/login"
	"github.com/kusfo/highperformancetransactionservice/src/passwords"
	"github.com/kusfo/highperformancetransactionservice/src/postgres"
	"github.com/kusfo/highperformancetransactionservice/src/sessions"
	"github.com/kusfo/highperformancetransactionservice/src/users"
)

// Called only by sequential tests on the disposable PostgreSQL server. Roles
// are cluster-wide; cleanup revokes this database's grants before dropping them.
func provisionRuntimeRoles(t *testing.T, admin *pgx.Conn) map[string]string {
	t.Helper()
	script, err := os.ReadFile("../../scripts/database-roles.sql")
	if err != nil {
		t.Fatal(err)
	}
	ctx := testContext(t)
	roles := []string{postgres.IdentityRole, postgres.FinancialHTTPRole, postgres.WorkerRole, postgres.RelayRole}
	logins := map[string]string{}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, name := range logins {
			if _, err := admin.Exec(cleanup, "DROP OWNED BY "+pgx.Identifier{name}.Sanitize()); err != nil {
				t.Error("login grant cleanup failed")
			}
			if _, err := admin.Exec(cleanup, "DROP ROLE "+pgx.Identifier{name}.Sanitize()); err != nil {
				t.Error("login cleanup failed")
			}
		}
		for _, name := range roles {
			if _, err := admin.Exec(cleanup, "DROP OWNED BY "+pgx.Identifier{name}.Sanitize()); err != nil {
				t.Error("capability grant cleanup failed")
			}
			if _, err := admin.Exec(cleanup, "DROP ROLE IF EXISTS "+pgx.Identifier{name}.Sanitize()); err != nil {
				t.Error("capability cleanup failed")
			}
		}
	})
	for range 2 {
		if _, err = admin.Exec(ctx, string(script)); err != nil {
			t.Fatal(err)
		}
	}
	for _, role := range roles {
		name := role + "_test_login"
		if _, err = admin.Exec(ctx, "CREATE ROLE "+pgx.Identifier{name}.Sanitize()+" LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS"); err != nil {
			t.Fatal(err)
		}
		logins[role] = name
		if _, err = admin.Exec(ctx, "GRANT "+pgx.Identifier{role}.Sanitize()+" TO "+pgx.Identifier{name}.Sanitize()); err != nil {
			t.Fatal(err)
		}
	}
	return logins
}
func runtimeRoleConnection(t *testing.T, admin *pgx.Conn, name string) *pgx.Conn {
	t.Helper()
	cfg := admin.Config().Copy()
	cfg.User = name
	cfg.Password = ""
	c, err := pgx.ConnectConfig(testContext(t), cfg)
	if err != nil {
		t.Fatal("runtime login failed")
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	return c
}

func TestDatabaseRoleBoundaries(t *testing.T) {
	admin, _, ctx := userFixture(t)
	logins := provisionRuntimeRoles(t, admin)
	identity := runtimeRoleConnection(t, admin, logins[postgres.IdentityRole])
	http := runtimeRoleConnection(t, admin, logins[postgres.FinancialHTTPRole])
	worker := runtimeRoleConnection(t, admin, logins[postgres.WorkerRole])
	relay := runtimeRoleConnection(t, admin, logins[postgres.RelayRole])
	for role, c := range map[string]*pgx.Conn{postgres.IdentityRole: identity, postgres.FinancialHTTPRole: http, postgres.WorkerRole: worker, postgres.RelayRole: relay} {
		if err := postgres.CheckRuntimeRole(ctx, c, role); err != nil {
			t.Fatalf("runtime role rejected: %s", role)
		}
	}
	if err := postgres.CheckRuntimeRole(ctx, admin, postgres.IdentityRole); !errors.Is(err, postgres.ErrRuntimeRole) {
		t.Fatal("superuser accepted")
	}
	hash, err := passwords.Hash("role-test-password")
	if err != nil {
		t.Fatal(err)
	}
	u, err := users.NewRepository(identity).Create(ctx, "roles@example.com", hash, nil)
	if err != nil {
		t.Fatal(err)
	}
	token, err := login.NewService(identity, nil).Authenticate(ctx, u.Email, "role-test-password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = sessions.NewRepository(http, nil).Authenticate(ctx, token.AccessToken()); err != nil {
		t.Fatal("financial HTTP auth denied")
	}
	account, err := financial.NewAccounts(http).Create(ctx, u.UserID, financial.Metadata{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = financial.NewAccounts(http).ByNumber(ctx, u.UserID, account.AccountNumber); err != nil {
		t.Fatal(err)
	}
	// Prove the account lock/state write needed by processing has no identity access.
	tx, err := worker.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SELECT account_id FROM financial.accounts WHERE account_id=$1 FOR UPDATE`, account.AccountID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE financial.accounts SET balance=5 WHERE account_id=$1`, account.AccountID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := financial.NewAccounts(http).ByNumber(ctx, u.UserID, account.AccountNumber)
	if err != nil || got.Balance != 5 {
		t.Fatal("worker change not durable")
	}
	denied := func(c *pgx.Conn, sql string) {
		t.Helper()
		_, err := c.Exec(ctx, sql)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
			t.Fatalf("expected permission denial: %s: %v", sql, err)
		}
	}
	for _, sql := range []string{
		`SELECT password_hash FROM identity.users`, `SELECT email FROM identity.users`, `SELECT * FROM identity.users`,
		`UPDATE identity.users SET active=false`, `UPDATE identity.users SET password_hash='forbidden'`,
		`INSERT INTO identity.users(user_id,email,password_hash) VALUES(gen_random_uuid(),'forbidden@example.com','forbidden')`,
		`UPDATE identity.sessions SET revoked_at=now()`, `DELETE FROM identity.sessions`,
		`TRUNCATE identity.users CASCADE`, `ALTER TABLE financial.accounts ADD COLUMN forbidden integer`,
		`UPDATE financial.accounts SET owner_id=gen_random_uuid()`, `DELETE FROM financial.accounts`, `TRUNCATE financial.accounts`,
		`CREATE TABLE public.forbidden(id integer)`, `SELECT * FROM public.schema_migrations`,
	} {
		denied(http, sql)
	}
	for _, c := range []*pgx.Conn{worker, relay} {
		denied(c, `SELECT user_id FROM identity.users`)
		denied(c, `SELECT token_hash FROM identity.sessions`)
		denied(c, `UPDATE identity.users SET active=false`)
	}
	denied(worker, `UPDATE financial.accounts SET display_name='forbidden'`)
	denied(worker, `UPDATE financial.accounts SET next_sequence=next_sequence+1`)
	denied(worker, `TRUNCATE financial.accounts`)
	denied(relay, `SELECT * FROM financial.accounts`)
	denied(relay, `UPDATE financial.accounts SET balance=0`)
	denied(identity, `SELECT * FROM financial.accounts`)
	denied(identity, `DELETE FROM identity.users`)
	// Rerunning provisioning removes table and column grants accidentally added
	// to a capability role, including PUBLIC grants inherited by every login.
	if _, err = admin.Exec(ctx, `GRANT SELECT(password_hash) ON identity.users TO hpts_financial_http; GRANT SELECT(email) ON identity.users TO PUBLIC`); err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile("../../scripts/database-roles.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = admin.Exec(ctx, string(script)); err != nil {
		t.Fatal(err)
	}
	denied(http, `SELECT password_hash FROM identity.users`)
	denied(http, `SELECT email FROM identity.users`)
	if err = postgres.CheckRuntimeRole(ctx, http, postgres.FinancialHTTPRole); err != nil {
		t.Fatal(err)
	}
	// Direct broad grants are not masked by belonging to the right group.
	if _, err = admin.Exec(ctx, "GRANT SELECT(password_hash) ON identity.users TO "+pgx.Identifier{logins[postgres.FinancialHTTPRole]}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	if err = postgres.CheckRuntimeRole(ctx, http, postgres.FinancialHTTPRole); err == nil {
		t.Fatal("credential grant accepted")
	}
	if _, err = admin.Exec(ctx, "REVOKE SELECT(password_hash) ON identity.users FROM "+pgx.Identifier{logins[postgres.FinancialHTTPRole]}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	// An otherwise valid role that owns a table must not serve as runtime identity.
	if _, err = admin.Exec(ctx, "CREATE TABLE public.owner_probe(id integer); ALTER TABLE public.owner_probe OWNER TO "+pgx.Identifier{logins[postgres.IdentityRole]}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	if err = postgres.CheckRuntimeRole(ctx, identity, postgres.IdentityRole); err == nil {
		t.Fatal("object owner accepted")
	}
	if _, err = admin.Exec(ctx, "DROP TABLE public.owner_probe"); err != nil {
		t.Fatal(err)
	}
	// Role switching cannot disguise the migration session identity.
	if _, err = admin.Exec(ctx, "SET ROLE hpts_financial_http"); err != nil {
		t.Fatal(err)
	}
	err = postgres.CheckRuntimeRole(ctx, admin, postgres.FinancialHTTPRole)
	if _, resetErr := admin.Exec(ctx, "RESET ROLE"); resetErr != nil {
		t.Fatal(resetErr)
	}
	if err == nil {
		t.Fatal("SET ROLE disguise accepted")
	}
}
