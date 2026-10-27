//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kusfo/highperformancetransactionservice/src/httpapi"
	"github.com/kusfo/highperformancetransactionservice/src/login"
	"github.com/kusfo/highperformancetransactionservice/src/passwords"
	"github.com/kusfo/highperformancetransactionservice/src/sessions"
	"github.com/kusfo/highperformancetransactionservice/src/userapi"
	"github.com/kusfo/highperformancetransactionservice/src/users"
)

func TestLoginHTTPS(t *testing.T) {
	conn, repo, ctx := userFixture(t)
	password := " exact-password界 "
	hash, err := passwords.Hash(password)
	if err != nil {
		t.Fatal(err)
	}
	active, err := repo.Create(ctx, "login@example.com", hash, nil)
	if err != nil {
		t.Fatal(err)
	}
	inactive, err := repo.Create(ctx, "inactive@example.com", hash, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetActive(ctx, inactive.UserID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Create(ctx, "malformed@example.com", "malformed-hash", nil); err != nil {
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
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	cert, key, client := serviceCertificate(t)
	client.Timeout = 10 * time.Second
	httpCfg := httpapi.DefaultConfig()
	httpCfg.Addr = "127.0.0.1:0"
	httpCfg.TLSCertFile = cert
	httpCfg.TLSKeyFile = key
	logs := &safeLog{}
	runtime, err := httpapi.New(httpCfg, userapi.NewHandlerWithLogin(users.NewRepository(pool), login.NewService(pool, func() time.Time { return now })), logs)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", httpCfg.Addr)
	if err != nil {
		t.Fatal(err)
	}
	serveCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runtime.Serve(serveCtx, listener) }()
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
	submit := func(email, p string) (int, string) {
		t.Helper()
		data, _ := json.Marshal(map[string]string{"email": email, "password": p})
		response, err := client.Post("https://"+listener.Addr().String()+"/sessions", "application/json", strings.NewReader(string(data)))
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		if response.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("missing no-store")
		}
		return response.StatusCode, string(body)
	}
	var tokens []string
	for i := 0; i < 2; i++ {
		status, body := submit(" LOGIN@EXAMPLE.COM ", password)
		if status != 200 {
			t.Fatalf("login status %d", status)
		}
		var result struct {
			AccessToken string    `json:"accessToken"`
			TokenType   string    `json:"tokenType"`
			ExpiresIn   int       `json:"expiresIn"`
			ExpiresAt   time.Time `json:"expiresAt"`
		}
		var fields map[string]any
		if json.Unmarshal([]byte(body), &result) != nil || json.Unmarshal([]byte(body), &fields) != nil || len(fields) != 4 {
			t.Fatal("response schema")
		}
		if result.TokenType != "Bearer" || result.ExpiresIn != 600 || !result.ExpiresAt.Equal(now.Add(10*time.Minute)) {
			t.Fatal("incorrect expiry")
		}
		s, err := sessions.NewRepository(conn, func() time.Time { return now }).Authenticate(ctx, result.AccessToken)
		if err != nil || s.UserID != active.UserID {
			t.Fatal("returned token was not durably issued")
		}
		tokens = append(tokens, result.AccessToken)
	}
	if tokens[0] == tokens[1] {
		t.Fatal("login token reused")
	}
	var failureBody string
	for _, tc := range []struct{ email, p string }{{"unknown@example.com", password}, {"login@example.com", "wrong-password"}, {"inactive@example.com", password}, {"malformed@example.com", password}, {"login@example.com", "x"}, {"login@example.com", strings.TrimSpace(password)}} {
		status, body := submit(tc.email, tc.p)
		if status != 401 {
			t.Fatalf("bad credentials returned %d", status)
		}
		if failureBody == "" {
			failureBody = body
		}
		if body != failureBody {
			t.Fatal("credential failures distinguish identities")
		}
	}
	var count int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM identity.sessions").Scan(&count); err != nil || count != 2 {
		t.Fatal("failed login issued session")
	}
	pool.Close()
	status, body := submit("login@example.com", password)
	if status != 500 || strings.Contains(body, hash) {
		t.Fatal("unsafe outage response")
	}
	stop()
	for _, secret := range append(tokens, password, hash, "login@example.com") {
		if strings.Contains(logs.String(), secret) {
			t.Fatal("login secret logged")
		}
	}
}

func TestLoginCommitFailure(t *testing.T) {
	conn, repo, ctx := userFixture(t)
	hash, err := passwords.Hash("example-password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Create(ctx, "commit@example.com", hash, nil); err != nil {
		t.Fatal(err)
	}
	_, err = conn.Exec(ctx, `CREATE FUNCTION identity.fail_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'secret-commit-payload'; END $$;
 CREATE CONSTRAINT TRIGGER fail_commit AFTER INSERT ON identity.sessions DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION identity.fail_commit()`)
	if err != nil {
		t.Fatal(err)
	}
	issued, err := login.NewService(conn, nil).Authenticate(ctx, "commit@example.com", "example-password")
	if !errors.Is(err, login.ErrStorage) || issued.AccessToken() != "" {
		t.Fatal("failed commit exposed session")
	}
	var count int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM identity.sessions").Scan(&count); err != nil || count != 0 {
		t.Fatal("failed commit persisted session")
	}
}

func TestLoginWaitsForUserLifecycleChange(t *testing.T) {
	for _, change := range []string{"deactivation", "password"} {
		t.Run(change, func(t *testing.T) {
			conn, repo, ctx := userFixture(t)
			hash, err := passwords.Hash("example-password")
			if err != nil {
				t.Fatal(err)
			}
			user, err := repo.Create(ctx, "race-login@example.com", hash, nil)
			if err != nil {
				t.Fatal(err)
			}
			tx, err := conn.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(context.Background())
			if change == "deactivation" {
				_, err = tx.Exec(ctx, "UPDATE identity.users SET active=false WHERE user_id::text=$1", user.UserID)
			} else {
				_, err = tx.Exec(ctx, "UPDATE identity.users SET password_hash='replaced-hash' WHERE user_id::text=$1", user.UserID)
			}
			if err != nil {
				t.Fatal(err)
			}
			other, err := pgx.ConnectConfig(ctx, conn.Config().Copy())
			if err != nil {
				t.Fatal(err)
			}
			defer other.Close(context.Background())
			observer, err := pgx.ConnectConfig(ctx, conn.Config().Copy())
			if err != nil {
				t.Fatal(err)
			}
			defer observer.Close(context.Background())
			result := make(chan error, 1)
			go func() {
				issued, err := login.NewService(other, nil).Authenticate(ctx, "race-login@example.com", "example-password")
				if issued.AccessToken() != "" {
					err = errors.New("session issued")
				}
				result <- err
			}()
			deadline := time.Now().Add(5 * time.Second)
			for {
				var blocked bool
				if err := observer.QueryRow(ctx, "SELECT cardinality(pg_blocking_pids($1)) > 0", other.PgConn().PID()).Scan(&blocked); err != nil {
					t.Fatal(err)
				}
				if blocked {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("login did not wait on user row")
				}
				time.Sleep(10 * time.Millisecond)
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-result:
				if !errors.Is(err, login.ErrInvalidCredentials) {
					t.Fatal("stale credentials accepted")
				}
			case <-ctx.Done():
				t.Fatal("login wait timeout")
			}
			var count int
			if err := conn.QueryRow(ctx, "SELECT count(*) FROM identity.sessions").Scan(&count); err != nil || count != 0 {
				t.Fatal("lifecycle race issued session")
			}
		})
	}
}
