//go:build integration

package integration_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kusfo/highperformancetransactionservice/src/httpapi"
	"github.com/kusfo/highperformancetransactionservice/src/login"
	"github.com/kusfo/highperformancetransactionservice/src/passwords"
	"github.com/kusfo/highperformancetransactionservice/src/profile"
	"github.com/kusfo/highperformancetransactionservice/src/sessions"
	"github.com/kusfo/highperformancetransactionservice/src/userapi"
	"github.com/kusfo/highperformancetransactionservice/src/users"
)

// admittedDeactivationBarrier pauses only the first two deactivations, after
// the real middleware has authenticated and the handler has checked ownership.
// Embedding the real service leaves persistence and other routes unchanged.
type admittedDeactivationBarrier struct {
	*profile.Service
	calls   atomic.Int32
	arrived chan string
	release chan struct{}
}

func (b *admittedDeactivationBarrier) Deactivate(ctx context.Context, id string) error {
	if b.calls.Add(1) <= 2 {
		b.arrived <- id
		select {
		case <-b.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return b.Service.Deactivate(ctx, id)
}

func TestDeactivationHTTPS(t *testing.T) {
	conn, repo, ctx := userFixture(t)
	hash, err := passwords.Hash("original-password")
	if err != nil {
		t.Fatal(err)
	}
	owner, err := repo.Create(ctx, "profile@example.com", hash, nil)
	if err != nil {
		t.Fatal(err)
	}
	other, err := repo.Create(ctx, "other@example.com", hash, nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.Database = conn.Config().Database
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	issuer := sessions.NewRepository(pool, nil)
	token, err := issuer.Issue(ctx, owner.UserID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := issuer.Issue(ctx, owner.UserID)
	if err != nil {
		t.Fatal(err)
	}
	otherToken, err := issuer.Issue(ctx, other.UserID)
	if err != nil {
		t.Fatal(err)
	}
	cert, key, client := serviceCertificate(t)
	client.Timeout = 10 * time.Second
	httpCfg := httpapi.DefaultConfig()
	httpCfg.Addr = "127.0.0.1:0"
	httpCfg.TLSCertFile = cert
	httpCfg.TLSKeyFile = key
	barrier := &admittedDeactivationBarrier{
		Service: profile.NewService(pool),
		arrived: make(chan string, 2),
		release: make(chan struct{}),
	}
	release := sync.OnceFunc(func() { close(barrier.release) })
	logs := &safeLog{}
	runtime, err := httpapi.New(httpCfg, userapi.NewHandlerWithLifecycle(users.NewRepository(pool), login.NewService(pool, nil), issuer, barrier), logs)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", httpCfg.Addr)
	if err != nil {
		t.Fatal(err)
	}
	serverCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runtime.Serve(serverCtx, listener) }()
	stop := sync.OnceFunc(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(12 * time.Second):
			t.Error("cleanup timeout")
		}
	})
	t.Cleanup(stop)
	t.Cleanup(release)
	request := func(method, path, body, access string, want int) string {
		t.Helper()
		r, err := http.NewRequest(method, "https://"+listener.Addr().String()+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Content-Type", "application/json")
		if access != "" {
			r.Header.Set("Authorization", "Bearer "+access)
		}
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != want {
			t.Fatalf("%s status %d want %d", method, response.StatusCode, want)
		}
		for _, secret := range []string{hash, "original-password", "replacement-password", token.AccessToken()} {
			if strings.Contains(string(data), secret) {
				t.Fatal("secret in response")
			}
		}
		return string(data)
	}
	path := "/users/" + owner.UserID
	request("DELETE", path, "", "", 401)
	request("DELETE", path, "", otherToken.AccessToken(), 404)
	request("DELETE", path, "{}", token.AccessToken(), 400)
	// Both real bearer checks must complete before either database mutation.
	type deletionResult struct {
		status      int
		body, cache string
		err         error
	}
	results := make(chan deletionResult, 2)
	for _, access := range []string{token.AccessToken(), second.AccessToken()} {
		go func(access string) {
			r, err := http.NewRequest("DELETE", "https://"+listener.Addr().String()+path, nil)
			if err != nil {
				results <- deletionResult{err: err}
				return
			}
			r.Header.Set("Authorization", "Bearer "+access)
			response, err := client.Do(r)
			if err != nil {
				results <- deletionResult{err: err}
				return
			}
			defer response.Body.Close()
			data, err := io.ReadAll(response.Body)
			results <- deletionResult{response.StatusCode, string(data), response.Header.Get("Cache-Control"), err}
		}(access)
	}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for range 2 {
		select {
		case id := <-barrier.arrived:
			if id != owner.UserID {
				t.Fatal("unexpected admitted owner")
			}
		case result := <-results:
			t.Fatalf("DELETE finished before both requests were admitted: status=%d transportError=%t", result.status, result.err != nil)
		case <-deadline.C:
			t.Fatal("both DELETE requests did not reach the post-authentication barrier")
		}
	}
	// Independently prove that neither request has deactivated the user yet.
	before, err := repo.ByID(ctx, owner.UserID)
	if err != nil || !before.Active {
		t.Fatal("user deactivated before barrier release")
	}
	release()
	for range 2 {
		select {
		case result := <-results:
			if result.err != nil || result.status != 204 || result.body != "" || result.cache != "no-store" {
				t.Fatalf("admitted DELETE failed: status=%d transportError=%t", result.status, result.err != nil)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("admitted DELETE did not finish")
		}
	}
	request("DELETE", path, "", token.AccessToken(), 401)
	request("PATCH", path, `{"displayName":"blocked"}`, second.AccessToken(), 401)
	request("POST", "/sessions", `{"email":"profile@example.com","password":"original-password"}`, "", 401)
	persisted, err := repo.ByID(ctx, owner.UserID)
	if err != nil || persisted.Active || persisted.Email != owner.Email || persisted.DisplayName != owner.DisplayName {
		t.Fatal("identity not retained inactive")
	}
	credential, err := repo.CredentialsByEmail(ctx, owner.Email)
	if err != nil || credential.PasswordHash() != hash {
		t.Fatal("credential changed")
	}
	var total, revoked int
	if err = conn.QueryRow(ctx, `SELECT count(*),count(revoked_at) FROM identity.sessions WHERE user_id=$1`, owner.UserID).Scan(&total, &revoked); err != nil || total != 2 || revoked != 2 {
		t.Fatal("sessions not retained and revoked")
	}
	if _, err = repo.Create(ctx, owner.Email, hash, nil); !errors.Is(err, users.ErrEmailAlreadyUsed) {
		t.Fatal("email released")
	}
	if _, err = issuer.Authenticate(ctx, otherToken.AccessToken()); err != nil {
		t.Fatal("other user's session changed")
	}
	if err = repo.SetActive(ctx, owner.UserID, true); err != nil {
		t.Fatal(err)
	}
	for _, access := range []string{token.AccessToken(), second.AccessToken()} {
		request("DELETE", path, "", access, 401)
	}
	issued, err := login.NewService(pool, nil).Authenticate(ctx, owner.Email, "original-password")
	if err != nil {
		t.Fatal("reenabled login failed")
	}
	request("DELETE", path, "", issued.AccessToken(), 204)
	stop()
	for _, secret := range []string{hash, "original-password", token.AccessToken(), second.AccessToken(), issued.AccessToken()} {
		if strings.Contains(logs.String(), secret) {
			t.Fatal("secret in logs")
		}
	}
}

func TestDeactivationRollback(t *testing.T) {
	conn, repo, ctx := userFixture(t)
	owner, err := repo.Create(ctx, "rollback@example.com", "stored-private-hash", nil)
	if err != nil {
		t.Fatal(err)
	}
	issuer := sessions.NewRepository(conn, nil)
	token, err := issuer.Issue(ctx, owner.UserID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = conn.Exec(ctx, `CREATE FUNCTION identity.fail_deactivation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'private-failure'; END $$;
 CREATE CONSTRAINT TRIGGER fail_deactivation AFTER UPDATE ON identity.sessions DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION identity.fail_deactivation()`)
	if err != nil {
		t.Fatal(err)
	}
	if err = profile.NewService(conn).Deactivate(ctx, owner.UserID); !errors.Is(err, users.ErrStorage) {
		t.Fatal("failed commit not mapped")
	}
	got, err := repo.ByID(ctx, owner.UserID)
	if err != nil || !got.Active {
		t.Fatal("active flag not rolled back")
	}
	if _, err = issuer.Authenticate(ctx, token.AccessToken()); err != nil {
		t.Fatal("session revocation not rolled back")
	}
	if err = profile.NewService(conn).Deactivate(ctx, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, users.ErrNotFound) {
		t.Fatal("missing user not rejected")
	}
}

// A session issued by a transaction holding login's lock must be seen by the
// deactivation's revocation statement after that transaction commits.
func TestDeactivationWaitsForIssuance(t *testing.T) {
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
			go func() { result <- profile.NewService(worker).Deactivate(waitCtx, owner.UserID) }()
			deadline := time.Now().Add(5 * time.Second)
			for {
				var blocked bool
				err = observer.QueryRow(ctx, `SELECT cardinality(pg_blocking_pids(pid))>0 AND query LIKE 'UPDATE identity.users%' FROM pg_stat_activity WHERE pid=$1`, pid).Scan(&blocked)
				if err != nil {
					t.Fatal(err)
				}
				if blocked {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("deactivation did not wait on user lock")
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
					t.Fatal("deactivation stuck")
				}
			}
			got, err := repo.ByID(ctx, owner.UserID)
			if err != nil || got.Active != cancelWaiting {
				t.Fatal("incorrect active state")
			}
			_, err = sessions.NewRepository(conn, nil).Authenticate(ctx, token.AccessToken())
			if cancelWaiting && err != nil || !cancelWaiting && !errors.Is(err, sessions.ErrInvalidSession) {
				t.Fatal("incorrect session state")
			}
		})
	}
}
