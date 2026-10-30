package userapi

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kusfo/highperformancetransactionservice/src/profile"
	"github.com/kusfo/highperformancetransactionservice/src/sessions"
	"github.com/kusfo/highperformancetransactionservice/src/users"
)

type profileChecker struct{ err error }

func (c profileChecker) Authenticate(context.Context, string) (sessions.Session, error) {
	return sessions.Session{UserID: "owner"}, c.err
}

type profileStub struct {
	calls int
	err   error
}

func (s *profileStub) Update(_ context.Context, id, name string) (users.User, error) {
	s.calls++
	return users.User{UserID: id, DisplayName: name}, s.err
}
func (s *profileStub) ChangePassword(context.Context, string, string, string) error {
	s.calls++
	return s.err
}
func TestProfileRoutes(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, body string
		err                      error
		status, calls            int
	}{
		{"update", "PATCH", "/users/owner", `{"displayName":"名字"}`, nil, 200, 1},
		{"change", "PUT", "/users/owner/password", `{"currentPassword":"old-password","newPassword":"new-password"}`, nil, 204, 1},
		{"wrong current", "PUT", "/users/owner/password", `{"currentPassword":"x","newPassword":"new-password"}`, profile.ErrCurrentPassword, 403, 1},
		{"other owner", "PATCH", "/users/other", `{"displayName":"Name"}`, nil, 404, 0},
		{"other password", "PUT", "/users/other/password", `{"currentPassword":"old-password","newPassword":"new-password"}`, nil, 404, 0},
		{"method", "POST", "/users/owner", `{}`, nil, 405, 0},
		{"subpath", "PATCH", "/users/owner/extra", `{}`, nil, 404, 0},
		{"short new", "PUT", "/users/owner/password", `{"currentPassword":"old-password","newPassword":"short"}`, nil, 400, 0},
		{"missing current", "PUT", "/users/owner/password", `{"newPassword":"new-password"}`, nil, 400, 0},
		{"storage", "PATCH", "/users/owner", `{"displayName":"Name"}`, errors.New("private-secret"), 500, 1},
		{"missing", "PATCH", "/users/owner", `{"displayName":"Name"}`, users.ErrNotFound, 404, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			editor := &profileStub{err: tc.err}
			h := NewHandlerWithProfile(nil, nil, profileChecker{}, editor)
			r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Authorization", "Bearer fixture")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status || editor.calls != tc.calls {
				t.Fatalf("status=%d calls=%d", w.Code, editor.calls)
			}
			if strings.Contains(w.Body.String(), "private-secret") {
				t.Fatal("error leaked")
			}
			if tc.status == 204 && w.Body.Len() != 0 {
				t.Fatal("204 has body")
			}
		})
	}
}
func TestProfileStrictInput(t *testing.T) {
	for _, body := range []string{`{}`, `null`, `[]`, `{"displayName":null}`, `{"displayName":2}`, `{"DisplayName":"Name"}`, `{"displayName":""}`, `{"displayName":"a\u0000b"}`, `{"displayName":"a","displayName":"b"}`, `{"email":"change@example.com"}`, `{"displayName":"a","active":false}`, `{"displayName":"a","password":"secret"}`, `{"displayName":"a","userId":"other"}`, `{"displayName":"a","saltedhashedpassword":"secret"}`, `{"displayName":"` + strings.Repeat("界", 101) + `"}`, `{"displayName":"` + string([]byte{255}) + `"}`, `{"displayName":"a"}{}`} {
		editor := &profileStub{}
		h := NewHandlerWithProfile(nil, nil, profileChecker{}, editor)
		r := httptest.NewRequest("PATCH", "/users/owner", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer fixture")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 400 || editor.calls != 0 {
			t.Fatalf("invalid input accepted: %d", w.Code)
		}
	}
}
func TestProfileAuthenticationPrecedesUpdate(t *testing.T) {
	editor := &profileStub{}
	h := NewHandlerWithProfile(nil, nil, profileChecker{sessions.ErrInvalidSession}, editor)
	r := httptest.NewRequest("PATCH", "/users/owner", strings.NewReader(`{"displayName":"Name"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer forged")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 || editor.calls != 0 {
		t.Fatal("unauthorized update")
	}
}
