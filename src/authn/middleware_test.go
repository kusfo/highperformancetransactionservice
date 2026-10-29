package authn

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kusfo/highperformancetransactionservice/src/sessions"
)

type checkFunc func(context.Context, string) (sessions.Session, error)

func (f checkFunc) Authenticate(c context.Context, t string) (sessions.Session, error) {
	return f(c, t)
}
func TestMalformedAuthorization(t *testing.T) {
	for _, headers := range [][]string{nil, {""}, {"Basic secret"}, {"Bearer"}, {"Bearer "}, {"Bearer\tsecret"}, {" Bearer secret"}, {"Bearer a b"}, {"Bearer a,b"}, {"Bearer first", "Bearer second"}} {
		checker := checkFunc(func(context.Context, string) (sessions.Session, error) {
			t.Error("malformed header reached checker")
			return sessions.Session{}, nil
		})
		handler := Require(checker, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unauthorized request dispatched") }))
		request := httptest.NewRequest("GET", "/private?accessToken=secret", nil)
		request.Header["Authorization"] = headers
		request.Header.Set("X-User-ID", "forged")
		request.AddCookie(&http.Cookie{Name: "accessToken", Value: "secret"})
		result := httptest.NewRecorder()
		handler.ServeHTTP(result, request)
		if result.Code != 401 || result.Header().Get("WWW-Authenticate") != "Bearer" || result.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("incorrect unauthorized response")
		}
	}
}
func TestIdentityAndFailureMapping(t *testing.T) {
	for _, tc := range []struct {
		session sessions.Session
		err     error
		status  int
	}{{sessions.Session{UserID: "validated"}, nil, 204}, {sessions.Session{}, sessions.ErrInvalidSession, 401}, {sessions.Session{}, errors.New("secret-storage-error"), 500}, {sessions.Session{}, nil, 500}} {
		called := false
		checker := checkFunc(func(ctx context.Context, token string) (sessions.Session, error) {
			if token != "secret-token" {
				t.Error("token changed")
			}
			return tc.session, tc.err
		})
		handler := Require(checker, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			id, ok := FromContext(r.Context())
			if !ok || id.UserID != "validated" {
				t.Error("wrong identity")
			}
			w.WriteHeader(204)
		}))
		request := httptest.NewRequest("GET", "/private", nil)
		request.Header.Set("Authorization", "bEaReR   secret-token")
		request.Header.Set("X-User-ID", "forged")
		result := httptest.NewRecorder()
		handler.ServeHTTP(result, request)
		if result.Code != tc.status || called != (tc.status == 204) {
			t.Fatal("authentication failed open")
		}
		if tc.status == 500 && result.Body.String() != `{"code":"INTERNAL_ERROR","message":"An internal error occurred."}`+"\n" {
			t.Fatal("unsafe storage error")
		}
		if _, ok := FromContext(request.Context()); ok {
			t.Fatal("mutated original context")
		}
	}
	if _, ok := FromContext(context.WithValue(context.Background(), "userId", "forged")); ok {
		t.Fatal("trusted external context key")
	}
}
func TestCancelledAuthentication(t *testing.T) {
	for _, before := range []bool{true, false} {
		ctx, cancel := context.WithCancel(context.Background())
		if before {
			cancel()
		}
		checker := checkFunc(func(context.Context, string) (sessions.Session, error) {
			if before {
				t.Error("cancelled request queried")
			}
			cancel()
			return sessions.Session{UserID: "validated"}, nil
		})
		handler := Require(checker, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("cancelled request dispatched") }))
		request := httptest.NewRequest("GET", "/private", nil).WithContext(ctx)
		request.Header.Set("Authorization", "Bearer token")
		result := httptest.NewRecorder()
		handler.ServeHTTP(result, request)
		cancel()
		if result.Code != 503 {
			t.Fatal("wrong cancellation response")
		}
	}
}
