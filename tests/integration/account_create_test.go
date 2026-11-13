//go:build integration

package integration_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kusfo/highperformancetransactionservice/src/accountapi"
	"github.com/kusfo/highperformancetransactionservice/src/financial"
	"github.com/kusfo/highperformancetransactionservice/src/login"
	"github.com/kusfo/highperformancetransactionservice/src/passwords"
	"github.com/kusfo/highperformancetransactionservice/src/postgres"
	"github.com/kusfo/highperformancetransactionservice/src/sessions"
)

func TestAccountCreationHTTPS(t *testing.T) {
	admin, users, ctx := userFixture(t)
	logins := provisionRuntimeRoles(t, admin)
	db := runtimeRoleConnection(t, admin, logins[postgres.FinancialHTTPRole])
	hash, err := passwords.Hash("account-test-password")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(accountapi.NewHandler(financial.NewAccounts(db), sessions.NewRepository(db, nil)))
	t.Cleanup(server.Close)
	numbers := map[string]bool{}
	for _, email := range []string{"first@example.com", "second@example.com"} {
		user, err := users.Create(ctx, email, hash, nil)
		if err != nil {
			t.Fatal(err)
		}
		session, err := login.NewService(admin, nil).Authenticate(ctx, email, "account-test-password")
		if err != nil {
			t.Fatal(err)
		}
		for range 2 {
			request, err := http.NewRequest("POST", server.URL+"/accounts", strings.NewReader(`{"displayName":" 界😀 ","description":"test"}`))
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Authorization", "Bearer "+session.AccessToken())
			request.Header.Set("Content-Type", "application/json")
			response, err := server.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			var account financial.Account
			err = json.NewDecoder(response.Body).Decode(&account)
			response.Body.Close()
			if err != nil || response.StatusCode != 201 || account.UserID != user.UserID || account.Balance != 0 || account.Status != "active" || account.DisplayName != " 界😀 " || account.Description != "test" || account.CreatedAt.IsZero() || account.AccountNumber == "" || numbers[account.AccountNumber] {
				t.Fatalf("unexpected account response: status=%d err=%v", response.StatusCode, err)
			}
			numbers[account.AccountNumber] = true
			if response.Header.Get("Location") != "/accounts/"+account.AccountNumber {
				t.Fatal("Location mismatch")
			}
			stored, err := financial.NewAccounts(db).ByNumber(ctx, user.UserID, account.AccountNumber)
			if err != nil || stored.DisplayName != account.DisplayName || stored.Balance != 0 {
				t.Fatal("account not persisted")
			}
		}
	}
	var count int
	if err := admin.QueryRow(ctx, "SELECT count(*) FROM financial.accounts").Scan(&count); err != nil || count != 4 {
		t.Fatal("expected four distinct accounts")
	}
}
