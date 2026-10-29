//go:build integration

package integration_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kusfo/highperformancetransactionservice/src/authn"
	"github.com/kusfo/highperformancetransactionservice/src/httpapi"
	"github.com/kusfo/highperformancetransactionservice/src/sessions"
)

func TestBearerAuthenticationHTTPS(t *testing.T) {
	conn, users, ctx := userFixture(t)
	user, err := users.Create(ctx, "auth@example.com", "fixture-hash", nil)
	if err != nil {
		t.Fatal(err)
	}
	var clock atomic.Int64
	start := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	clock.Store(start.UnixNano())
	now := func() time.Time { return time.Unix(0, clock.Load()).UTC() }
	issuer := sessions.NewRepository(conn, now)
	valid, err := issuer.Issue(ctx, user.UserID)
	if err != nil {
		t.Fatal(err)
	}
	revoked, err := issuer.Issue(ctx, user.UserID)
	if err != nil {
		t.Fatal(err)
	}
	if err := issuer.Revoke(ctx, revoked.AccessToken()); err != nil {
		t.Fatal(err)
	}
	poolCfg, err := pgxpool.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	poolCfg.ConnConfig.Database = conn.Config().Database
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	var calls atomic.Int32
	protected := authn.Require(sessions.NewRepository(pool, now), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		id, ok := authn.FromContext(r.Context())
		if !ok || id.UserID != user.UserID {
			httpapi.WriteError(w, 500, "TEST_FAILURE", "Wrong identity.")
			return
		}
		httpapi.WriteJSON(w, 200, map[string]string{"userId": id.UserID})
	}))
	cfg := httpapi.DefaultConfig()
	cfg.Addr = "127.0.0.1:0"
	cert, key, client := serviceCertificate(t)
	cfg.TLSCertFile = cert
	cfg.TLSKeyFile = key
	logs := &safeLog{}
	runtime, err := httpapi.New(cfg, protected, logs)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", cfg.Addr)
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
	request := func(headers []string, want int) {
		t.Helper()
		r, err := http.NewRequest("GET", "https://"+listener.Addr().String()+"/protected?accessToken="+valid.AccessToken(), nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, header := range headers {
			r.Header.Add("Authorization", header)
		}
		r.Header.Set("X-User-ID", "forged-user")
		r.AddCookie(&http.Cookie{Name: "accessToken", Value: valid.AccessToken()})
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != want {
			t.Fatalf("status %d, want %d", response.StatusCode, want)
		}
		if want == 401 && (response.Header.Get("WWW-Authenticate") != "Bearer" || string(body) != `{"code":"UNAUTHORIZED","message":"Authentication required."}`+"\n") {
			t.Fatal("inconsistent unauthorized result")
		}
		if strings.Contains(string(body), valid.AccessToken()) || strings.Contains(string(body), revoked.AccessToken()) {
			t.Fatal("token exposed")
		}
	}
	bearer := []string{"Bearer " + valid.AccessToken()}
	request(bearer, 200)
	for _, headers := range [][]string{nil, {"Basic secret"}, {"Bearer malformed"}, {"Bearer " + strings.Repeat("A", 43)}, {"Bearer " + revoked.AccessToken()}, {bearer[0], bearer[0]}} {
		request(headers, 401)
	}
	clock.Store(start.Add(10*time.Minute - time.Microsecond).UnixNano())
	request(bearer, 200)
	clock.Store(start.Add(10 * time.Minute).UnixNano())
	request(bearer, 401)
	clock.Store(start.UnixNano())
	if err := users.SetActive(ctx, user.UserID, false); err != nil {
		t.Fatal(err)
	}
	request(bearer, 401)
	if err := issuer.RevokeAll(ctx, user.UserID); err != nil {
		t.Fatal(err)
	}
	if err := users.SetActive(ctx, user.UserID, true); err != nil {
		t.Fatal(err)
	}
	request(bearer, 401)
	if calls.Load() != 2 {
		t.Fatal("unauthorized request reached handler")
	}
	pool.Close()
	request(bearer, 500)
	stop()
	for _, secret := range []string{valid.AccessToken(), revoked.AccessToken(), "forged-user", "fixture-hash"} {
		if strings.Contains(logs.String(), secret) {
			t.Fatal("auth input logged")
		}
	}
}
