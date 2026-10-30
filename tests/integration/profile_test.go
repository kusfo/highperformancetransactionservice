//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kusfo/highperformancetransactionservice/src/httpapi"
	"github.com/kusfo/highperformancetransactionservice/src/login"
	"github.com/kusfo/highperformancetransactionservice/src/passwords"
	"github.com/kusfo/highperformancetransactionservice/src/profile"
	"github.com/kusfo/highperformancetransactionservice/src/sessions"
	"github.com/kusfo/highperformancetransactionservice/src/userapi"
	"github.com/kusfo/highperformancetransactionservice/src/users"
)

func TestProfileHTTPS(t *testing.T) {
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
	logs := &safeLog{}
	runtime, err := httpapi.New(httpCfg, userapi.NewHandlerWithProfile(users.NewRepository(pool), login.NewService(pool, nil), issuer, profile.NewService(pool)), logs)
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
	request("PATCH", path, `{"displayName":"Name"}`, "", 401)
	request("PATCH", "/users/"+other.UserID, `{"displayName":"Changed"}`, token.AccessToken(), 404)
	request("PATCH", "/users/00000000-0000-0000-0000-000000000000", `{"displayName":"Changed"}`, token.AccessToken(), 404)
	request("PUT", "/users/"+other.UserID+"/password", `{"currentPassword":"original-password","newPassword":"replacement-password"}`, token.AccessToken(), 404)
	for _, name := range []string{"界", strings.Repeat("界", 100), "  Display Name  "} {
		body, _ := json.Marshal(map[string]string{"displayName": name})
		data := request("PATCH", path, string(body), token.AccessToken(), 200)
		var updated users.User
		if json.Unmarshal([]byte(data), &updated) != nil || updated.UserID != owner.UserID || updated.Email != owner.Email || updated.DisplayName != name || !updated.Active {
			t.Fatal("wrong profile response")
		}
	}
	for _, body := range []string{`{}`, `{"displayName":null}`, `{"displayName":""}`, `{"displayName":"a\u0000b"}`, `{"displayName":"Name","email":"other@example.com"}`, `{"active":false}`, `{"password":"replacement-password"}`} {
		request("PATCH", path, body, token.AccessToken(), 400)
	}
	credentials, err := repo.CredentialsByEmail(ctx, owner.Email)
	if err != nil || credentials.PasswordHash() != hash {
		t.Fatal("profile changed credentials")
	}
	unchanged, err := repo.ByID(ctx, other.UserID)
	if err != nil || unchanged.DisplayName != "" {
		t.Fatal("other profile changed")
	}
	request("PUT", path+"/password", `{"currentPassword":"wrong-password","newPassword":"replacement-password"}`, token.AccessToken(), 403)
	if _, err := issuer.Authenticate(ctx, second.AccessToken()); err != nil {
		t.Fatal("failed change revoked session")
	}
	body := request("PUT", path+"/password", `{"currentPassword":"original-password","newPassword":"replacement-password"}`, token.AccessToken(), 204)
	if body != "" {
		t.Fatal("204 body")
	}
	for _, access := range []string{token.AccessToken(), second.AccessToken()} {
		request("PATCH", path, `{"displayName":"Name"}`, access, 401)
	}
	if _, err := issuer.Authenticate(ctx, otherToken.AccessToken()); err != nil {
		t.Fatal("other user's session revoked")
	}
	auth := login.NewService(pool, nil)
	if _, err := auth.Authenticate(ctx, owner.Email, "original-password"); !errors.Is(err, login.ErrInvalidCredentials) {
		t.Fatal("old password works")
	}
	fresh, err := auth.Authenticate(ctx, owner.Email, "replacement-password")
	if err != nil {
		t.Fatal(err)
	}
	request("PATCH", path, `{"displayName":"After login"}`, fresh.AccessToken(), 200)
	stop()
	for _, secret := range []string{hash, "original-password", "replacement-password", token.AccessToken(), second.AccessToken(), fresh.AccessToken()} {
		if strings.Contains(logs.String(), secret) {
			t.Fatal("secret logged")
		}
	}
}

func TestPasswordChangeRollbackAndConcurrency(t *testing.T) {
	conn, repo, ctx := userFixture(t)
	hash, err := passwords.Hash("original-password")
	if err != nil {
		t.Fatal(err)
	}
	owner, err := repo.Create(ctx, "atomic@example.com", hash, nil)
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
	// Fail at commit, after both the credential update and revocation ran.
	_, err = conn.Exec(ctx, `CREATE FUNCTION identity.reject_password_change() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'private database detail'; END $$;
 CREATE CONSTRAINT TRIGGER reject_password_change AFTER UPDATE ON identity.users DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION identity.reject_password_change()`)
	if err != nil {
		t.Fatal(err)
	}
	service := profile.NewService(pool)
	if err := service.ChangePassword(ctx, owner.UserID, "original-password", "replacement-password"); !errors.Is(err, users.ErrStorage) {
		t.Fatal("commit failure not reported safely")
	}
	credentials, err := repo.CredentialsByEmail(ctx, owner.Email)
	if err != nil || credentials.PasswordHash() != hash {
		t.Fatal("failed commit changed password")
	}
	if _, err := issuer.Authenticate(ctx, token.AccessToken()); err != nil {
		t.Fatal("failed commit revoked session")
	}
	if _, err := conn.Exec(ctx, `DROP TRIGGER reject_password_change ON identity.users`); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			results <- service.ChangePassword(ctx, owner.UserID, "original-password", "replacement-password")
		}()
	}
	close(start)
	successes, declines := 0, 0
	for i := 0; i < 2; i++ {
		err := <-results
		switch {
		case err == nil:
			successes++
		case errors.Is(err, profile.ErrCurrentPassword):
			declines++
		default:
			t.Fatal(err)
		}
	}
	if successes != 1 || declines != 1 {
		t.Fatal("concurrent changes did not serialize")
	}
	if _, err := issuer.Authenticate(ctx, token.AccessToken()); !errors.Is(err, sessions.ErrInvalidSession) {
		t.Fatal("session survived password change")
	}
	if err := repo.SetActive(ctx, owner.UserID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Update(ctx, owner.UserID, "Name"); !errors.Is(err, users.ErrNotFound) {
		t.Fatal("inactive profile updated")
	}
	if err := service.ChangePassword(ctx, owner.UserID, "replacement-password", "another-password"); !errors.Is(err, users.ErrNotFound) {
		t.Fatal("inactive password changed")
	}
}
