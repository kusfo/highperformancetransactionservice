package userapi

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kusfo/highperformancetransactionservice/src/login"
	"github.com/kusfo/highperformancetransactionservice/src/sessions"
)

type authFunc func(context.Context, string, string) (sessions.Issued, error)

func (f authFunc) Authenticate(c context.Context, e, p string) (sessions.Issued, error) {
	return f(c, e, p)
}
func TestLoginValidationAndErrors(t *testing.T) {
	for _, body := range []string{`{}`, `null`, strings.TrimSuffix(validBody, "}") + `,"displayName":"x"}`, strings.TrimSuffix(validBody, "}") + `,"accessToken":"secret"}`, `{"email":"person@example.com","password":null}`, `{"email":"person@example.com","password":""}`, strings.Replace(validBody, "example-password", strings.Repeat("界", 129), 1), `{"email":"person@example.com","email":"other@example.com","password":"test-password"}`} {
		h := NewHandlerWithLogin(nil, authFunc(func(context.Context, string, string) (sessions.Issued, error) {
			t.Error("invalid input reached auth")
			return sessions.Issued{}, nil
		}))
		r := request(body)
		r.URL.Path = "/sessions"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatalf("invalid login status %d", w.Code)
		}
	}
	for _, tc := range []struct {
		err    error
		status int
	}{{login.ErrInvalidCredentials, 401}, {errors.New("secret-driver-token-hash"), 500}} {
		h := NewHandlerWithLogin(nil, authFunc(func(_ context.Context, e, p string) (sessions.Issued, error) {
			if e != "person@example.com" || p != "x" {
				t.Error("input changed")
			}
			return sessions.Issued{}, tc.err
		}))
		r := request(strings.Replace(validBody, "example-password", "x", 1))
		r.URL.Path = "/sessions"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status || strings.Contains(w.Body.String(), "secret-") {
			t.Fatal("unsafe login failure")
		}
	}
	h := NewHandlerWithLogin(nil, authFunc(func(context.Context, string, string) (sessions.Issued, error) {
		t.Error("unexpected auth")
		return sessions.Issued{}, nil
	}))
	r := request(validBody)
	r.URL.Path = "/sessions"
	r.Method = "GET"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 405 || w.Header().Get("Allow") != "POST" {
		t.Fatal("method mismatch")
	}
	h.slots <- struct{}{}
	h.slots <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r = request(validBody).WithContext(ctx)
	r.URL.Path = "/sessions"
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 503 {
		t.Fatal("cancelled waiter accepted")
	}
}
