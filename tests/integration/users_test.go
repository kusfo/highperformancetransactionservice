//go:build integration

package integration_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/kusfo/highperformancetransactionservice/src/postgres"
	"github.com/kusfo/highperformancetransactionservice/src/users"
	"github.com/kusfo/highperformancetransactionservice/tests/testdb"
)

func userFixture(t *testing.T) (*pgx.Conn, *users.Repository, context.Context) {
	t.Helper()
	conn := testdb.New(t)
	ctx := testContext(t)
	if err := postgres.Migrate(ctx, conn, os.DirFS("../../migrations")); err != nil {
		t.Fatal(err)
	}
	if err := postgres.Migrate(ctx, conn, os.DirFS("../../migrations")); err != nil {
		t.Fatal(err)
	}
	return conn, users.NewRepository(conn), ctx
}
func TestUserPersistenceAndDeactivation(t *testing.T) {
	t.Parallel()
	_, repo, ctx := userFixture(t)
	user, err := repo.Create(ctx, " Person@Example.COM ", "fixture-hash", nil)
	if err != nil {
		t.Fatal(err)
	}
	if user.Email != "person@example.com" || user.DisplayName != "" || !user.Active || len(user.UserID) != 36 {
		t.Fatalf("unexpected profile: %+v", user)
	}
	got, err := repo.ByID(ctx, user.UserID)
	if err != nil || got != user {
		t.Fatalf("lookup mismatch: %+v %v", got, err)
	}
	for _, active := range []bool{false, false, true, false} {
		if err := repo.SetActive(ctx, user.UserID, active); err != nil {
			t.Fatal(err)
		}
		credential, err := repo.CredentialsByEmail(ctx, "PERSON@EXAMPLE.COM")
		if err != nil || credential.User.Active != active || credential.PasswordHash() != "fixture-hash" {
			t.Fatal("state or hash not retained")
		}
		if _, err := repo.Create(ctx, "person@example.com", "other-hash", nil); !errors.Is(err, users.ErrEmailAlreadyUsed) {
			t.Fatalf("email reservation lost: %v", err)
		}
	}
	if _, err := repo.ByID(ctx, "missing"); !errors.Is(err, users.ErrNotFound) {
		t.Fatal(err)
	}
	if err := repo.SetActive(ctx, "missing", true); !errors.Is(err, users.ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := repo.CredentialsByEmail(ctx, "missing@example.com"); !errors.Is(err, users.ErrNotFound) {
		t.Fatal(err)
	}
}
func TestConcurrentEmailRegistration(t *testing.T) {
	t.Parallel()
	conn, repo, ctx := userFixture(t)
	const contenders = 8
	start := make(chan struct{})
	results := make(chan error, contenders)
	for i := 0; i < contenders; i++ {
		email := []string{"Concurrent@Example.com", " concurrent@example.COM ", "CONCURRENT@EXAMPLE.COM"}[i%3]
		c, err := pgx.ConnectConfig(ctx, conn.Config().Copy())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close(context.Background()) })
		go func() {
			<-start
			_, err := users.NewRepository(c).Create(ctx, email, "fixture-hash", nil)
			results <- err
		}()
	}
	close(start)
	success := 0
	for i := 0; i < contenders; i++ {
		err := <-results
		if err == nil {
			success++
		} else if !errors.Is(err, users.ErrEmailAlreadyUsed) {
			t.Fatalf("unexpected failure: %v", err)
		}
	}
	if success != 1 {
		t.Fatalf("successful registrations: %d", success)
	}
	credential, err := repo.CredentialsByEmail(ctx, "concurrent@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetActive(ctx, credential.User.UserID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Create(ctx, "CONCURRENT@example.com", "fixture-hash", nil); !errors.Is(err, users.ErrEmailAlreadyUsed) {
		t.Fatal("inactive email reused")
	}
}
func TestUserValidationAndDatabaseConstraints(t *testing.T) {
	t.Parallel()
	conn, repo, ctx := userFixture(t)
	empty := ""
	long := ""
	for i := 0; i < 101; i++ {
		long += "界"
	}
	for _, name := range []*string{&empty, &long} {
		if _, err := repo.Create(ctx, "person@example.com", "fixture-hash", name); !errors.Is(err, users.ErrInvalidInput) {
			t.Fatal(err)
		}
	}
	if _, err := repo.Create(ctx, "person@example.com", "", nil); !errors.Is(err, users.ErrInvalidInput) {
		t.Fatal(err)
	}
	name := "界"
	if _, err := repo.Create(ctx, "person@example.com", "fixture-hash", &name); err != nil {
		t.Fatal(err)
	}
	for _, email := range []string{"UPPER@example.com", " space@example.com", "é@example.com"} {
		_, err := conn.Exec(ctx, `INSERT INTO identity.users(user_id,email,password_hash) VALUES ('550e8400-e29b-41d4-a716-446655440000',$1,'fixture-hash')`, email)
		assertConstraint(t, err, "23514", "users_email_canonical")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := repo.ByID(cancelled, "missing"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

func assertConstraint(t *testing.T, err error, state, constraint string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != state || pgErr.ConstraintName != constraint {
		t.Fatalf("expected %s/%s, got %v", state, constraint, err)
	}
}
func TestUserPersistenceBoundariesAndOutage(t *testing.T) {
	conn, repo, ctx := userFixture(t)
	name := strings.Repeat("界", 100)
	user, err := repo.Create(ctx, "boundary@example.com", "secret-stored-hash", &name)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := repo.ByID(ctx, user.UserID); err != nil || got.DisplayName != name {
		t.Fatal("100-character name not retained")
	}
	_, err = conn.Exec(ctx, `INSERT INTO identity.users(user_id,email,password_hash) VALUES ('550e8400-e29b-41d4-a716-446655440000','boundary@example.com','hash')`)
	assertConstraint(t, err, "23505", "users_email_unique")
	_, err = conn.Exec(ctx, `INSERT INTO identity.users(user_id,email,password_hash) VALUES ('550e8400-e29b-41d4-a716-446655440000','other@example.com','')`)
	assertConstraint(t, err, "23514", "users_password_hash_check")
	_, err = conn.Exec(ctx, `INSERT INTO identity.users(user_id,email,password_hash,display_name) VALUES ('550e8400-e29b-41d4-a716-446655440000','other@example.com','hash',$1)`, strings.Repeat("界", 101))
	assertConstraint(t, err, "23514", "users_display_name_check")
	if err := conn.Close(ctx); err != nil {
		t.Fatal(err)
	}
	_, err = repo.Create(ctx, "outage@example.com", "secret-stored-hash", nil)
	if !errors.Is(err, users.ErrStorage) || strings.Contains(err.Error(), "secret-stored-hash") || strings.Contains(err.Error(), "outage@example.com") {
		t.Fatalf("unsafe outage result: %v", err)
	}
	if _, err := repo.CredentialsByEmail(ctx, "boundary@example.com"); !errors.Is(err, users.ErrStorage) {
		t.Fatalf("credential outage: %v", err)
	}
	if err := repo.SetActive(ctx, user.UserID, false); !errors.Is(err, users.ErrStorage) {
		t.Fatalf("state outage: %v", err)
	}
}
func TestConcurrentRegistrationAgainstInactiveEmail(t *testing.T) {
	conn, repo, ctx := userFixture(t)
	user, err := repo.Create(ctx, "reserved@example.com", "fixture-hash", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetActive(ctx, user.UserID, false); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 6)
	for i := 0; i < 6; i++ {
		c, err := pgx.ConnectConfig(ctx, conn.Config().Copy())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close(context.Background()) })
		email := []string{"RESERVED@example.com", " reserved@EXAMPLE.COM "}[i%2]
		go func() {
			<-start
			_, err := users.NewRepository(c).Create(ctx, email, "fixture-hash", nil)
			results <- err
		}()
	}
	close(start)
	for i := 0; i < 6; i++ {
		if err := <-results; !errors.Is(err, users.ErrEmailAlreadyUsed) {
			t.Fatalf("inactive email was not reserved: %v", err)
		}
	}
	var count int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM identity.users").Scan(&count); err != nil || count != 1 {
		t.Fatalf("unexpected rows: %d %v", count, err)
	}
}
