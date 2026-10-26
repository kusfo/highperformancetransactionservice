package userapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kusfo/highperformancetransactionservice/src/users"
)

type createFunc func(context.Context, string, string, *string) (users.User, error)

func (f createFunc) Create(ctx context.Context, email, hash string, name *string) (users.User, error) {
	return f(ctx, email, hash, name)
}

func request(body string) *http.Request {
	r := httptest.NewRequest("POST", "/users", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}

const validBody = `{"email":" Person@Example.COM ","password":"example-password"}`

func TestRegistrationValidation(t *testing.T) {
	h := NewHandler(createFunc(func(context.Context, string, string, *string) (users.User, error) {
		t.Error("invalid input reached storage")
		return users.User{}, nil
	}))
	h.hash = func(string) (string, error) { t.Error("invalid input reached hashing"); return "", nil }
	for _, body := range []string{
		`{}`, `null`, `[]`, `{"email":null,"password":"example-password"}`,
		`{"email":"person@example.com","password":null}`, `{"email":"person@example.com","password":12}`,
		`{"Email":"person@example.com","password":"example-password"}`,
		`{"email":"person@example.com","email":"other@example.com","password":"example-password"}`,
		`{"email":"bad","password":"example-password"}`,
		`{"email":"é@example.com","password":"example-password"}`,
		`{"email":"person@example.com","password":"short"}`,
		strings.Replace(validBody, "example-password", strings.Repeat("a", 129), 1),
		strings.TrimSuffix(validBody, "}") + `,"displayName":null}`,
		strings.TrimSuffix(validBody, "}") + `,"displayName":"a\u0000b"}`,
		strings.TrimSuffix(validBody, "}") + `,"displayName":""}`,
		strings.TrimSuffix(validBody, "}") + `,"displayName":"` + strings.Repeat("界", 101) + `"}`,
		strings.TrimSuffix(validBody, "}") + `,"saltedhashedpassword":"secret-hash"}`,
		strings.TrimSuffix(validBody, "}") + `,"passwordHash":"secret-hash"}`,
		strings.TrimSuffix(validBody, "}") + `,"userId":"client-id"}`,
		strings.TrimSuffix(validBody, "}") + `,"active":false}`,
		validBody + `{}`, `{"email":"person@example.com","password":"example-` + string([]byte{255}) + `password"}`,
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, request(body))
		if w.Code != 400 || !strings.Contains(w.Body.String(), `"code":"INVALID_REQUEST"`) {
			t.Fatalf("invalid request returned %d", w.Code)
		}
	}
	for _, tc := range []struct {
		method, path string
		status       int
	}{{"GET", "/users", 405}, {"POST", "/users/", 404}, {"POST", "/sessions", 404}} {
		r := request(validBody)
		r.Method = tc.method
		r.URL.Path = tc.path
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatal("incorrect route status")
		}
		if tc.status == 405 && w.Header().Get("Allow") != "POST" {
			t.Fatal("missing Allow")
		}
	}
}

func TestRegistrationResults(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{nil, 201, ""}, {users.ErrEmailAlreadyUsed, 409, "EMAIL_ALREADY_USED"},
		{users.ErrInvalidInput, 400, "INVALID_REQUEST"}, {errors.New("secret-password-hash-token"), 500, "INTERNAL_ERROR"},
	} {
		h := NewHandler(createFunc(func(_ context.Context, email, hash string, name *string) (users.User, error) {
			if email != "person@example.com" || hash != "server-hash" || name != nil {
				t.Error("incorrect storage input")
			}
			return users.User{UserID: "550e8400-e29b-41d4-a716-446655440000", Email: email}, tc.err
		}))
		h.hash = func(p string) (string, error) {
			if p != "example-password" {
				t.Error("password changed")
			}
			return "server-hash", nil
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, request(validBody))
		if w.Code != tc.status {
			t.Fatalf("status %d", w.Code)
		}
		if tc.code != "" && !strings.Contains(w.Body.String(), tc.code) {
			t.Fatal("missing code")
		}
		if tc.status == 201 && w.Body.String() != `{"userId":"550e8400-e29b-41d4-a716-446655440000"}`+"\n" {
			t.Fatal("unsafe success fields")
		}
		for _, secret := range []string{"secret-password", "server-hash", "example-password", "person@example.com"} {
			if strings.Contains(w.Body.String(), secret) {
				t.Fatal("secret in response")
			}
		}
	}
	h := NewHandler(nil)
	h.hash = func(string) (string, error) { return "", errors.New("secret-hash-error") }
	w := httptest.NewRecorder()
	h.ServeHTTP(w, request(validBody))
	if w.Code != 500 || strings.Contains(w.Body.String(), "secret-hash-error") {
		t.Fatal("unsafe hashing failure")
	}
}

func TestRegistrationCancellationAndConcurrencyBound(t *testing.T) {
	var calls atomic.Int32
	h := NewHandler(createFunc(func(context.Context, string, string, *string) (users.User, error) {
		calls.Add(1)
		return users.User{}, nil
	}))
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	h.hash = func(string) (string, error) { entered <- struct{}{}; <-release; return "hash", nil }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan int, 2)
	for i := 0; i < 2; i++ {
		go func() {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, request(validBody).WithContext(ctx))
			done <- w.Code
		}()
	}
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("hash did not start")
		}
	}
	waiting, cancelWait := context.WithCancel(context.Background())
	cancelWait()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, request(validBody).WithContext(waiting))
	if w.Code != 503 {
		t.Fatal("cancelled waiting request accepted")
	}
	cancel()
	close(release)
	for i := 0; i < 2; i++ {
		select {
		case status := <-done:
			if status != 503 {
				t.Fatal("cancelled hash persisted")
			}
		case <-time.After(time.Second):
			t.Fatal("handler did not finish")
		}
	}
	if calls.Load() != 0 || len(h.slots) != 0 {
		t.Fatal("cancelled work persisted or leaked slot")
	}
}
