package accountapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kusfo/highperformancetransactionservice/src/financial"
	"github.com/kusfo/highperformancetransactionservice/src/sessions"
)

type checker struct{ err error }

func (c checker) Authenticate(context.Context, string) (sessions.Session, error) {
	return sessions.Session{UserID: "550e8400-e29b-41d4-a716-446655440000"}, c.err
}

type creator struct {
	calls    int
	owner    string
	metadata financial.Metadata
	err      error
}

func (c *creator) Create(ctx context.Context, owner string, m financial.Metadata) (financial.Account, error) {
	c.calls++
	c.owner, c.metadata = owner, m
	return financial.Account{AccountNumber: "000123", UserID: owner, DisplayName: m.DisplayName, Description: m.Description, Status: "active", CreatedAt: time.Now().UTC()}, c.err
}

func TestCreate(t *testing.T) {
	for _, body := range []string{`{}`, `{"displayName":"","description":""}`, `{"displayName":" 界😀 ","description":" description "}`, `{"displayName":"` + strings.Repeat("界", 100) + `","description":"` + strings.Repeat("😀", 1000) + `"}`} {
		t.Run(body[:2], func(t *testing.T) {
			repo := &creator{}
			h := NewHandler(repo, checker{})
			for range 2 {
				r := httptest.NewRequest("POST", "/accounts", strings.NewReader(body))
				r.Header.Set("Authorization", "Bearer test")
				r.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				var result map[string]any
				if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &result) != nil || len(result) != 7 || result["balance"] != float64(0) || result["status"] != "active" || result["userId"] != repo.owner || w.Header().Get("Location") != "/accounts/000123" || w.Header().Get("Cache-Control") != "no-store" {
					t.Fatalf("unexpected response: %d %s", w.Code, w.Body.String())
				}
				if result["displayName"] != repo.metadata.DisplayName || result["description"] != repo.metadata.Description {
					t.Fatal("metadata changed")
				}
			}
			if repo.calls != 2 || repo.owner != "550e8400-e29b-41d4-a716-446655440000" {
				t.Fatal("creation/retry ownership")
			}
		})
	}
}

func TestCreateRejectsInput(t *testing.T) {
	bodies := []string{``, `null`, `[]`, `{} {}`, `{"displayName":null}`, `{"description":1}`, `{"displayName":"a","displayName":"b"}`, `{"DisplayName":"a"}`, `{"displayName":"\ud800"}`, "{\"displayName\":\"\xff\"}", `{"description":"\u0000"}`, `{"displayName":"` + strings.Repeat("界", 101) + `"}`, `{"description":"` + strings.Repeat("界", 1001) + `"}`}
	for _, field := range []string{"userId", "owner", "balance", "accountNumber", "status", "currency", "accountId"} {
		bodies = append(bodies, `{"`+field+`":"override"}`)
	}
	for _, body := range bodies {
		t.Run(body, func(t *testing.T) {
			repo := &creator{}
			r := httptest.NewRequest("POST", "/accounts", strings.NewReader(body))
			r.Header.Set("Authorization", "Bearer test")
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			NewHandler(repo, checker{}).ServeHTTP(w, r)
			if w.Code != 400 || repo.calls != 0 {
				t.Fatalf("status=%d calls=%d", w.Code, repo.calls)
			}
		})
	}
}

func TestCreateFailures(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, token, contentType string
		key                                    bool
		authErr, storeErr                      error
		want                                   int
	}{
		{name: "unauthenticated", want: 401},
		{name: "invalid session", token: "test", authErr: sessions.ErrInvalidSession, want: 401},
		{name: "auth outage", token: "test", authErr: errors.New("private"), want: 500},
		{name: "unsupported method", method: "GET", token: "test", want: 405},
		{name: "unknown path", path: "/accounts/unknown", want: 404},
		{name: "media type", token: "test", contentType: "text/plain", want: 415},
		{name: "empty idempotency key", token: "test", key: true, want: 400},
		{name: "storage outage", token: "test", storeErr: errors.New("private"), want: 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			method, path, ct := tc.method, tc.path, tc.contentType
			if method == "" {
				method = "POST"
			}
			if path == "" {
				path = "/accounts"
			}
			if ct == "" {
				ct = "application/json"
			}
			repo := &creator{err: tc.storeErr}
			r := httptest.NewRequest(method, path, strings.NewReader(`{}`))
			r.Header.Set("Content-Type", ct)
			if tc.token != "" {
				r.Header.Set("Authorization", "Bearer "+tc.token)
			}
			if tc.key {
				r.Header.Set("Idempotency-Key", "")
			}
			w := httptest.NewRecorder()
			NewHandler(repo, checker{tc.authErr}).ServeHTTP(w, r)
			if w.Code != tc.want || strings.Contains(w.Body.String(), "private") {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
			if tc.storeErr == nil && repo.calls != 0 {
				t.Fatal("unexpected write")
			}
		})
	}
}
