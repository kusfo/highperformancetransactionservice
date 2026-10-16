//go:build integration

package integration_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
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
		c, err := pgx.ConnectConfig(ctx, conn.Config().Copy())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close(context.Background()) })
		go func() {
			<-start
			_, err := users.NewRepository(c).Create(ctx, "Concurrent@Example.com", "fixture-hash", nil)
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
		if _, err := conn.Exec(ctx, `INSERT INTO identity.users(user_id,email,password_hash) VALUES ('550e8400-e29b-41d4-a716-446655440000',$1,'fixture-hash')`, email); err == nil {
			t.Fatal("database accepted noncanonical email")
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := repo.ByID(cancelled, "missing"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}
