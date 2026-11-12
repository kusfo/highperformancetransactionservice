//go:build integration

package integration_test

import (
	"context"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/kusfo/highperformancetransactionservice/src/identityadmin"
	"github.com/kusfo/highperformancetransactionservice/src/login"
	"github.com/kusfo/highperformancetransactionservice/src/passwords"
	"github.com/kusfo/highperformancetransactionservice/src/profile"
	"github.com/kusfo/highperformancetransactionservice/src/sessions"
)

func TestIdentityAdminCLI(t *testing.T) {
	binary := buildCLI(t, "identity-admin")
	conn, repo, ctx := userFixture(t)
	hash, err := passwords.Hash("operator-test-password")
	if err != nil {
		t.Fatal(err)
	}
	owner, err := repo.Create(ctx, "operator@example.com", hash, nil)
	if err != nil {
		t.Fatal(err)
	}
	other, err := repo.Create(ctx, "other@example.com", hash, nil)
	if err != nil {
		t.Fatal(err)
	}
	issuer := sessions.NewRepository(conn, nil)
	first, err := issuer.Issue(ctx, owner.UserID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := issuer.Issue(ctx, owner.UserID)
	if err != nil {
		t.Fatal(err)
	}
	unrelated, err := issuer.Issue(ctx, other.UserID)
	if err != nil {
		t.Fatal(err)
	}
	dbURL, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	dbURL.Path = "/" + conn.Config().Database
	dbURL.RawPath = ""
	query := dbURL.Query()
	query.Del("dbname")
	dbURL.RawQuery = query.Encode()
	secrets := []string{hash, "operator-test-password", first.AccessToken(), second.AccessToken(), unrelated.AccessToken(), "private-driver-error"}
	invoke := func(database, input string, wantOK bool, args ...string) {
		t.Helper()
		runCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(runCtx, binary, args...)
		cmd.Env = cliEnv(map[string]string{"DATABASE_URL": database})
		cmd.Stdin = strings.NewReader(input)
		output, err := cmd.CombinedOutput()
		if (err == nil) != wantOK {
			t.Fatalf("unexpected CLI success=%t", err == nil)
		}
		for _, secret := range secrets {
			if strings.Contains(string(output), secret) {
				t.Fatal("CLI disclosed secret")
			}
		}
		expected := "identity administration completed\n"
		if !wantOK {
			expected = "identity administration failed; check command, input, user and database configuration\n"
		}
		if string(output) != expected {
			t.Fatal("unexpected CLI output")
		}
	}
	run := func(input string, ok bool, args ...string) { t.Helper(); invoke(dbURL.String(), input, ok, args...) }
	rejected := func(token string) {
		t.Helper()
		if _, err := issuer.Authenticate(ctx, token); !errors.Is(err, sessions.ErrInvalidSession) {
			t.Fatal("revoked session accepted")
		}
	}
	accepted := func(token string) {
		t.Helper()
		if _, err := issuer.Authenticate(ctx, token); err != nil {
			t.Fatal("valid session rejected")
		}
	}
	invoke("", "", false, "enable-user", owner.UserID)
	invoke("postgres://user:private-driver-error@invalid.invalid/db?bad=%", "", false, "enable-user", owner.UserID)
	run("", false, "revoke-session", first.AccessToken())
	run("invalid-token", false, "revoke-session")
	run(first.AccessToken()+"\n", true, "revoke-session")
	rejected(first.AccessToken())
	accepted(second.AccessToken())
	accepted(unrelated.AccessToken())
	var revokedAt time.Time
	if err = conn.QueryRow(ctx, `SELECT revoked_at FROM identity.sessions WHERE user_id=$1 AND revoked_at IS NOT NULL`, owner.UserID).Scan(&revokedAt); err != nil {
		t.Fatal(err)
	}
	run(first.AccessToken(), true, "revoke-session")
	var repeatedAt time.Time
	if err = conn.QueryRow(ctx, `SELECT revoked_at FROM identity.sessions WHERE user_id=$1 AND revoked_at IS NOT NULL`, owner.UserID).Scan(&repeatedAt); err != nil || !repeatedAt.Equal(revokedAt) {
		t.Fatal("retry changed revocation")
	}
	run(strings.Repeat("A", 43), true, "revoke-session") // unknown canonical token: harmless
	run("", true, "revoke-user-sessions", owner.UserID)
	rejected(second.AccessToken())
	accepted(unrelated.AccessToken())
	fresh, err := login.NewService(conn, nil).Authenticate(ctx, owner.Email, "operator-test-password")
	if err != nil {
		t.Fatal(err)
	}
	secrets = append(secrets, fresh.AccessToken())
	run("", true, "enable-user", owner.UserID) // active no-op preserves new sessions
	accepted(fresh.AccessToken())
	if err = profile.NewService(conn).Deactivate(ctx, owner.UserID); err != nil {
		t.Fatal(err)
	}
	run("", true, "enable-user", owner.UserID)
	rejected(first.AccessToken())
	rejected(second.AccessToken())
	rejected(fresh.AccessToken())
	enabled, err := login.NewService(conn, nil).Authenticate(ctx, owner.Email, "operator-test-password")
	if err != nil {
		t.Fatal("re-enabled login failed")
	}
	secrets = append(secrets, enabled.AccessToken())
	accepted(enabled.AccessToken())
	saved, err := repo.CredentialsByEmail(ctx, owner.Email)
	if err != nil || saved.PasswordHash() != hash {
		t.Fatal("credentials not preserved")
	}
	if err = repo.SetActive(ctx, owner.UserID, false); err != nil {
		t.Fatal(err)
	} // legacy/manual inactive state
	// Failure on commit must roll back both revocation and re-enablement.
	_, err = conn.Exec(ctx, `CREATE FUNCTION identity.fail_admin() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'private-driver-error'; END $$;
 CREATE CONSTRAINT TRIGGER fail_admin AFTER UPDATE ON identity.sessions DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION identity.fail_admin()`)
	if err != nil {
		t.Fatal(err)
	}
	run("", false, "enable-user", owner.UserID)
	state, err := repo.ByID(ctx, owner.UserID)
	if err != nil || state.Active {
		t.Fatal("failed commit enabled user")
	}
	var unrevoked int
	if err = conn.QueryRow(ctx, `SELECT count(*) FROM identity.sessions WHERE user_id=$1 AND revoked_at IS NULL`, owner.UserID).Scan(&unrevoked); err != nil || unrevoked != 1 {
		t.Fatal("failed commit revoked session")
	}
	if _, err = conn.Exec(ctx, `DROP TRIGGER fail_admin ON identity.sessions`); err != nil {
		t.Fatal(err)
	}
	run("", true, "enable-user", owner.UserID)
	rejected(enabled.AccessToken())
	accepted(unrelated.AccessToken())
	run("", false, "enable-user", "00000000-0000-0000-0000-000000000000")
	run("", false, "revoke-user-sessions", "00000000-0000-0000-0000-000000000000")
	run("", false, "enable-user", "private-driver-error")
}

func TestOperatorRevocationWaitsForIssuance(t *testing.T) {
	for _, cancelWaiting := range []bool{false, true} {
		t.Run(map[bool]string{false: "revoke committed issuance", true: "cancel waiter"}[cancelWaiting], func(t *testing.T) {
			conn, repo, ctx := userFixture(t)
			owner, err := repo.Create(ctx, "race@example.com", "stored-hash", nil)
			if err != nil {
				t.Fatal(err)
			}
			worker, err := pgx.ConnectConfig(ctx, conn.Config().Copy())
			if err != nil {
				t.Fatal(err)
			}
			defer worker.Close(context.Background())
			observer, err := pgx.ConnectConfig(ctx, conn.Config().Copy())
			if err != nil {
				t.Fatal(err)
			}
			defer observer.Close(context.Background())
			tx, err := conn.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(context.Background())
			if _, err = tx.Exec(ctx, `SELECT user_id FROM identity.users WHERE user_id=$1 FOR UPDATE`, owner.UserID); err != nil {
				t.Fatal(err)
			}
			token, err := sessions.NewRepository(tx, nil).Issue(ctx, owner.UserID)
			if err != nil {
				t.Fatal(err)
			}
			waitCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
			defer cancel()
			result := make(chan error, 1)
			pid := worker.PgConn().PID()
			go func() { result <- identityadmin.NewService(worker).RevokeUserSessions(waitCtx, owner.UserID) }()
			deadline := time.Now().Add(5 * time.Second)
			for {
				var blocked bool
				err = observer.QueryRow(ctx, `SELECT cardinality(pg_blocking_pids(pid))>0 AND query LIKE 'SELECT active%' FROM pg_stat_activity WHERE pid=$1`, pid).Scan(&blocked)
				if err != nil {
					t.Fatal(err)
				}
				if blocked {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("operator revocation did not wait on user lock")
				}
				time.Sleep(10 * time.Millisecond)
			}
			if cancelWaiting {
				cancel()
				select {
				case err = <-result:
					if !errors.Is(err, context.Canceled) {
						t.Fatal("wrong cancellation error")
					}
				case <-time.After(5 * time.Second):
					t.Fatal("waiter stuck")
				}
			}
			if err = tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if !cancelWaiting {
				select {
				case err = <-result:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("operator revocation stuck")
				}
			}
			got, err := repo.ByID(ctx, owner.UserID)
			if err != nil || !got.Active {
				t.Fatal("incorrect active state")
			}
			_, err = sessions.NewRepository(conn, nil).Authenticate(ctx, token.AccessToken())
			if cancelWaiting && err != nil || !cancelWaiting && !errors.Is(err, sessions.ErrInvalidSession) {
				t.Fatal("incorrect session state")
			}
		})
	}
}
