package userapi

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kusfo/highperformancetransactionservice/src/sessions"
	"github.com/kusfo/highperformancetransactionservice/src/users"
)

type lifecycleStub struct {
	profileStub
	id string
}

func (s *lifecycleStub) Deactivate(_ context.Context, id string) error {
	s.calls++
	s.id = id
	return s.err
}
func TestDeactivationRoutes(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, body string
		authErr, err             error
		status, calls            int
	}{
		{"success", "DELETE", "/users/owner", "", nil, nil, 204, 1},
		{"other owner", "DELETE", "/users/other", "", nil, nil, 404, 0},
		{"unauthorized", "DELETE", "/users/owner", "", sessions.ErrInvalidSession, nil, 401, 0},
		{"body", "DELETE", "/users/owner", "{}", nil, nil, 400, 0},
		{"whitespace body", "DELETE", "/users/owner", " ", nil, nil, 400, 0},
		{"wrong path", "DELETE", "/users/owner/extra", "", nil, nil, 404, 0},
		{"password route", "DELETE", "/users/owner/password", "", nil, nil, 405, 0},
		{"wrong method", "POST", "/users/owner", "", nil, nil, 405, 0},
		{"missing", "DELETE", "/users/owner", "", nil, users.ErrNotFound, 404, 1},
		{"storage", "DELETE", "/users/owner", "", nil, errors.New("private-secret"), 500, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			editor := &lifecycleStub{profileStub: profileStub{err: tc.err}}
			h := NewHandlerWithLifecycle(nil, nil, profileChecker{tc.authErr}, editor)
			r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			r.Header.Set("Authorization", "Bearer fixture")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status || editor.calls != tc.calls {
				t.Fatalf("status=%d calls=%d", w.Code, editor.calls)
			}
			if editor.calls > 0 && editor.id != "owner" {
				t.Fatal("untrusted identity")
			}
			if strings.Contains(w.Body.String(), "private-secret") {
				t.Fatal("secret leaked")
			}
			if tc.status == 204 && (w.Body.Len() != 0 || w.Header().Get("Cache-Control") != "no-store") {
				t.Fatal("invalid success response")
			}
			if tc.name == "wrong method" && w.Header().Get("Allow") != "PATCH, DELETE" {
				t.Fatal("missing methods")
			}
		})
	}
}
